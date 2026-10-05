package model

import (
	"fmt"
	"time"
)

// ============================================================================
// 争议工单
//
// 现场两类「两份成绩 / 一个说法」的场景都收在这里：
//
//	重复打分    —— 裁判人工上报（D-00x），前端 P8 队列的主场景
//	同步冲突    —— 离线补传时发现同队同轮已有服务端记录，系统自动建单
//
// 二者共用同一张表与同一套裁定流程，只在 source 列区分 —— 运营要能一眼看出
// 「这是人报的」还是「这是补传撞车自动生成的」。
//
// 与改分申请单（ScoreChangeRequest）的分工：
//
//	改分申请单：裁判说「我要把这个分改成 X」→ 裁判长授权。方向是申请改数。
//	争议工单  ：出现两份成绩或申诉 → 裁判长判定是非。方向是裁定结论。
// ============================================================================

// DisputeKind 争议类型。
type DisputeKind string

const (
	DisputeDuplicate DisputeKind = "duplicate"     // 重复打分
	DisputeSync      DisputeKind = "sync_conflict" // 离线补传同步冲突
	DisputeOther     DisputeKind = "other"         // 其他申诉
)

// DisputeSource 工单来源。
type DisputeSource string

const (
	SourceReferee DisputeSource = "referee" // 裁判人工上报
	SourceSystem  DisputeSource = "system"  // 系统自动生成（补传冲突）
)

// DisputeStatus 工单状态。
type DisputeStatus string

const (
	DisputePending   DisputeStatus = "pending"   // 待裁定
	DisputeDecided   DisputeStatus = "decided"   // 已裁定
	DisputeWithdrawn DisputeStatus = "withdrawn" // 已撤回（处理前可撤）
)

// DisputeVerdict 裁定结论，对应前端 P8a / P8b / P8c 三档位。
type DisputeVerdict string

const (
	VerdictUphold     DisputeVerdict = "uphold"     // 维持原判（P8a）
	VerdictAdjust     DisputeVerdict = "adjust"     // 授权改分（P8b）
	VerdictDisqualify DisputeVerdict = "disqualify" // 取消资格（P8c）
)

// Dispute 一条争议工单。
type Dispute struct {
	ID     int64  `json:"id"`
	Code   string `json:"code"` // 工单号 D-001，由 ID 派生，不落库
	TeamID int64  `json:"teamId"`
	// RoundNo 轮次。两种争议都发生在「同一队同一轮」上，故非空；
	// 唯一约束 ux_disputes_one_open 依赖它做去重。
	RoundNo        int             `json:"roundNo"`
	Kind           DisputeKind     `json:"kind"`
	Source         DisputeSource   `json:"source"`
	Status         DisputeStatus   `json:"status"`
	Reason         string          `json:"reason"`
	Operator       string          `json:"operator"` // 提出人；系统建单时为「系统·补传」
	Verdict        *DisputeVerdict `json:"verdict,omitempty"`
	Decider        string          `json:"decider"`        // 裁定人（裁判长）
	DecisionReason string          `json:"decisionReason"` // 裁定原因（必填）
	CreatedAt      time.Time       `json:"createdAt"`
	DecidedAt      *time.Time      `json:"decidedAt,omitempty"`
}

// DeriveCode 由 ID 生成工单号。
//
// 刻意不落库：编号只是 ID 的展示形态，存一份就多一处可能与 ID 不一致的状态。
// 前端 P8 显示的 D-002 即由此而来。
func (d *Dispute) DeriveCode() { d.Code = fmt.Sprintf("D-%03d", d.ID) }

// IsOpen 是否仍在待裁定（未结）。
func (d Dispute) IsOpen() bool { return d.Status == DisputePending }

// ---------------------------------------------------------------------------
// 中文标签：供 API 直接返回可读文案，避免前端再维护一份映射
// ---------------------------------------------------------------------------

// KindLabel 类型中文名。
func (k DisputeKind) Label() string {
	switch k {
	case DisputeDuplicate:
		return "重复打分"
	case DisputeSync:
		return "同步冲突"
	case DisputeOther:
		return "其他"
	}
	return string(k)
}

// SourceLabel 来源中文名。
func (s DisputeSource) Label() string {
	switch s {
	case SourceReferee:
		return "人工上报"
	case SourceSystem:
		return "系统 · 补传"
	}
	return string(s)
}

// StatusLabel 状态中文名。
func (s DisputeStatus) Label() string {
	switch s {
	case DisputePending:
		return "待裁定"
	case DisputeDecided:
		return "已裁定"
	case DisputeWithdrawn:
		return "已撤回"
	}
	return string(s)
}

// VerdictLabel 裁定结论中文名。
func (v DisputeVerdict) Label() string {
	switch v {
	case VerdictUphold:
		return "维持原判"
	case VerdictAdjust:
		return "授权改分"
	case VerdictDisqualify:
		return "取消资格"
	}
	return string(v)
}
