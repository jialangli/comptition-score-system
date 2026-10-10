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

	// VoidedTeams 被裁定「取消资格」的队伍（成绩作废，2026/10/10 接线）。
	//
	// 与 StandingRow.Disqualified（红牌）是两回事，所以刻意不共用同一个开关：
	//
	//	红牌     成绩**保留** —— 留在榜内、名次置 0，分数照给（留痕可查）
	//	裁定作废  成绩**作废** —— 整行不进榜单，连 Result 都不再给出
	//
	// 判据由调用方从争议工单派生（唯一事实源 = 工单），不在这里落库复制一份：
	// 改判（disqualify → uphold）时榜单自动恢复，无需任何回滚维护。
	VoidedTeams map[int64]bool

	// KeepGap 名次是否保留「作废队」留下的空缺（= 赛事级递补规则里的「不递补」）。
	//
	//	true （不递补，产品默认）作废队的位置**留空**：第 5 名被取消资格，
	//	     第 6 名**仍是第 6 名** —— 公示表上就是 4 → 6 这种空洞。
	//	     这样一次裁定不会让后面所有人的名次都变，且"第 5 名被取消资格"对外可见。
	//	false（按名次顺延）编号连续，第 6 名前移成第 5 名。
	//
	// ⚠️ 只在 VoidedTeams 里有队伍时才看得出区别：**红牌取消比赛资格不产生空洞**
	// （它们留在榜内、名次 0、排在最后），弃赛队也不产生（压根不进原榜）。
	// 而且空缺是按**组别**算的（RankAllGroups 逐组调用本函数），
	// 一个组别的空缺不会挪到另一个组别去。
	//
	// 值由 model.SubstituteMode.KeepGap() 给出 —— 不要在各调用点自己判断字符串。
	KeepGap bool

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
		// ⚠️ 裁定取消资格的队伍**先入列、最后再整行剔除**（见函数末尾）。
		// 它不参与名次与奖项，但当 KeepGap（不递补）时它必须**占住**原来的名次位置 ——
		// 一上来就 continue，就算不出「第 5 名空缺、第 6 名还是第 6 名」这种结果，
		// 而前端公示表正是那么显示的（它用含作废队的 origRank 发名次）。
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
			// 红牌 = 当场取消比赛资格：成绩保留（Result 照常算出），
			// 但不排名次、不参评奖项。
			Disqualified: DisqualifiedByCards(recs, ev.PenaltyRule.CardRules),
		}
		if has && best.Record != nil {
			row.Duration = finiteOr0(best.Record.DurationSec)
			row.BestRound = best.RoundNo
		}
		rows = append(rows, row)
	}

	sortRows(rows, ev.RankRule.TieBreak, opts.NameLess)

	// 名次怎么发：两档，由 opts.KeepGap 选（= 赛事级「递补规则」，见 model.SubstituteMode）。
	//
	//	pos  在**当前这堆行**里的位置（含作废行与取消资格行）—— 等价于前端的 origRank
	//	rank 只在「有资格的队伍」之间连续递增
	//
	//   - KeepGap（不递补，默认）：Rank = pos → 作废队留下的空洞被保留下来
	//   - 否则（按名次顺延）：Rank = rank → 编号连续，后面队伍前移
	//
	// 被取消比赛资格（红牌）的队伍**不产生空洞**：它们排在最后、名次恒为 0，
	// 因此前面的队伍不受影响 —— 这一条两端一致，别把它和"作废留空"混在一起。
	rank, pos := 0, 0
	var prevLiveTotal *float64
	for i := range rows {
		pos++
		if opts.VoidedTeams[rows[i].Team.ID] {
			continue // 作废行：只占位（pos 已前进），不发名次、不参评奖项
		}
		if rows[i].Disqualified {
			rows[i].Rank = 0
			rows[i].Award = ""
			rows[i].Tie = false
			continue
		}
		rank++
		if opts.KeepGap {
			rows[i].Rank = pos
		} else {
			rows[i].Rank = rank
		}
		// 并列标记 = 与**前一支有资格的队伍**同分（前端是在 live 数组里跟上一项比）。
		// 不能直接拿 rows[i-1]：它可能是一支作废队 —— 那样会把"并列"错判成不并列。
		rows[i].Tie = prevLiveTotal != nil && *prevLiveTotal == rows[i].Result.Total
		total := rows[i].Result.Total
		prevLiveTotal = &total
	}

	// 作废行整行剔除：成绩作废 = 不进榜单，连 Result 都不再给出。
	//
	// 位置**必须**在 assignAwards 之前 —— 2026-10-10 定案：**作废队不占奖项名额**，
	// 让出的名额由后面的队伍顶上。于是它既不参与名额的分母（名额按**在榜**队数算），
	// 也不当获奖人：10 队里作废 1 队、三等奖 30%，名额由 3 个变 2 个 —— 这是剔除的正常结果。
	//
	// 与默认的「不递补」（KeepGap）不矛盾，两条规则各管一件事：
	//
	//	名次留空  名次是**身份标识**。「第 5 名被取消资格」这句话要能对上号，
	//	         所以空缺必须留着、后面的队伍不许改号 —— 空缺**不允许被填补**。
	//	奖项剔除  奖项是**名额分配**（按在榜队数 × 占比，见 assignAwards）。
	//	         作废队既然不在榜上，就不占名额 —— 名额**不允许被占用**。
	//
	// 一句话：名次上的空洞保留，奖项上的名额不留。两者都让「被取消资格」看得见，
	// 只是一个靠空缺、一个靠名额顺延。
	if len(opts.VoidedTeams) > 0 {
		kept := rows[:0]
		for i := range rows {
			if opts.VoidedTeams[rows[i].Team.ID] {
				continue
			}
			kept = append(kept, rows[i])
		}
		rows = kept
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
		// 被取消比赛资格的排在最后：他们不参与名次，挨在一起也便于页面
		// 用一段「已取消资格」的说明统一交代，而不是散在榜单中间。
		if a.Disqualified != b.Disqualified {
			return !a.Disqualified
		}
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
// 每档名额 = floor(榜单总数 × 占比)，从高到低依次填满。
//
// 为什么用 floor 而不是 ceil（2026-10-04 定调）：
//
//	ceil 的放大效应在小组赛很明显 —— 2 队时「一等奖 30%」经 ceil 变成 1+1，
//	结果两队全部获奖，「未进入名次」这件事在观感上就消失了。
//	floor 严格不超过比例，2 队 × 30% = 0.6 → 0 个名额，符合「比例就是上限」的直觉。
//
// floor 的副作用必须显式处理：人数少时低占比档位会算出 0 个名额。
// 这里对每个档位**至少保底 1 个**（比例 > 0 就该有人拿），
// 否则会出现「一等奖 3 人、二等奖 0 人、三等奖 0 人」这种更荒唐的分布。
// 换句话说 floor 管住了「不超编」，保底管住了「不空档」。
//
// onlyComplete 为 true 时，未完成录入的队伍会**占用名次但不占名额**：
// 名额总数仍按榜单总数算，跳过不合格者继续往下发。
//
// 归结成一句话：**只有「在榜且有资格」的队伍占名额**。三类都不占名额、名额一律顺延 ——
//
//	作废队    调用前已整行剔除，连分母都不参与（see Rank 里剔除的位置）
//	红牌队    在榜内、名次 0、排在最后，但不当获奖人
//	未完成队  在榜内、有名次（占位），但不出现在获奖名单里
//
// ⚠️ 前两者的差别要注意：作废队**不参与分母**，未完成队**参与分母**（名额总数照旧）。
// 这是刻意的 —— 作废 = 这支队伍不存在了；未完成 = 它还在，只是还没录完。
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
		cnt := int(math.Floor(float64(n) * t.Ratio))
		if cnt < 1 {
			cnt = 1 // 保底：比例大于 0 的档位至少产出 1 个名额
		}
		given := 0
		for idx < n && given < cnt {
			if rows[idx].Disqualified {
				idx++ // 已取消比赛资格：名次与奖项都不参与，名额顺延给下一名
				continue
			}
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
