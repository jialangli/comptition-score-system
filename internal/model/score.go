package model

import (
	"fmt"
	"time"
)

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
	RoundNo     int            `json:"roundNo"`     // 1 或 2
	TaskValues  map[string]any `json:"tasks"`       // taskID → 裁判录入的原始值
	DurationSec float64        `json:"time"`        // 用时（秒），时间奖励的输入
	Yellow      int            `json:"yellow"`      // 黄牌计数（当前周期，0..阈值-1）
	UpgradedRed int            `json:"upgradedRed"` // 由黄牌累计升级出的红牌数
	Red         int            `json:"red"`         // 直接记的红牌数
	Signed      bool           `json:"signed"`      // 选手代表已签字确认
	Operator    string         `json:"operator"`    // 记录人（裁判 / 记分员）
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

	// Disqualified 因红牌被取消比赛资格（当场生效）。
	//
	// 语义（2026/10/09 产品定调）：**成绩保留、只取消名次与奖项** ——
	// Result 里分数照常给出（留痕可查），但不参与名次（Rank = 0）
	// 也不参与奖项分配。与「弃赛」（队伍主动退赛）和裁定台的
	// 「取消资格（成绩作废）」是三条不同路径。
	Disqualified bool `json:"disqualified,omitempty"`

	Tie bool `json:"tie,omitempty"` // 与上一名同分
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
//
// 字段与 score_change_requests 表一一对应（见 0003 迁移）。
// (TeamID, RoundNo) 是去重键：同一队同一轮同时只允许存在一条待审批申请。
type ScoreChangeRequest struct {
	ID       int64   `json:"id"` // 申请单号，落库后回填
	ScoreID  int64   `json:"scoreId"`
	TeamID   int64   `json:"teamId"`
	RoundNo  int     `json:"roundNo"`  // 第几轮（1 / 2）
	Before   float64 `json:"before"`   // 修改前总分
	After    float64 `json:"after"`    // 拟修改后总分
	Reason   string  `json:"reason"`   // 必填：申诉 / 复核原因
	Operator string  `json:"operator"` // 申请人
	Approved bool    `json:"approved"` // 是否已获裁判长授权
	Approver string  `json:"approver,omitempty"`
	// AppealEvidenceID 关联的申述书照片证据（evidence.id）。
	// 争议触发的授权改分单由 P8b 生成时从争议单继承同一张，实现「两处都挂」。
	AppealEvidenceID *int64 `json:"appealEvidenceId,omitempty"`
	// BeforeTime / AfterTime 单内的「用时」（秒）—— 对应 wireframe P2b
	// 「用时（可改 · 改分模式专属）」：改分模式下可连同用时一起修正，
	// 但必须走同一张申请单审批，不能绕过。
	//
	// 两列都可为 nil，语义是「本单不涉及用时」（只改分不改时的申请、以及
	// 0022 之前落库的历史单）。**不要用 0 当"没改"**：0 秒是合法用时。
	//
	// 为什么必须进申请单：用时既是时间奖励的计算输入，又是并列裁决的
	// tiebreak（总分 ↓ → 用时 ↑ → 轮次小者）—— 不入单就等于「能改名次却不留痕」。
	BeforeTime *float64   `json:"beforeTime,omitempty"`
	AfterTime  *float64   `json:"afterTime,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	DecidedAt  *time.Time `json:"decidedAt,omitempty"` // 处理时间；待审批为 nil
}

// ChangesTime 本单是否涉及用时修正。
//
// 判据只此一处：service 校验、审计文案、告警详情都读它，
// 避免各自写 `AfterTime != nil` 而在某处漏掉（漏掉的表现是"改了却看不见"）。
func (r ScoreChangeRequest) ChangesTime() bool {
	return r.AfterTime != nil
}

// FormatDuration 秒 → "mm:ss"（用时在界面与留痕里的统一写法）。
//
// 放在 model 而不是各调用点：demo 前端、wireframe 文案、审计留痕三处必须同形，
// 各写一份必然漂移（例如 161 秒在 A 处显示 02:41、B 处显示 2:41）。
// 负数按 0 处理：用时不存在负值，出现负数只可能是上游算错，不该显示成 "-1:-1"。
func FormatDuration(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := int(sec + 0.5) // 四舍五入到秒：0.5 秒的差在成绩上无意义，显示要稳
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}
