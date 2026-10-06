package model

import "time"

// ============================================================================
// 留底证据库（0009）
//
// 证据三件：成绩表 / 签名图 / 提交留底截图。
//
// 三条来自 wireframe 的口径：
//
//  1. **产生端各不相同**（P2e note 7）：裁判提交成绩、裁判长提交裁定、
//     工作人员发布 —— 「规范统一、触发点各异」，所以 source 必须能区分。
//  2. **状态一律以「是否已上云」为准**：本地先落、异步同步。
//  3. **作废不抹除证据**：成绩作废只改「是否计入排名」，证据仍完整留存。
//
// ⚠️ 本模型只描述**元数据**，不承载二进制本体 —— 图片本体属于对象存储的职责。
// 后端要回答的是合规追溯真正的问题：这一轮证据齐不齐、谁产生的、上云了没有。
// ============================================================================

// EvidenceKind 证据类型。
type EvidenceKind string

const (
	EvScoreSheet EvidenceKind = "score_sheet"     // 成绩表
	EvSignature  EvidenceKind = "signature"       // 签名图
	EvSubmitSnap EvidenceKind = "submit_snapshot" // 提交留底截图
	EvDecision   EvidenceKind = "decision"        // 裁定单（裁判长产生）
	EvRelease    EvidenceKind = "release"         // 发布产物（工作人员产生）
)

// EvidenceSource 产生端。
type EvidenceSource string

const (
	SrcRefereeSubmit EvidenceSource = "referee_submit" // 裁判提交成绩时
	SrcChiefDecide   EvidenceSource = "chief_decide"   // 裁判长提交裁定时
	SrcStaffPublish  EvidenceSource = "staff_publish"  // 工作人员发布时
)

// EvidenceStatus 上云状态。
type EvidenceStatus string

const (
	EvLocal  EvidenceStatus = "local"  // 本地已生成，待同步
	EvSynced EvidenceStatus = "synced" // 已上云
)

// Evidence 一条留底证据。
type Evidence struct {
	ID          int64          `json:"id"`
	TeamID      int64          `json:"teamId"`
	RoundNo     *int           `json:"roundNo,omitempty"` // 可空：发布 / 部分裁定类不挂轮次
	Kind        EvidenceKind   `json:"kind"`
	KindLabel   string         `json:"kindLabel"`
	Source      EvidenceSource `json:"source"`
	SourceLabel string         `json:"sourceLabel"`
	FileName    string         `json:"fileName"` // 如 T-001_R1_1240.jpg
	Status      EvidenceStatus `json:"status"`
	StatusLabel string         `json:"statusLabel"`
	Operator    string         `json:"operator"`
	SeatID      *int64         `json:"seatId,omitempty"`
	DisputeID   *int64         `json:"disputeId,omitempty"`
	// StorageURL 本体存放位置；本期后端不存二进制，接入对象存储后填写
	StorageURL string     `json:"storageUrl"`
	CreatedAt  time.Time  `json:"createdAt"`
	SyncedAt   *time.Time `json:"syncedAt,omitempty"`
}

// DeriveLabels 填充三类中文标签，避免前端再维护一份映射。
func (e *Evidence) DeriveLabels() {
	e.KindLabel, e.SourceLabel, e.StatusLabel = e.Kind.Label(), e.Source.Label(), e.Status.Label()
}

// Label 类型中文名。
func (k EvidenceKind) Label() string {
	switch k {
	case EvScoreSheet:
		return "成绩表"
	case EvSignature:
		return "签名图"
	case EvSubmitSnap:
		return "提交留底截图"
	case EvDecision:
		return "裁定单"
	case EvRelease:
		return "发布产物"
	}
	return string(k)
}

// Label 产生端中文名。
func (s EvidenceSource) Label() string {
	switch s {
	case SrcRefereeSubmit:
		return "裁判提交成绩"
	case SrcChiefDecide:
		return "裁判长提交裁定"
	case SrcStaffPublish:
		return "工作人员发布"
	}
	return string(s)
}

// Label 状态中文名。
func (s EvidenceStatus) Label() string {
	switch s {
	case EvLocal:
		return "本地待同步"
	case EvSynced:
		return "已上云"
	}
	return string(s)
}

// EvidenceKinds 裁判提交成绩时自动生成的「证据三件」。
//
// 用于校验「三件齐不齐」—— 缺任何一件都不算完成留底。
func EvidenceKinds() []EvidenceKind {
	return []EvidenceKind{EvScoreSheet, EvSignature, EvSubmitSnap}
}
