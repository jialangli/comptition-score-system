package model

import "time"

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
	TaskEnum    TaskType = "enum"    // 等级评分：按等级映射取值
)

// Valid 校验评分方式是否合法。
func (t TaskType) Valid() bool {
	switch t {
	case TaskNumeric, TaskCount, TaskEnum:
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

// ScoreTemplate 计分模板。
type ScoreTemplate string

const (
	TplWeightedSum ScoreTemplate = "weighted_sum" // Σ(归一化分 × 权重)
	TplSum         ScoreTemplate = "sum"          // Σ(原始分)
	TplAverage     ScoreTemplate = "average"      // 取平均
)

// Valid 校验计分模板是否合法。
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
type PenaltyTemplate string

const (
	PenaltyPerCard PenaltyTemplate = "per_card" // 按牌扣分：黄牌 / 红牌各按张数折算
	PenaltyNone    PenaltyTemplate = "none"     // 不扣分
)

// Valid 校验扣分模板是否合法。空值视为「未配置」，按不扣分处理。
func (t PenaltyTemplate) Valid() bool {
	switch t {
	case "", PenaltyPerCard, PenaltyNone:
		return true
	}
	return false
}

// Display 返回中文名称（界面与留痕文案用）。
func (t PenaltyTemplate) Display() string {
	switch t {
	case PenaltyPerCard:
		return "按牌扣分"
	case PenaltyNone, "":
		return "不扣分"
	}
	return string(t)
}

// PenaltyRule 扣分规则。
//
//	per_card : {"yellow": 5, "red": 15}
type PenaltyRule struct {
	Template PenaltyTemplate `json:"template"`
	Params   map[string]any  `json:"params,omitempty"`
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
