package service

import (
	"context"
	"fmt"

	"github.com/jialangli/comptition-score-server/internal/engine"
	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 开赛就绪度（只读聚合）
//
// 「就绪度」不是新算分：它把赛项校验、队伍归台、场次派生、裁判激活这些
// **已有事实**拼成一屏（demo「总览」页 readinessOf / readinessItems 的后端同源实现）。
// 此前这套拼装只存在于 demo 前端视图 —— 任何非 demo 客户端都拿不到同一口径的结论，
// 本端点补齐它。只读：不写任何表、不留审计。
//
// 三条与 demo 逐字对齐的口径（readiness_test 逐项断言）：
//  1. 场次计数**排除独立场次**（加时赛 / 重赛走场内快照，不属于「开赛编排」）
//  2. 「队伍已分台」连**弃赛队**一起数 —— demo 就是这么算的；弃赛队没归台
//     同样亮红。想让它是绿的，把队伍归台或先办弃赛手续，口径不因退赛放水
//  3. 「场次已排班」看的是 slots.judges（0021 排班读写接口），正式场次
//     一个裁判都没有即亮红 —— 排班接口补齐后该项与 demo 完全同口径
// ============================================================================

// ReadinessCounters 就绪度计数（当前赛事分区内的实时统计）。
type ReadinessCounters struct {
	Events     int `json:"events"`     // 赛项数
	Teams      int `json:"teams"`      // 队伍数（含弃赛，与 demo 同口径）
	Slots      int `json:"slots"`      // 正式场次数（不含加时 / 重赛）
	Refs       int `json:"refs"`       // 裁判码数（含未激活 / 已作废）
	RuleBad    int `json:"ruleBad"`    // 配置校验未通过的赛项数
	Unseated   int `json:"unseated"`   // 未分台（或归台指向已删赛台）的队伍数
	Unslotted  int `json:"unslotted"`  // 已分台却排不进任何场次的队伍数
	EmptySlots int `json:"emptySlots"` // 没派生出队伍的正式场次数
	Unstaffed  int `json:"unstaffed"`  // 没排裁判的正式场次数（judges 为空）
	Idle       int `json:"idle"`       // 未激活裁判数（建档未用 + 已作废）
}

// ReadinessItem 一条就绪检查项。OK=false 时 Bad 给出可执行的整改指引。
type ReadinessItem struct {
	Key  string `json:"key"`
	OK   bool   `json:"ok"`
	Good string `json:"good,omitempty"`
	Bad  string `json:"bad,omitempty"`
}

// ReadinessReport 聚合结果。Ready = 全部检查项通过；Gaps 列出后端尚无法评估的项。
type ReadinessReport struct {
	Counters ReadinessCounters `json:"counters"`
	Items    []ReadinessItem   `json:"items"`
	Ready    bool              `json:"ready"`
	Gaps     []string          `json:"gaps,omitempty"`
}

// Readiness 汇总当前赛事的开赛就绪度。
func (s *Service) Readiness(ctx context.Context) (*ReadinessReport, error) {
	events, err := s.ro().Events.List(ctx)
	if err != nil {
		return nil, err
	}
	seats, err := s.ro().Seats.List(ctx)
	if err != nil {
		return nil, err
	}
	slots, err := s.ro().Slots.List(ctx, 0, "")
	if err != nil {
		return nil, err
	}
	refs, err := s.ro().Referees.ListByContest(ctx)
	if err != nil {
		return nil, err
	}

	seatIDs := make(map[int64]bool, len(seats))
	for _, st := range seats {
		seatIDs[st.ID] = true
	}
	// 场次口径：只看正式场次。切片显式新建，避免 :0:0 之外的老写法 aliasing 底层数组。
	main := make([]model.Slot, 0, len(slots))
	for _, sl := range slots {
		if !sl.Type.Extra() {
			main = append(main, sl)
		}
	}

	// 队伍按赛项逐个取（TeamRepo 没有跨赛项列表；赛项个位数，不值得为此加接口）。
	var teams []model.Team
	for i := range events {
		ts, err := s.ro().Teams.ListByEvent(ctx, events[i].ID, true)
		if err != nil {
			return nil, err
		}
		teams = append(teams, ts...)
	}

	c := ReadinessCounters{
		Events: len(events), Teams: len(teams), Slots: len(main), Refs: len(refs),
	}
	for i := range events {
		if res := engine.ValidateEvent(&events[i]); !res.OK() {
			c.RuleBad++
		}
	}
	// 未分台 = 没归台，或归台指向一张已不存在的赛台（后者原先在 demo 里被漏判过，
	// 队伍隐身却绿灯 —— 判据必须两头一致）。
	for _, t := range teams {
		if t.SeatID == nil || !seatIDs[*t.SeatID] {
			c.Unseated++
		}
	}
	// 空场次 = 正式场次没派生出任何队伍（TeamIDs 由 Slots.List 派生填好）。
	for _, sl := range main {
		if len(sl.TeamIDs) == 0 {
			c.EmptySlots++
		}
	}
	// 未排班 = 正式场次一个执裁裁判都没有（slots.judges 为空，0021）。
	for _, sl := range main {
		if len(sl.Judges) == 0 {
			c.Unstaffed++
		}
	}
	// 已分台却排不进任何场次：该赛台缺对应「时段 × 赛项 × 组别 × 轮次」的场次。
	// 轮次匹配用 TeamSession.Includes（空值按 both 解释）—— 与派生 SQL 同口径。
	for _, t := range teams {
		if t.SeatID == nil || !seatIDs[*t.SeatID] || t.Status == model.TeamWithdrawn {
			continue
		}
		fits := false
		for _, sl := range main {
			if sl.SeatID == *t.SeatID && sl.EventID == t.EventID &&
				sl.GroupCode == t.GroupCode && t.Session.Includes(sl.RoundNo) {
				fits = true
				break
			}
		}
		if !fits {
			c.Unslotted++
		}
	}
	// 裁判激活：只要不是 activated（建档未用 / 已作废）都算未激活 ——
	// 「本人还没在平板上登录过」是现场最容易卡住的点。
	for _, r := range refs {
		if r.Status != model.RefereeActivated {
			c.Idle++
		}
	}

	items := []ReadinessItem{
		{Key: "规则已校验", OK: c.Events > 0 && c.RuleBad == 0,
			Good: fmt.Sprintf("%d 个赛项全部通过", c.Events),
			Bad:  pickStr(c.Events == 0, "还没有配置赛项", fmt.Sprintf("%d 个赛项配置有误", c.RuleBad))},
		{Key: "队伍已导入", OK: c.Teams > 0,
			Good: fmt.Sprintf("%d 支在册", c.Teams),
			Bad:  "还没有队伍（可走 WRC 报名导入，或手工补报）"},
		{Key: "队伍已分台", OK: c.Teams > 0 && c.Unseated == 0,
			Good: fmt.Sprintf("%d 支全部分台", c.Teams),
			Bad:  fmt.Sprintf("%d 支未分台 —— 到「赛台与赛程编排」的「分台与顺位」点「一键自动分台」", c.Unseated)},
		{Key: "分台已排进场次", OK: c.Teams > 0 && c.Unslotted == 0,
			Good: "已分台队伍都能排进场次",
			Bad:  fmt.Sprintf("%d 支排不进任何场次 —— 该赛台缺对应时段 / 赛项 / 组别的场次（场次队伍由「分台与顺位」派生，不是手排的）", c.Unslotted)},
		{Key: "场次已排队伍", OK: c.Slots > 0 && c.EmptySlots == 0,
			Good: fmt.Sprintf("%d 个场次都排了队伍", c.Slots),
			Bad:  pickStr(c.Slots == 0, "还没有编排场次", fmt.Sprintf("%d 个场次是空的 —— 到「分台与顺位」把队伍归台（或补该时段的场次）", c.EmptySlots))},
		{Key: "场次已排班", OK: c.Slots > 0 && c.Unstaffed == 0,
			Good: "全部场次已排班",
			Bad:  pickStr(c.Slots == 0, "还没有编排场次", fmt.Sprintf("%d 个场次没排裁判 —— 到「执裁排班」一键按预绑排班", c.Unstaffed))},
		{Key: "裁判已激活", OK: c.Refs > 0 && c.Idle == 0,
			Good: fmt.Sprintf("%d 人全部激活", c.Refs),
			Bad:  pickStr(c.Refs == 0, "还没有裁判档案", fmt.Sprintf("%d 人未激活 —— 本人还没在平板上登录过，现场最容易卡在这里", c.Idle))},
	}

	ready := true
	for _, it := range items {
		if !it.OK {
			ready = false
			break
		}
	}
	// Gaps 保留为契约字段（omitempty）：将来再有「后端暂时评估不了」的检查项，
	// 放这里显式声明，而不是静默缺项。当前排班已接（0021），无缺口。
	return &ReadinessReport{
		Counters: c,
		Items:    items,
		Ready:    ready,
	}, nil
}

// pickStr 三元表达式替身：cond 为真取 a，否则取 b。
func pickStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
