package model

import "time"

// ============================================================================
// 发布单元与移交 / 发布状态机（0006）
//
// 发布单元 = **赛项 × 组别 × 赛台**（前端 P13 定义）。
// 不是赛项、也不是整场赛事 —— 一个赛项在三个赛台上就是三个单元，
// 因为它们分别移交、分别发布。
//
// 四态流转（P13 note 2）：
//
//	未移交 → 已移交·处理中 → ⏳ 待发布 → ✓ 运营已发布
//
// 前端表格的「移交状态」与「发布状态」两列是**同一状态的两种视图**，
// 因此这里只存一个 status，两列文案由 DeriveLabels 派生 —— 存两份真值迟早会打架。
// ============================================================================

// ReleaseStatus 发布单元状态。
type ReleaseStatus string

const (
	ReleaseNotHanded ReleaseStatus = "not_handed" // 未移交
	ReleaseHanded    ReleaseStatus = "handed"     // 已移交 · 处理中（工作人员尚未接收）
	ReleasePending   ReleaseStatus = "pending"    // ⏳ 待发布（已接收，等运营点发布）
	ReleasePublished ReleaseStatus = "published"  // ✓ 运营已发布
)

// ReleaseUnit 一个发布单元。
type ReleaseUnit struct {
	ID        int64         `json:"id"`
	EventID   string        `json:"eventId"`
	GroupCode string        `json:"groupCode"`
	SeatID    *int64        `json:"seatId,omitempty"`
	Status    ReleaseStatus `json:"status"`

	// 前端两列视图（由 DeriveLabels 填充，不落库）
	HandoverLabel string `json:"handoverLabel"`
	PublishLabel  string `json:"publishLabel"`

	HandedBy   string     `json:"handedBy"`
	HandedAt   *time.Time `json:"handedAt,omitempty"`
	ReceivedBy string     `json:"receivedBy"`
	ReceivedAt *time.Time `json:"receivedAt,omitempty"`
	// PublishedBy / PublishedAt 即 P13 表格最后一列「发布人 / 时间」
	PublishedBy string     `json:"publishedBy"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`

	// 已发布后又发生改分 / 裁定生效 → 需重发，状态同时回退为 pending（P13 note 5）
	RepublishRequired bool   `json:"republishRequired"`
	RepublishReason   string `json:"republishReason"`

	CreatedAt time.Time `json:"createdAt"`
}

// DeriveLabels 由状态派生前端两列文案。
//
// 重发态在「待发布」之后追加「（待重发）」而不是新增第五个状态：
// 它与首次待发布在后端是同一件事（都是等运营点发布），差别只在前端要不要标红。
func (u *ReleaseUnit) DeriveLabels() {
	switch u.Status {
	case ReleaseNotHanded:
		u.HandoverLabel, u.PublishLabel = "未移交", "— 未移交"
	case ReleaseHanded:
		u.HandoverLabel, u.PublishLabel = "已移交 · 处理中", "— 待接收"
	case ReleasePending:
		u.HandoverLabel = "已移交 · 待发布"
		u.PublishLabel = "⏳ 待发布"
		if u.RepublishRequired {
			u.PublishLabel = "⏳ 待发布（待重发）"
		}
	case ReleasePublished:
		u.HandoverLabel, u.PublishLabel = "已移交 · 已发布", "✓ 运营已发布"
	default:
		u.HandoverLabel, u.PublishLabel = string(u.Status), string(u.Status)
	}
}

// StatusLabel 状态中文名（审计与提示用）。
func (s ReleaseStatus) Label() string {
	switch s {
	case ReleaseNotHanded:
		return "未移交"
	case ReleaseHanded:
		return "已移交 · 处理中"
	case ReleasePending:
		return "待发布"
	case ReleasePublished:
		return "运营已发布"
	}
	return string(s)
}

// CanPublish 是否可被运营点发布。
func (s ReleaseStatus) CanPublish() bool {
	return s == ReleaseHanded || s == ReleasePending
}
