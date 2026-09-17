package store

import (
	"context"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 各业务域的存储契约
//
// 为什么要把接口写在这里而不是直接用具体实现：
//
//  1. service 层不 import pgx —— 换驱动 / 加测试替身都不用改业务代码。
//  2. 更重要的是**可注入**：`Repos` 是一个普通结构体，测试里可以把某一个
//     Repo 换成「注定失败」的装饰器，从而验证「审计写失败时业务改动会回滚」。
//     这条不变式靠人工 review 是保不住的，必须能构造出来才测得动。
//
// 约定：
//   - 所有方法都接受 context，尊重取消与超时
//   - 找不到记录返回 ErrNotFound（不返回驱动原始错误）
//   - 违反唯一约束返回 ErrDuplicate，违反外键返回 ErrInUse
// ============================================================================

// Store 存储层总入口。
type Store interface {
	// Ping 连通性检查，供 /healthz 使用。
	Ping(ctx context.Context) error
	// Close 释放连接池。
	Close()
	// Repos 返回非事务的读写入口。
	Repos() Repos
	// WithTx 在一个数据库事务内执行 fn。
	//
	// fn 内的所有写入要么一起成功、要么一起回滚。审计留痕必须走这里 ——
	// 结构上杜绝「改了数据但没留痕」。
	// fn 返回错误则回滚（含 panic 场景，由实现保证）。
	WithTx(ctx context.Context, fn func(Repos) error) error
}

// Repos 一组业务域仓储。事务内外拿到的是同一个类型，业务代码无需区分。
type Repos struct {
	Events    EventRepo
	Teams     TeamRepo
	Scores    ScoreRepo
	Seats     SeatRepo
	Slots     SlotRepo
	Audits    AuditRepo
	Screen    ScreenRepo
	Snapshots SnapshotRepo       // 加时赛场内快照
	CfgSnaps  ConfigSnapshotRepo // 配置快照
}

// ---------------------------------------------------------------------------
// 赛项与任务
// ---------------------------------------------------------------------------

// EventRepo 赛项读写。
type EventRepo interface {
	Create(ctx context.Context, ev *model.Event) error
	// Get 读取赛项及其全部任务项；不存在返回 ErrNotFound。
	Get(ctx context.Context, id string) (*model.Event, error)
	// List 按 id 升序返回全部赛项（含任务项）。
	List(ctx context.Context) ([]model.Event, error)
	// Update 更新赛项的规则部分（不含任务项）；不存在返回 ErrNotFound。
	Update(ctx context.Context, ev *model.Event) error
	// ReplaceTasks 整体替换赛项的任务项（先删后插，同一事务内完成）。
	ReplaceTasks(ctx context.Context, eventID string, tasks []model.Task) error
	// Delete 删除赛项；被队伍引用时返回 ErrInUse。
	Delete(ctx context.Context, id string) error
}

// ---------------------------------------------------------------------------
// 队伍
// ---------------------------------------------------------------------------

// TeamRepo 队伍读写。
//
// 「一号一队」由数据库唯一索引 ux_teams_event_no 兜底，违反时返回 ErrDuplicate，
// 上层据此拦截并交运营裁决。
type TeamRepo interface {
	Create(ctx context.Context, t *model.Team) error
	Get(ctx context.Context, id int64) (*model.Team, error)
	GetByNo(ctx context.Context, eventID, teamNo string) (*model.Team, error)
	// ListByEvent 按录入顺序返回赛项下的队伍；includeWithdrawn 决定是否含弃赛队伍。
	ListByEvent(ctx context.Context, eventID string, includeWithdrawn bool) ([]model.Team, error)
	// Upsert 合并式写入：按 (event_id, team_no) 命中则更新，否则插入。
	// 返回是否为新增。用于报名导入，绝不物理删除已有队伍。
	Upsert(ctx context.Context, t *model.Team) (created bool, err error)
	// Update 更新可变字段（名称/学校/教练/组别/成员）。
	Update(ctx context.Context, t *model.Team) error
	SetStatus(ctx context.Context, id int64, status model.TeamStatus) error
	SetGroup(ctx context.Context, id int64, group string) error
	Delete(ctx context.Context, id int64) error
	// CountByEvent 统计赛项下队伍数（含弃赛，便于界面提示）。
	CountByEvent(ctx context.Context, eventID string) (int, error)
}

// ---------------------------------------------------------------------------
// 打分记录
// ---------------------------------------------------------------------------

// ScoreRepo 打分记录读写。
type ScoreRepo interface {
	// Save 按 (team_id, round_no) 唯一键合并写入，返回记录 ID。
	Save(ctx context.Context, r *model.ScoreRecord) error
	Get(ctx context.Context, teamID int64, round int) (*model.ScoreRecord, error)
	ListByTeam(ctx context.Context, teamID int64) ([]model.ScoreRecord, error)
	// ListByEvent 按 teamID → 各轮记录返回，供榜单计算一次性取数（避免 N+1）。
	ListByEvent(ctx context.Context, eventID string) (map[int64][]model.ScoreRecord, error)
	Delete(ctx context.Context, id int64) error
	// CountByEvent 统计已录入的记录数，用于总览 KPI。
	CountByEvent(ctx context.Context, eventID string) (int, error)
}

// ---------------------------------------------------------------------------
// 赛台与场次
// ---------------------------------------------------------------------------

// SeatRepo 赛台读写（赛事级资源，可跨时段、跨赛项复用）。
type SeatRepo interface {
	Create(ctx context.Context, s *model.Seat) error
	Get(ctx context.Context, id int64) (*model.Seat, error)
	List(ctx context.Context) ([]model.Seat, error)
	Update(ctx context.Context, s *model.Seat) error
	// Delete 删除赛台；其下场次会级联删除（slot_teams / slot_snapshots 同样级联）。
	Delete(ctx context.Context, id int64) error
}

// SlotRepo 场次读写。
type SlotRepo interface {
	Create(ctx context.Context, s *model.Slot) error
	// Get 读取场次，并带出正式场次的队伍 ID 与加时赛的场内快照。
	Get(ctx context.Context, id int64) (*model.Slot, error)
	List(ctx context.Context, seatID int64, eventID string) ([]model.Slot, error)
	// UpdateMeta 只更新场次的元信息，不动队伍绑定。
	UpdateMeta(ctx context.Context, s *model.Slot) error
	Delete(ctx context.Context, id int64) error
	// SetTeams 整体替换正式场次的队伍绑定。
	SetTeams(ctx context.Context, slotID int64, teamIDs []int64) error
}

// SnapshotRepo 加时赛场内快照读写。
//
// 严格约束：本表的数据**永远不写入 teams 主库**。
// 独立表 + 独立接口，从结构上避免「加时赛队伍污染正式库」。
type SnapshotRepo interface {
	// Save 整体替换某场次的快照（先删后插）。
	Save(ctx context.Context, slotID int64, snaps []model.Snapshot) error
	List(ctx context.Context, slotID int64) ([]model.Snapshot, error)
}

// ---------------------------------------------------------------------------
// 审计
// ---------------------------------------------------------------------------

// AuditFilter 审计查询条件。
type AuditFilter struct {
	Action   string
	Operator string
	// Approver 按审批人筛选（只有需授权的操作才有值）。
	Approver string
	Since    *time.Time
	Until    *time.Time
	Limit    int
	Offset   int
}

// AuditRepo 审计读写。
//
// Append 在事务内调用（见 Store.WithTx），与业务改动同生共死。
type AuditRepo interface {
	Append(ctx context.Context, e model.AuditEntry) error
	List(ctx context.Context, f AuditFilter) ([]model.AuditLog, error)
	// CountByAction 按动作统计条数，用于验证「六类操作都留了痕」。
	CountByAction(ctx context.Context) (map[string]int, error)

	AppendImport(ctx context.Context, l *model.ImportLog) error
	ListImports(ctx context.Context, limit int) ([]model.ImportLog, error)
}

// ---------------------------------------------------------------------------
// 大屏与配置快照
// ---------------------------------------------------------------------------

// ScreenRepo 大屏配置读写。
type ScreenRepo interface {
	// Get 读取大屏配置；未配置过时返回带默认值的配置而不是 ErrNotFound ——
	// 大屏页面永远能渲染，运营不需要先「创建配置」。
	Get(ctx context.Context, eventID string) (*model.ScreenConfig, error)
	Upsert(ctx context.Context, c *model.ScreenConfig) error
}

// ConfigSnapshotRepo 配置快照读写（改配置前留档，可回滚）。
type ConfigSnapshotRepo interface {
	Create(ctx context.Context, note string, payload any) (int64, error)
	List(ctx context.Context, limit int) ([]model.ConfigSnapshot, error)
	Get(ctx context.Context, id int64) (*model.ConfigSnapshot, error)
}
