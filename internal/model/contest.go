package model

import "time"

// ContestStatus 赛事状态。
//
// 与前端 demo 的四态一致：筹备中 / 进行中 / 已结束 / 已归档。
// 归档 = 只读快照，不可逆地拒绝写入（前端在 save() 层硬拦，不是只提示）。
type ContestStatus string

const (
	ContestPrep     ContestStatus = "prep"
	ContestLive     ContestStatus = "live"
	ContestDone     ContestStatus = "done"
	ContestArchived ContestStatus = "archived"
)

// Contest 一场赛事。
//
// 一场赛事 = 一次具体举办（如「2026 WRC 中国区总决赛 · 杭州」）。
// ⚠️ 与「赛项 Event」不是一个层级：赛事是容器，赛项是容器里的比赛项目。
//    前端术语口径见仓库文档；后端落地为 events.contest_id 引用本表。
type Contest struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Season     string        `json:"season"`
	StartDate  *time.Time    `json:"startDate,omitempty"`
	EndDate    *time.Time    `json:"endDate,omitempty"`
	Venue      string        `json:"venue"`
	Host       string        `json:"host"`
	Status     ContestStatus `json:"status"`
	CreatedAt  time.Time     `json:"createdAt"`
	UpdatedAt  time.Time     `json:"updatedAt"`
	ArchivedAt *time.Time    `json:"archivedAt,omitempty"`
}

// IsArchived 是否已归档（只读快照，拒绝一切写入）。
func (c *Contest) IsArchived() bool { return c.Status == ContestArchived }

// IsReadOnly 是否只读。已归档的赛事不允许任何修改。
func (c *Contest) IsReadOnly() bool { return c.Status == ContestArchived }
