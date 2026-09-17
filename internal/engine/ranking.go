package engine

import (
	"math"
	"sort"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 排名与奖项
//
// 与计分同样的原则：纯函数、确定性、可重复执行。
//
// 「确定性」在排名里是硬要求：同一份成绩反复计算，名次必须一模一样，
// 否则成绩公示后再复核会得出不同榜单，现场无法解释。
// 为此本文件里所有涉及 map 遍历的地方都显式排序 —— Go 的 map 迭代顺序是随机的，
// 拿它决定奖项分配顺序会产出「每次刷新都不一样」的榜单。
// ============================================================================

// RankInput 排名输入。
type RankInput struct {
	Event  *model.Event
	Teams  []model.Team                  // 该赛项的全部队伍（可含已弃赛，由选项决定是否参与）
	Scores map[int64][]model.ScoreRecord // teamID → 该队全部轮次的打分记录
}

// RankOptions 排名选项。零值即为「全部组别混合排名、含弃赛、不限签字」，
// 与前端原型 standings() 的行为一致，便于两边比对。
type RankOptions struct {
	// Group 只排某个组别；留空表示该赛项全部组别混合排名。
	Group string

	// RefTime 时间奖励基准时长；<=0 时取赛项配置 ev.scoreRule.params.refTime，
	// 仍未配置则用 DefaultRefTime。
	RefTime float64

	// IncludeWithdrawn 是否把已弃赛队伍计入榜单。
	// 默认 false —— 弃赛队伍不应再参与名次与奖项分配（与前端原型的差异点，见 PROGRESS.md）。
	IncludeWithdrawn bool

	// OnlySigned 是否只统计选手代表已签字的轮次。
	// 默认 false：与前端原型一致，未签字也先参与试排名（现场需要实时看名次）。
	// 正式公示前应置为 true。
	OnlySigned bool

	// AwardOnlyComplete 是否只向「全部任务均已录入」的队伍分配奖项。
	//
	// 默认 false（与前端原型一致）。但**前端原型的这个行为是错的**：
	// 种子里尚未比赛的队伍（成绩 0、complete=false）照样会分到「三等奖」，
	// 因为它们只按名次序号取奖项。正式公示前应置为 true，
	// 否则会出现「一场没比的队也拿奖」的事故。
	AwardOnlyComplete bool

	// NameLess 名次完全相同时的兜底排序函数；nil 时按 Unicode 码点升序。
	// 之所以做成可注入：中文姓名的拼音序需要 collator（如 x/text/collate），
	// 而本包刻意不引入任何第三方依赖。需要拼音序时由调用方注入即可。
	NameLess func(a, b string) int
}

// Rank 计算榜单（单个组别或全部组别混合），按名次升序返回。
//
// 排序规则：
//
//  1. 总分降序
//  2. 依 tieBreak 逐项裁决（当前支持 time：用时少者优先；score 由第 1 步覆盖）
//  3. 仍未分出名次 → 按队名（可注入排序器）→ 队号 兜底，保证顺序确定
//
// 奖项分配：按奖项占比 × 榜单总数向上取整，从第 1 名起依次分配。
// 占比之和不足 1 时靠后队伍无奖项；累计超出榜单总数时自动截断。
func Rank(in RankInput, opts RankOptions) []model.StandingRow {
	if in.Event == nil {
		return nil
	}
	ev := in.Event
	refTime := RefTimeFor(ev, opts.RefTime)

	rows := make([]model.StandingRow, 0, len(in.Teams))
	for i := range in.Teams {
		t := in.Teams[i]
		if t.EventID != "" && t.EventID != ev.ID {
			continue // 防御：传入了不属于本赛项的队伍
		}
		if !opts.IncludeWithdrawn && t.Status == model.TeamWithdrawn {
			continue
		}
		if opts.Group != "" && t.GroupCode != opts.Group {
			continue
		}

		recs := in.Scores[t.ID]
		if opts.OnlySigned {
			recs = signedOnly(recs)
		}
		best, has := BestOf(ev, recs, refTime)

		row := model.StandingRow{
			Team:   t,
			Result: best.Result,
			Rounds: RoundNumbers(recs),
		}
		if has && best.Record != nil {
			row.Duration = finiteOr0(best.Record.DurationSec)
			row.BestRound = best.RoundNo
		}
		rows = append(rows, row)
	}

	sortRows(rows, ev.RankRule.TieBreak, opts.NameLess)

	for i := range rows {
		rows[i].Rank = i + 1
		rows[i].Tie = i > 0 && rows[i-1].Result.Total == rows[i].Result.Total
	}

	assignAwards(rows, ev.RankRule.AwardTiers, opts.AwardOnlyComplete)
	return rows
}

// RankAllGroups 按组别独立排名（公示表 / 大屏使用）。
//
// 返回顺序：先按赛项配置的组别顺序（小学组在上、初中组在下由配置决定），
// 再追加配置里未声明但队伍数据中出现的组别（按名称升序），保证输出稳定。
func RankAllGroups(in RankInput, opts RankOptions) []model.GroupStandings {
	if in.Event == nil {
		return nil
	}
	ev := in.Event

	present := make(map[string]bool, len(in.Teams))
	for i := range in.Teams {
		t := in.Teams[i]
		if t.EventID != "" && t.EventID != ev.ID {
			continue
		}
		if t.GroupCode != "" {
			present[t.GroupCode] = true
		}
	}

	order := make([]string, 0, len(present))
	declared := make(map[string]bool, len(ev.Groups))
	for _, g := range ev.Groups {
		if present[g] && !declared[g] {
			order = append(order, g)
			declared[g] = true
		}
	}
	extra := make([]string, 0, len(present))
	for g := range present {
		if !declared[g] {
			extra = append(extra, g)
		}
	}
	sort.Strings(extra)
	order = append(order, extra...)

	out := make([]model.GroupStandings, 0, len(order))
	for _, g := range order {
		sub := opts
		sub.Group = g
		out = append(out, model.GroupStandings{Group: g, Rows: Rank(in, sub)})
	}
	return out
}

// ============================================================================
// 内部实现
// ============================================================================

func signedOnly(recs []model.ScoreRecord) []model.ScoreRecord {
	out := make([]model.ScoreRecord, 0, len(recs))
	for i := range recs {
		if recs[i].Signed {
			out = append(out, recs[i])
		}
	}
	return out
}

func sortRows(rows []model.StandingRow, tieBreak []string, nameLess func(a, b string) int) {
	if nameLess == nil {
		nameLess = CompareNameZh
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Result.Total != b.Result.Total {
			return a.Result.Total > b.Result.Total
		}
		for _, tb := range tieBreak {
			if tb == "time" && a.Duration != b.Duration {
				return a.Duration < b.Duration
			}
		}
		if c := nameLess(a.Team.Name, b.Team.Name); c != 0 {
			return c < 0
		}
		return a.Team.TeamNo < b.Team.TeamNo
	})
}

// tierRatio 奖项及其占比。
type tierRatio struct {
	Name  string
	Ratio float64
}

// awardOrder 奖项由高到低的固定顺序。
//
// 未列出的自定义奖项按占比降序排在后面 —— 占比越高通常档位越高，
// 这是一个可解释的兜底，而不是随机顺序。
var awardOrder = []string{"特等奖", "一等奖", "二等奖", "三等奖", "优胜奖"}

// orderedTiers 把奖项映射转成有序切片。
func orderedTiers(tiers map[string]float64) []tierRatio {
	if len(tiers) == 0 {
		return nil
	}
	out := make([]tierRatio, 0, len(tiers))
	used := make(map[string]bool, len(tiers))
	for _, name := range awardOrder {
		if r, ok := tiers[name]; ok {
			out = append(out, tierRatio{Name: name, Ratio: numOr0(r)})
			used[name] = true
		}
	}
	rest := make([]string, 0, len(tiers))
	for name := range tiers {
		if !used[name] {
			rest = append(rest, name)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		if tiers[rest[i]] != tiers[rest[j]] {
			return tiers[rest[i]] > tiers[rest[j]]
		}
		return rest[i] < rest[j]
	})
	for _, name := range rest {
		out = append(out, tierRatio{Name: name, Ratio: numOr0(tiers[name])})
	}
	return out
}

// assignAwards 从第 1 名起依次分配奖项。
//
// 每档名额为 ceil(榜单总数 × 占比)，从高到低依次填满。
// 注意 ceil 的放大效应：队伍数很少时（如某组只有 2 队）每档至少 1 个，
// 会出现「全员获奖」。这是赛制决定的比例问题（可通过缩小占比调节），
// 引擎只负责忠实执行。
//
// onlyComplete 为 true 时，未完成录入的队伍会**占用名次但不占名额**：
// 名额总数仍按榜单总数算，跳过不合格者继续往下发。
func assignAwards(rows []model.StandingRow, tiers map[string]float64, onlyComplete bool) {
	n := len(rows)
	if n == 0 {
		return
	}
	idx := 0
	for _, t := range orderedTiers(tiers) {
		if t.Ratio <= 0 {
			continue
		}
		cnt := int(math.Ceil(float64(n) * t.Ratio))
		given := 0
		for idx < n && given < cnt {
			if onlyComplete && !rows[idx].Result.Complete {
				idx++ // 名次保留，但不出现在获奖名单里
				continue
			}
			rows[idx].Award = t.Name
			idx++
			given++
		}
	}
}
