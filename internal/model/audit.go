package model

import "time"

// ============================================================================
// 审计
//
// 六类操作全留痕：改配置 / 改分 / 弃赛 / 改组 / 调赛台 / 删队，另加导入队伍。
// 每条含：操作人 / 时间 / 对象 / 旧值 → 新值 / 原因，保留 ≥2 年。
// 这是争议追溯的依据 —— 由 service 层在**同一事务内**写入，杜绝「改了但没留痕」。
// ============================================================================

// AuditAction 审计动作。
type AuditAction string

const (
	ActConfig   AuditAction = "改配置"
	ActScore    AuditAction = "改分"
	ActWithdraw AuditAction = "弃赛"
	ActRegroup  AuditAction = "改组"
	ActSeat     AuditAction = "调赛台"
	ActDelete   AuditAction = "删队"
	ActImport   AuditAction = "导入队伍"
	ActLock     AuditAction = "锁定成绩" // 成绩/配置锁定与解锁
)

// AllAuditActions 必须留痕的六类操作。
func AllAuditActions() []AuditAction {
	return []AuditAction{ActConfig, ActScore, ActWithdraw, ActRegroup, ActSeat, ActDelete}
}

// AuditLog 一条审计记录。
type AuditLog struct {
	ID       int64  `json:"id"`
	Operator string `json:"operator"` // 实际操作人
	// Approver 审批人（裁判长）。仅「需授权才能执行」的操作填写：
	// 依据需求确认单，改分必须记录操作人与审批人两个名字。
	Approver  string      `json:"approver,omitempty"`
	Action    AuditAction `json:"action"`
	Target    string      `json:"target"`
	Before    string      `json:"before"`
	After     string      `json:"after"`
	Reason    string      `json:"reason"`
	CreatedAt time.Time   `json:"createdAt"`
}

// AuditEntry 写入审计时的入参（不含 ID 与时间）。
type AuditEntry struct {
	Operator string
	Approver string
	Action   AuditAction
	Target   string
	Before   string
	After    string
	Reason   string
}

// ImportSummary 导入四态统计，与前端变更比对结果一致。
type ImportSummary struct {
	Insert   int `json:"insert"`
	Update   int `json:"update"`
	Skip     int `json:"skip"`
	Conflict int `json:"conflict"`
}

// ImportLog 报名导入审计。
type ImportLog struct {
	ID        int64         `json:"id"`
	Source    string        `json:"source"` // excel | api | manual
	Operator  string        `json:"operator"`
	Summary   ImportSummary `json:"summary"`
	Detail    []any         `json:"detail"` // 逐条变更明细（含旧值 / 新值）
	Status    string        `json:"status"` // success | rolled_back
	CreatedAt time.Time     `json:"createdAt"`
}
