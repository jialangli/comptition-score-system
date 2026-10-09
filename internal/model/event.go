package model

import (
	"strings"
	"time"
)

// ============================================================================
// 赛项与评分规则
// 字段名与 JSON 标签刻意与前端《配置 Schema v1》逐字一致 —— 前后端、导出三处
// 共用同一套命名，省掉一层映射代码。
// ============================================================================

// TaskType 评分方式（任务原语）。只有三类，任何赛项的评分项都能用它描述。
type TaskType string

const (
	TaskNumeric TaskType = "numeric" // 数值评分：裁判给 0–满分 的连续分
	TaskCount   TaskType = "count"   // 计数得分：数量 × 每单位分
	TaskToggle  TaskType = "toggle"  // 是否完成：完成记满分、未完成记 0
	TaskEnum    TaskType = "enum"    // 等级评分：按等级映射取值（已下线，仅保留兼容历史数据）
)

// Valid 校验评分方式是否合法。
//
// 可选值为 numeric / count / toggle 三类（2026/10/08 口径：评分方式只有三类）；
// enum 保留在白名单里只为让**历史数据**仍能通过校验，前端已不可选。
func (t TaskType) Valid() bool {
	switch t {
	case TaskNumeric, TaskCount, TaskToggle, TaskEnum:
		return true
	}
	return false
}

// Display 返回界面与审计用的中文名称。
//
// 内部键（numeric）保持不变以兼容配置 Schema 与导出 JSON，
// 中文只出现在展示与留痕文案里 —— 这条边界不要打破。
func (t TaskType) Display() string {
	switch t {
	case TaskNumeric:
		return "数值评分"
	case TaskCount:
		return "计数得分"
	case TaskToggle:
		return "是否完成"
	case TaskEnum:
		return "等级评分"
	}
	return string(t)
}

// Control 裁判端录入控件类型。
type Control string

const (
	CtrlSlider  Control = "slider"
	CtrlNumber  Control = "number"
	CtrlCounter Control = "counter"
	CtrlSelect  Control = "select"
	CtrlToggle  Control = "toggle" // 开关（是/否），配合 TaskToggle 使用
)

// Task 任务项，是计分的最小单元。
type Task struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Type      TaskType           `json:"type"`
	MaxScore  *float64           `json:"maxScore,omitempty"` // numeric 满分；count 可空表示无上限
	Weight    float64            `json:"weight"`             // numeric 为归一化权重；count 为每单位分
	Control   Control            `json:"control,omitempty"`
	EnumMap   map[string]float64 `json:"enumMap,omitempty"` // 仅 enum：等级 → 分值
	SortOrder int                `json:"sortOrder"`
}

// ScoreTemplate 计分规则。
type ScoreTemplate string

// 本赛制只用 TplSum（直接求和）。另两种**引擎继续支持**（历史赛事要逐位复现，
// 与 per_card 的处理一致），但界面已不再提供 —— 现场人员误选它的可能性
// 远大于真的需要它，而一旦误选，分制会变且不报错。
const (
	TplWeightedSum ScoreTemplate = "weighted_sum" // Σ(归一化分 × 权重)；界面已下线，仅历史数据
	TplSum         ScoreTemplate = "sum"          // Σ(原始分) —— 本赛制唯一在用的模板
	TplAverage     ScoreTemplate = "average"      // 取平均；界面已下线，仅历史数据
)

// Valid 校验计分规则是否合法。
func (t ScoreTemplate) Valid() bool {
	switch t {
	case TplWeightedSum, TplSum, TplAverage:
		return true
	}
	return false
}

// Display 返回中文名称（界面与留痕文案用）。
func (t ScoreTemplate) Display() string {
	switch t {
	case TplWeightedSum:
		return "加权求和"
	case TplSum:
		return "直接求和"
	case TplAverage:
		return "取平均值"
	}
	return string(t)
}

// ScoreRule 计分规则。
type ScoreRule struct {
	Template ScoreTemplate  `json:"template"`
	Params   map[string]any `json:"params,omitempty"`
}

// BonusTemplate 奖励模板。
type BonusTemplate string

const (
	BonusTime  BonusTemplate = "time_bonus"  // 时间奖励：越快加分
	BonusCount BonusTemplate = "count_bonus" // 计数奖励：某计数项按数量加分
	BonusNone  BonusTemplate = "none"        // 无奖励
)

// Valid 校验奖励模板是否合法。
func (t BonusTemplate) Valid() bool {
	switch t {
	case BonusTime, BonusCount, BonusNone:
		return true
	}
	return false
}

// Display 返回中文名称（界面与留痕文案用）。
func (t BonusTemplate) Display() string {
	switch t {
	case BonusTime:
		return "时间奖励"
	case BonusCount:
		return "计数奖励"
	case BonusNone:
		return "无奖励"
	}
	return string(t)
}

// BonusRule 奖励规则。
//
// Params 约定：
//
//	time_bonus  : {"perSecond": 0.5, "cap": 10}
//	count_bonus : {"taskId": "energy", "perUnit": 5, "cap": null}
type BonusRule struct {
	Template BonusTemplate  `json:"template"`
	Params   map[string]any `json:"params,omitempty"`
}

// PenaltyTemplate 扣分模板。
//
// 口径（2026/10/09 产品定调）：判罚**只有「仅记录不扣分」一档** —— 黄 / 红牌
// 只作记录、留痕与公示，不进总分。红牌的直接后果「取消比赛资格」是**赛制固定的
// 处置**（保留成绩、只取消名次与奖项），不在这里配。
//
// 两个历史值保留但前端不再提供，仅用于读旧数据：
//   - per_card 按牌扣分（已下线）：老赛事的黄/红牌扣分值可能非 0，算分分支继续
//     保留，保证历史赛事复现出的分数与当初一致；
//   - none 不扣分：与 record_only 效果相同，早期前端写入的值。
type PenaltyTemplate string

const (
	// PenaltyRecordOnly 仅记录不扣分 —— 当前唯一在用的判罚口径。
	PenaltyRecordOnly PenaltyTemplate = "record_only"

	PenaltyNone    PenaltyTemplate = "none"     // 历史值：不扣分（等价于 record_only）
	PenaltyPerCard PenaltyTemplate = "per_card" // 历史值：按牌扣分（已下线，仅兼容旧数据）
)

// Valid 校验扣分模板是否合法。空值视为「未配置」，按不扣分处理。
//
// 历史值仍算合法 —— 老赛事必须能原样读回、原样复现；若判为非法，
// 历史赛项一保存就会被拦下，那是数据事故而不是校验。
func (t PenaltyTemplate) Valid() bool {
	switch t {
	case "", PenaltyRecordOnly, PenaltyNone, PenaltyPerCard:
		return true
	}
	return false
}

// Deprecated 该模板是否已下线（前端不应再提供，仅用于读旧数据）。
func (t PenaltyTemplate) Deprecated() bool {
	return t == PenaltyPerCard
}

// Display 返回中文名称（界面与留痕文案用）。
func (t PenaltyTemplate) Display() string {
	switch t {
	case PenaltyRecordOnly:
		return "仅记录不扣分"
	case PenaltyPerCard:
		return "按牌扣分"
	case PenaltyNone, "":
		return "不扣分"
	}
	return string(t)
}

// CardReason 一条可判罚事由。
//
// 黄牌自 2026/10 起改为「计分板常驻计数器」，点一下即记一张、不选事由，
// 所以实际只有红牌用事由；Card 字段保留是为了兼容早期同时配黄 / 红事由的数据。
type CardReason struct {
	Code  string `json:"code"`            // 事由编号，如 R01
	Card  string `json:"card"`            // yellow / red
	Label string `json:"label,omitempty"` // 事由名称
}

// DefaultRedThreshold 累计升级阈值默认值：3 张黄牌 → 1 张红牌。
const DefaultRedThreshold = 3

// CardRules 牌面计数规则，对应后台「赛项与规则配置 → 判罚规则」。
//
// 与 PenaltyRule 的分工：
//   - PenaltyRule 管「扣不扣分」（现在恒为「仅记录不扣分」）；
//   - CardRules 管「黄牌计数器开不开、几张升级红牌、红牌有哪些事由」。
//
// 红牌的直接后果（当场取消比赛资格）由赛制固定、不在这份配置里 —— 可配的是
// 「怎么记牌」，不配「记了会怎样」，避免现场把后果也改掉。
//
// Enabled 用指针是为了区分「未配置」与「显式关闭」：历史数据没有这个字段，
// 读出来是 nil，必须按「开」处理（与改制前行为一致），
// 不能当成 false 静默停用黄牌计数器。
//
// 注：2026/10/09 去掉了一个从来没起过作用的开关 —— 红牌本就有两条来路
// （裁判直接记 + 黄牌累计升级），事由表也只服务于前者，所以不必再配「要不要允许」。
type CardRules struct {
	// Enabled 黄牌计数器总开关。关闭 = **整个黄牌体系停用**：
	// 裁判端不显示计数器、黄牌不累计、也不再有「累计 N 张自动升级红牌」；
	// 红牌仍可直接记录（红牌是独立入口，不受黄牌开关影响）。
	Enabled *bool `json:"enabled,omitempty"`

	// RedThreshold 累计多少张黄牌自动升级为 1 张红牌。仅 Enabled 为真时生效。
	RedThreshold int `json:"redThreshold,omitempty"`

	// Reasons 可判罚事由（裁判端只读选择）。
	Reasons []CardReason `json:"reasons,omitempty"`
}

// EnabledOrDefault 黄牌计数器是否启用。未配置时默认启用。
func (c *CardRules) EnabledOrDefault() bool {
	if c == nil || c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// ThresholdOrDefault 累计升级阈值。未配置或非正数时取默认值。
func (c *CardRules) ThresholdOrDefault() int {
	if c == nil || c.RedThreshold <= 0 {
		return DefaultRedThreshold
	}
	return c.RedThreshold
}

// ReasonLabels 返回指定牌面的事由名称（保持配置顺序、跳过空条目）。
func (c *CardRules) ReasonLabels(card string) []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Reasons))
	for _, r := range c.Reasons {
		if r.Card != card {
			continue
		}
		if label := strings.TrimSpace(r.Label); label != "" {
			out = append(out, label)
		}
	}
	return out
}

// PenaltyRule 判罚规则。
//
//	record_only : 仅记录不扣分（当前口径，params 留空）
//	per_card    : {"yellow": 5, "red": 15}（已下线，仅读旧数据）
//
// ⚠️ CardRules 必须是**具名字段**，不能像早期前端那样塞进 Params["cardRules"]：
// 塞进 Params 后 Go 侧没有类型可读；而作为本结构体的未知顶层字段又会被
// encoding/json 直接丢弃 —— 存回库里就永久少一块配置，且全程零报错。
type PenaltyRule struct {
	Template  PenaltyTemplate `json:"template"`
	Params    map[string]any  `json:"params,omitempty"`
	CardRules *CardRules      `json:"cardRules,omitempty"`
}

// RankRule 排名与奖项规则。
type RankRule struct {
	TieBreak   []string           `json:"tieBreak"`   // 并列裁决优先级：score / time
	AwardTiers map[string]float64 `json:"awardTiers"` // 奖项占比：{"一等奖":0.1,...}
}

// Event 赛项。
type Event struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Groups        []string    `json:"groups"`
	Tasks         []Task      `json:"tasks"`
	ScoreRule     ScoreRule   `json:"scoreRule"`
	BonusRules    []BonusRule `json:"bonusRules"`
	PenaltyRule   PenaltyRule `json:"penaltyRule"`
	RankRule      RankRule    `json:"rankRule"`
	CustomFormula *string     `json:"customFormula"` // v1 固定为 nil
	ConfigVersion string      `json:"configVersion"`
	CreatedAt     time.Time   `json:"createdAt"`
	UpdatedAt     time.Time   `json:"updatedAt"`
}

// TaskByID 按 ID 查找任务项，未找到返回 nil。
func (e *Event) TaskByID(id string) *Task {
	for i := range e.Tasks {
		if e.Tasks[i].ID == id {
			return &e.Tasks[i]
		}
	}
	return nil
}

// GroupAllowed 判断组别是否属于本赛项。
func (e *Event) GroupAllowed(group string) bool {
	for _, g := range e.Groups {
		if g == group {
			return true
		}
	}
	return false
}

// ConfigSnapshot 配置快照：改配置前留档，可回滚。
type ConfigSnapshot struct {
	ID        int64     `json:"id"`
	Note      string    `json:"note"`
	Payload   any       `json:"payload"` // {schemaVersion, events:[...]}
	CreatedAt time.Time `json:"createdAt"`
}
