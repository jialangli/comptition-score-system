package model

import "time"

// ============================================================================
// 打分记录与计分结果
//
// 赛制为两轮制：每支队伍在两轮各产生一条记录，最终成绩由 engine 取优后计算。
// ============================================================================

const (
	MinRound = 1 // 第一轮
	MaxRound = 2 // 第二轮
)

// ScoreRecord 单轮打分记录。
type ScoreRecord struct {
	ID          int64          `json:"id"`
	TeamID      int64          `json:"teamId"`
	RoundNo     int            `json:"roundNo"`  // 1 或 2
	TaskValues  map[string]any `json:"tasks"`    // taskID → 裁判录入的原始值
	DurationSec float64        `json:"time"`     // 用时（秒），时间奖励的输入
	Yellow      int            `json:"yellow"`   // 黄牌数
	Red         int            `json:"red"`      // 红牌数
	Signed      bool           `json:"signed"`   // 选手代表已签字确认
	Operator    string         `json:"operator"` // 记录人（裁判 / 记分员）
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

// ScoreResult 计分结果。字段与前端 computeTotal 的返回结构保持一致，
// 便于前端在「后端模式」和「离线模式」之间无感切换。
type ScoreResult struct {
	Base     float64 `json:"base"`     // 基础分：Σ 各任务得分
	Bonus    float64 `json:"bonus"`    // 加分：时间奖励 + 计数奖励
	Penalty  float64 `json:"penalty"`  // 扣分：黄 / 红牌
	Total    float64 `json:"total"`    // 总分 = max(0, base + bonus - penalty)
	Complete bool    `json:"complete"` // 所有任务均已录入
}

// StandingRow 榜单行。
type StandingRow struct {
	Rank      int         `json:"rank"`
	Team      Team        `json:"team"`
	Result    ScoreResult `json:"result"`
	Duration  float64     `json:"time"`            // 取优轮的用时（并列裁决用）
	Rounds    []int       `json:"rounds"`          // 有记录的轮次（升序）
	BestRound int         `json:"bestRound"`       // 取优采用的那一轮；0 表示尚无记录
	Award     string      `json:"award,omitempty"` // 一等奖 / 亚军 …
	Tie       bool        `json:"tie,omitempty"`   // 与上一名同分
}

// GroupStandings 一个组别的榜单。
//
// 依据公示表样例：小学组与中学组各自有独立的冠军 / 亚军 / 季军，
// 即**名次与奖项按组别独立计算**，因此在榜单结构上区分组别，
// 而不是把全赛项队伍混在一张表里排名。
type GroupStandings struct {
	Group string        `json:"group"`
	Rows  []StandingRow `json:"rows"`
}

// ScoreChangeRequest 改分申请。
//
// 依据需求确认单：裁判提交后禁止直接改分，须走审批并留痕；
// 裁判长本人改分同样需要走这条链路。
type ScoreChangeRequest struct {
	ScoreID   int64     `json:"scoreId"`
	TeamID    int64     `json:"teamId"`
	Before    float64   `json:"before"`   // 修改前总分
	After     float64   `json:"after"`    // 拟修改后总分
	Reason    string    `json:"reason"`   // 必填：申诉 / 复核原因
	Operator  string    `json:"operator"` // 申请人
	Approved  bool      `json:"approved"` // 是否已获裁判长授权
	Approver  string    `json:"approver,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}
