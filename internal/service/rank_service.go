package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/engine"
	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 榜单与大屏
//
// 本层只负责「取数 + 调引擎 + 分页 + 脱敏」，不做任何计算 ——
// 计分与排名属于 engine 的职责，那里有 100% 覆盖率的单测在守。
//
// 一次榜单请求的数据来源固定为三次查询（赛项 / 队伍 / 成绩），
// 刻意避免 N+1：大屏每 5 秒轮询一次，榜单接口必须是 O(1) 次查询。
// ============================================================================

// StandingsOptions 榜单查询选项。
type StandingsOptions struct {
	// Group 只看某个组别；留空则返回全部组别（分组返回）。
	Group string
	// IncludeWithdrawn 是否把弃赛队伍计入。默认 false。
	IncludeWithdrawn bool
	// OnlySigned 是否只统计已签字轮次。正式公示前应置 true。
	OnlySigned bool
	// AwardOnlyComplete 是否只给完成全部任务录入的队伍发奖。
	// 正式公示前应置 true —— 否则「一场没比的队伍也拿奖」。
	AwardOnlyComplete bool
}

// StandingsResult 榜单结果。
type StandingsResult struct {
	EventID    string                  `json:"eventId"`
	EventName  string                  `json:"eventName"`
	RefTime    float64                 `json:"refTime"`
	Groups     []model.GroupStandings  `json:"groups"` // 分组榜单（组内独立名次与奖项）
	Teams      int                     `json:"teams"`  // 参与排名的队伍数
	Validation engine.ValidationResult `json:"validation"`
}

// Standings 计算榜单。
//
// 返回**按组别分组**的结果，而不是一张混合大表 —— 因为公示表样例明确
// 「小学组与中学组各自有冠军/亚军/季军」，名次与奖项都是组内独立的。
func (s *Service) Standings(ctx context.Context, eventID string, opts StandingsOptions) (*StandingsResult, error) {
	ev, err := s.ro().Events.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	teams, err := s.ro().Teams.ListByEvent(ctx, eventID, opts.IncludeWithdrawn)
	if err != nil {
		return nil, err
	}
	scores, err := s.ro().Scores.ListByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}

	rankOpts := engine.RankOptions{
		Group:             opts.Group,
		RefTime:           engine.RefTimeFor(ev, 0),
		IncludeWithdrawn:  opts.IncludeWithdrawn,
		OnlySigned:        opts.OnlySigned,
		AwardOnlyComplete: opts.AwardOnlyComplete,
	}
	in := engine.RankInput{Event: ev, Teams: teams, Scores: scores}

	res := &StandingsResult{
		EventID:    ev.ID,
		EventName:  ev.Name,
		RefTime:    rankOpts.RefTime,
		Teams:      len(teams),
		Validation: engine.ValidateEvent(ev),
	}
	if opts.Group != "" {
		res.Groups = []model.GroupStandings{{Group: opts.Group, Rows: engine.Rank(in, rankOpts)}}
	} else {
		res.Groups = engine.RankAllGroups(in, rankOpts)
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// 大屏
// ---------------------------------------------------------------------------

// ScreenPageData 大屏一屏数据（含分页元信息），姓名已脱敏。
type ScreenPageData struct {
	EventID     string            `json:"eventId"`
	EventName   string            `json:"eventName"`
	Page        int               `json:"page"` // 从 1 开始
	TotalPage   int               `json:"totalPage"`
	TotalRows   int               `json:"totalRows"`
	PageSize    int               `json:"pageSize"`
	IntervalSec int               `json:"intervalSec"` // 本屏停留秒数
	Locked      bool              `json:"locked"`      // 运营已「置顶 / 锁定本场」
	Pinned      string            `json:"pinned,omitempty"`
	Rows        []model.ScreenRow `json:"rows"`
}

// ScreenPage 取大屏的某一屏。
//
// 三个刻意的实现选择：
//
//  1. **顺序按组别分区**：先按赛项配置的组别顺序排（小学组在上），
//     组内再按名次。这样「同一赛项不同组别同页分区展示」在前端只是
//     按 Group 字段分组渲染，不需要前端再做排序。
//  2. **姓名在后端脱敏**：大屏入口不止一个，只要有一处前端忘了调用脱敏函数
//     就会泄露未成年人真实姓名。后端直接返回脱敏结果，前端没有出错的机会。
//  3. **锁定即停播**：Pinned 非空表示运营已锁定本屏，前端应停止轮播。
func (s *Service) ScreenPage(ctx context.Context, eventID string, page int) (*ScreenPageData, error) {
	ev, err := s.ro().Events.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	cfg, err := s.ro().Screen.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}

	groups, err := s.groupedRows(ctx, eventID)
	if err != nil {
		return nil, err
	}

	// 按组别顺序铺平；每行携带组内名次与组别，前端按 Group 分区即可
	flat := make([]model.ScreenRow, 0, len(groups))
	for _, g := range groups {
		for _, r := range g.Rows {
			flat = append(flat, model.ScreenRow{
				Rank:     r.Rank,
				TeamName: r.Team.Name,
				School:   r.Team.School,
				Group:    g.Group,
				Members:  engine.MaskMembers(r.Team.Members),
				Award:    r.Award,
			})
		}
	}

	size := cfg.PageSize
	if size <= 0 {
		size = model.DefaultPageSize
	}
	totalRows := len(flat)
	totalPage := (totalRows + size - 1) / size
	if totalPage == 0 {
		totalPage = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPage {
		page = totalPage
	}
	start := (page - 1) * size
	end := start + size
	if start > totalRows {
		start = totalRows
	}
	if end > totalRows {
		end = totalRows
	}

	out := &ScreenPageData{
		EventID:     ev.ID,
		EventName:   ev.Name,
		Page:        page,
		TotalPage:   totalPage,
		TotalRows:   totalRows,
		PageSize:    size,
		IntervalSec: cfg.IntervalSec,
		Locked:      cfg.Pinned != "",
		Pinned:      cfg.Pinned,
		Rows:        append([]model.ScreenRow{}, flat[start:end]...),
	}
	return out, nil
}

// GetScreenConfig 读取大屏配置（未配置时返回默认值）。
func (s *Service) GetScreenConfig(ctx context.Context, eventID string) (*model.ScreenConfig, error) {
	return s.ro().Screen.Get(ctx, eventID)
}

// UpdateScreenConfig 更新大屏配置（每屏条数 / 停留秒数 / 置顶锁定）。
//
// 留痕：锁定与解除锁定必须可追溯 —— 现场「屏幕被谁定住了」是要能被回答的问题。
func (s *Service) UpdateScreenConfig(ctx context.Context, cfg *model.ScreenConfig, reason string) (*model.ScreenConfig, error) {
	old, err := s.ro().Screen.Get(ctx, cfg.EventID)
	if err != nil {
		return nil, err
	}
	cfg.Normalize()

	if strings.TrimSpace(reason) == "" {
		reason = "大屏轮播参数调整"
	}
	lockChanged := old.Pinned != cfg.Pinned

	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Screen.Upsert(ctx, cfg); err != nil {
			return err
		}
		if !lockChanged && old.PageSize == cfg.PageSize && old.IntervalSec == cfg.IntervalSec {
			return nil // 无实质变化就不留噪音记录
		}
		return log(ctx, r, model.ActConfig,
			fmt.Sprintf("大屏配置（%s）", cfg.EventID),
			describeScreen(old), describeScreen(cfg), reason)
	}); err != nil {
		return nil, err
	}
	return cfg, nil
}

// groupedRows 取「按组别分区」的榜单（大屏与公示共用）。
func (s *Service) groupedRows(ctx context.Context, eventID string) ([]model.GroupStandings, error) {
	res, err := s.Standings(ctx, eventID, StandingsOptions{})
	if err != nil {
		return nil, err
	}
	return res.Groups, nil
}

func describeScreen(c *model.ScreenConfig) string {
	if c == nil {
		return ""
	}
	lock := "未锁定"
	if c.Pinned != "" {
		lock = "已锁定本场（" + c.Pinned + "）"
	}
	return fmt.Sprintf("每屏 %d 条 / 停留 %d 秒 / %s", c.PageSize, c.IntervalSec, lock)
}
