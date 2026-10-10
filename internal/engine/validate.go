package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 赛项配置校验
//
// 分两级结论，语义明确、不混用：
//
//	LevelError   阻断 —— 配置不合法，保存接口必须返回 400，不允许进入打分环节。
//	             典型：任务 id 重复、等级任务缺映射表、引用了不存在的计分规则。
//	LevelWarning 提醒 —— 可以保存，但很可能不是运营的本意，界面上需要黄条提示。
//	             典型：权重之和不等于 1、奖项占比之和超过 1。
//
// 与前端 validateEvent 的差异（三处，均为修正而非放宽）：
//
//  1. 无效计分规则：前端仅在校验里不检查，而 computeTotal 会静默落入
//     average 分支算出错误成绩 → 这里作为 error 阻断。
//  2. count_bonus 引用不存在任务：前端写了一个恒真的判断（some 遍历奖励规则
//     时必然包含自身），该 error 永远不会触发 → 这里改为 warning，
//     如实说明「该计数项作为派生项参与加分、不计入基础分」。
//  3. 补齐了前端未覆盖的检查项：赛项名 / 组别 / 任务类型 / 奖励模板 /
//     扣分模板 / 同分裁决项合法性。
// ============================================================================

// IssueLevel 校验结论的严重程度。
type IssueLevel string

const (
	LevelError   IssueLevel = "error"   // 阻断
	LevelWarning IssueLevel = "warning" // 提醒
)

// Issue 一条校验结论。
type Issue struct {
	Level IssueLevel `json:"level"`
	Field string     `json:"field,omitempty"` // 定位字段（JSON 路径），便于界面高亮
	Msg   string     `json:"message"`
}

// ValidationResult 配置校验结果。
type ValidationResult struct {
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
}

// OK 是否无阻断性问题（可保存）。
func (r ValidationResult) OK() bool { return len(r.Errors) == 0 }

// ErrorMessages 取出全部阻断项文本，便于日志与测试断言。
func (r ValidationResult) ErrorMessages() []string {
	out := make([]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		out = append(out, e.Msg)
	}
	return out
}

// WarningMessages 取出全部提醒项文本。
func (r ValidationResult) WarningMessages() []string {
	out := make([]string, 0, len(r.Warnings))
	for _, w := range r.Warnings {
		out = append(out, w.Msg)
	}
	return out
}

// ValidateEvent 校验赛项配置。纯函数，不修改入参。
func ValidateEvent(ev *model.Event) ValidationResult {
	var res ValidationResult
	fail := func(field, format string, args ...any) {
		res.Errors = append(res.Errors, Issue{Level: LevelError, Field: field, Msg: fmt.Sprintf(format, args...)})
	}
	warn := func(field, format string, args ...any) {
		res.Warnings = append(res.Warnings, Issue{Level: LevelWarning, Field: field, Msg: fmt.Sprintf(format, args...)})
	}

	if ev == nil {
		fail("event", "赛项不存在")
		return res
	}

	// —— 基本信息 ——
	// 赛项 ID 由系统分配，界面上只读，这里仅做兜底检查。
	if strings.TrimSpace(ev.ID) == "" {
		fail("id", "赛项 ID 不能为空（应由系统分配）")
	}
	if strings.TrimSpace(ev.Name) == "" {
		fail("name", "赛项名称不能为空")
	}
	if len(ev.Groups) == 0 {
		fail("groups", "至少需要配置一个组别")
	}
	seenGroup := make(map[string]bool, len(ev.Groups))
	for i, g := range ev.Groups {
		field := fmt.Sprintf("groups[%d]", i)
		if strings.TrimSpace(g) == "" {
			fail(field, "存在空白组别")
			continue
		}
		if seenGroup[g] {
			warn(field, "组别「%s」重复配置", g)
		}
		seenGroup[g] = true
	}

	// —— 计分规则 ——
	if !ev.ScoreRule.Template.Valid() {
		fail("scoreRule.template", "计分规则「%s」不是有效值（本赛制只用「直接求和」）", string(ev.ScoreRule.Template))
	}

	// —— 阶段 ——
	//
	// 阶段只影响一处：时间奖励的**基准时长**（见 RefTimeFor）。
	// 所以这里不阻断、只提醒 —— 配错不会让赛项"不可用"，
	// 而是让时间奖励静默按另一个基准算（前端 225s / 后端 120s 就是这么来的），
	// 属于必须让人看见的那类问题。
	if len(ev.Phases) > 0 {
		for i := range ev.Phases {
			p := &ev.Phases[i]
			field := fmt.Sprintf("phases[%d]", i)
			if strings.TrimSpace(p.ID) == "" {
				warn(field+".id", "第 %d 个阶段的 id 为空，平板端计时与赛程对不上", i+1)
			}
			if p.DurationSec <= 0 {
				warn(field+".durationSec", "阶段「%s」时长不是正数，不会计入时间奖励的基准时长", p.Name)
			}
		}
		if ev.PhaseTotalSec() <= 0 {
			warn("phases", "各阶段时长之和不是正数，时间奖励的基准时长将退回 scoreRule.params.refTime 或默认值")
		}
	}

	// —— 任务项 ——
	if len(ev.Tasks) == 0 {
		fail("tasks", "至少需要配置一个任务项，否则成绩恒为 0")
	}
	seenTask := make(map[string]bool, len(ev.Tasks))
	weightSum := 0.0
	for i := range ev.Tasks {
		t := &ev.Tasks[i]
		field := fmt.Sprintf("tasks[%d]", i)

		if strings.TrimSpace(t.ID) == "" {
			fail(field+".id", "第 %d 个任务的 id 不能为空", i+1)
		} else if seenTask[t.ID] {
			fail(field+".id", "任务 id 重复：%s", t.ID)
		} else {
			seenTask[t.ID] = true
		}
		if strings.TrimSpace(t.Name) == "" {
			fail(field+".name", "任务「%s」未命名", t.ID)
		}
		if !t.Type.Valid() {
			fail(field+".type", "任务「%s」的评分方式「%s」不是有效值（可选：数值评分 / 计数得分 / 是否完成）", t.Name, string(t.Type))
			continue
		}

		switch t.Type {
		case model.TaskNumeric:
			weightSum += numOr0(t.Weight)
			switch {
			case t.MaxScore == nil:
				warn(field+".maxScore", "数值任务「%s」未设满分，将按原始分直接计入（不做归一化），与其他任务不同量级", t.Name)
			case numOr0(*t.MaxScore) <= 0:
				warn(field+".maxScore", "数值任务「%s」的满分应大于 0", t.Name)
			}
		case model.TaskToggle:
			if t.MaxScore == nil || numOr0(*t.MaxScore) <= 0 {
				warn(field+".maxScore", "是否完成任务「%s」未设满分（或满分为 0），完成也不会得分", t.Name)
			}
		case model.TaskEnum:
			if len(t.EnumMap) == 0 {
				fail(field+".enumMap", "等级任务「%s」缺少等级映射表（等级 → 分值）", t.Name)
			}
		case model.TaskCount:
			if numOr0(t.Weight) == 0 {
				warn(field+".weight", "计数任务「%s」每单位分为 0，将不产生任何得分", t.Name)
			}
		}

		// —— 量词（题卡上的「每颗 +100」）——
		//
		// 它不参与算分，所以除了「写给谁看」之外只有两条底线：
		//   ① 只对计数得分有意义 —— 数值/是否完成没有「每单位」的概念，填了会被界面忽略，
		//      与其让人以为配了没生效，不如当场说清楚（警告而非拦下：存量配置不该因它变红）。
		//   ② 过长会撑坏题卡版式 —— 量词是「个/颗/块」这一档的字，写成一整句说明就变味了。
		if strings.TrimSpace(t.Unit) != t.Unit {
			fail(field+".unit", "量词「%s」首尾有空白（会被当成另一个值）", t.Unit)
		}
		if n := utf8.RuneCountInString(t.Unit); n > 4 {
			fail(field+".unit", "量词「%s」过长（%d 个字，最多 4 个）—— 它是「个 / 颗 / 块」这一档的计量字，不是说明文字", t.Unit, n)
		}
		if t.Type != model.TaskCount && t.Unit != "" {
			warn(field+".unit", "任务「%s」是%s，量词「%s」不会出现在题卡上（量词只对「计数得分」有意义）",
				t.Name, t.Type.Display(), t.Unit)
		}
	}
	// 界面已不再提供「加权求和」；这条校验只为历史赛项的配置健康度保留。
	if ev.ScoreRule.Template == model.TplWeightedSum && math.Abs(weightSum-1) > 0.001 {
		warn("tasks", "数值任务权重之和 = %.2f，加权求和模板下建议等于 1.0", weightSum)
	}

	// —— 奖励规则 ——
	for i := range ev.BonusRules {
		b := &ev.BonusRules[i]
		field := fmt.Sprintf("bonusRules[%d]", i)

		if !b.Template.Valid() {
			fail(field+".template", "奖励规则 #%d 的模板「%s」不是有效值（可选：时间奖励 / 计数奖励 / 无奖励）", i+1, string(b.Template))
			continue
		}
		switch b.Template {
		case model.BonusTime:
			if numOr0(b.Params["perSecond"]) == 0 {
				warn(field+".perSecond", "时间奖励未配置每秒加分，将不产生加分")
			}
			if capped, ok := b.Params["cap"]; ok && capped != nil && numOr0(capped) < 0 {
				warn(field+".cap", "时间奖励的封顶值为负数，加分会被压到 0 以下")
			}
		case model.BonusCount:
			taskID, _ := b.Params["taskId"].(string)
			if taskID == "" {
				fail(field+".taskId", "计数奖励 #%d 未指定计数项", i+1)
				continue
			}
			if !seenTask[taskID] {
				warn(field+".taskId", "计数项「%s」未在任务列表中声明，将作为派生计数项参与加分（不计入基础分）", taskID)
			}
			if numOr0(b.Params["perUnit"]) == 0 {
				warn(field+".perUnit", "计数奖励「%s」每单位分为 0，将不产生加分", taskID)
			}
		}
	}

	// —— 判罚规则 ——
	//
	// 模板现在只有「仅记录不扣分」一档（2026/10/09 口径：黄 / 红牌只作记录、
	// 留痕与公示，不进总分）。历史值 per_card / none 仍是合法值 —— 老赛事要能
	// 原样保存、原样复现，判为非法会让历史赛项一提交就被拦下。
	// 这里刻意**不给历史值发警告**：前端原型（demo validateEvent）不校验判罚段，
	// 单方面加警告会让两端校验结果的数量对不上（golden 基准会立刻挂）。
	if !ev.PenaltyRule.Template.Valid() {
		fail("penaltyRule.template", "判罚模板「%s」不是有效值（可选：仅记录不扣分）", string(ev.PenaltyRule.Template))
	}
	if ev.PenaltyRule.Template == model.PenaltyPerCard {
		y, r := numOr0(ev.PenaltyRule.Params["yellow"]), numOr0(ev.PenaltyRule.Params["red"])
		if y == 0 && r == 0 {
			warn("penaltyRule.params", "已启用按牌扣分，但黄牌与红牌的扣分值均为 0")
		}
	}

	// —— 牌面计数规则（黄牌计数器开关 / 累计升级阈值）——
	//
	// 关闭开关 = 整个黄牌体系停用（不显示、不累计、也不升级红牌）；红牌是独立入口，
	// 仍可直接记录，所以「关了黄牌计数器」本身**不构成矛盾、不产生警告**。
	//（原来还有一条「关了计数器 + 禁止直接记红牌」的警告，随该开关一并删除。）
	if cr := ev.PenaltyRule.CardRules; cr != nil {
		if cr.RedThreshold < 0 {
			fail("penaltyRule.cardRules.redThreshold", "黄牌累计升级阈值不能为负数（收到 %d）", cr.RedThreshold)
		}
	}

	// —— 排名与奖项 ——
	if len(ev.RankRule.TieBreak) == 0 {
		warn("rankRule.tieBreak", "未配置同分裁决项，将仅按总分排名")
	}
	for i, tb := range ev.RankRule.TieBreak {
		if tb != "score" && tb != "time" {
			fail(fmt.Sprintf("rankRule.tieBreak[%d]", i), "同分裁决项「%s」不是有效值（可选：得分 / 用时）", tb)
		}
	}
	tierNames := sortedTierNames(ev.RankRule.AwardTiers)
	tierSum := 0.0
	for _, name := range tierNames {
		v := numOr0(ev.RankRule.AwardTiers[name])
		if v < 0 {
			fail("rankRule.awardTiers."+name, "奖项「%s」的占比不能为负数", name)
		}
		tierSum += v
	}
	switch {
	case len(tierNames) == 0:
		warn("rankRule.awardTiers", "未配置任何奖项，榜单将不分配奖项")
	case tierSum == 0:
		warn("rankRule.awardTiers", "奖项占比全为 0，榜单将不分配奖项")
	case tierSum > 1.0001:
		warn("rankRule.awardTiers", "奖项占比之和 = %.2f，建议不超过 1.0，否则靠后的队伍拿不到奖项", tierSum)
	}

	// —— 本期不支持的项 ——
	if ev.CustomFormula != nil {
		fail("customFormula", "本期不支持自定义计分公式，customFormula 必须为空")
	}

	return res
}

// sortedTierNames 返回排序后的奖项名，用于让校验结论的顺序稳定
// （map 迭代顺序随机，直接遍历会导致同一配置每次报错顺序不同）。
func sortedTierNames(tiers map[string]float64) []string {
	out := make([]string, 0, len(tiers))
	for k := range tiers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
