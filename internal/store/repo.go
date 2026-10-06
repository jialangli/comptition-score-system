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
	Contests  ContestRepo // 赛事（0004 多赛事分区根）
	Events    EventRepo
	Teams     TeamRepo
	Scores    ScoreRepo
	Seats     SeatRepo
	Slots     SlotRepo
	Audits    AuditRepo
	Screen    ScreenRepo
	Snapshots SnapshotRepo       // 加时赛场内快照
	CfgSnaps  ConfigSnapshotRepo // 配置快照
	Changes   ChangeRequestRepo  // 改分申请单
	Disputes  DisputeRepo        // 争议工单（0005）
	Releases  ReleaseUnitRepo    // 发布单元（0006）
	Referees  RefereeCodeRepo    // 裁判码（0007）
	Locks     WriteLockRepo      // 赛台-队伍可写锁（0008）
	Evidence  EvidenceRepo       // 留底证据库（0009）
}

// ---------------------------------------------------------------------------
// 赛事
// ---------------------------------------------------------------------------

// ContestRepo 赛事读写（0004 多赛事维度）。
//
// 赛事是其余各表的分区根：所有业务表的外键都指向 contests(id)，
// 且 ON DELETE RESTRICT —— 删赛事必须先清干净它的数据。
type ContestRepo interface {
	Create(ctx context.Context, c *model.Contest) error
	Get(ctx context.Context, id string) (*model.Contest, error)
	List(ctx context.Context) ([]model.Contest, error)
	SetStatus(ctx context.Context, id string, status model.ContestStatus) error
	Delete(ctx context.Context, id string) error
	// EnsureDefault 确保兜底赛事存在（TRUNCATE 后需要重建，否则外键会拦住建赛项）。
	EnsureDefault(ctx context.Context) error
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
// 改分申请单
// ---------------------------------------------------------------------------

// ChangeRequestRepo 改分申请单读写。
//
// 「同一队同一轮只允许一条待审批申请」由部分唯一索引 ux_scr_one_pending 兜底，
// 违反时 Create 返回 ErrDuplicate —— 去重放在数据库而不是应用层先查再插，
// 因为后者存在竞态窗口（两个请求同时查到「没有」就会都插进去）。
type ChangeRequestRepo interface {
	// Create 落库一条申请，回填 ID / CreatedAt。重复待审批返回 ErrDuplicate。
	Create(ctx context.Context, r *model.ScoreChangeRequest) error
	// Get 按申请单号读取；不存在返回 ErrNotFound。
	Get(ctx context.Context, id int64) (*model.ScoreChangeRequest, error)
	// PendingOf 取某队某轮**尚未处理**的申请；没有则返回 ErrNotFound。
	PendingOf(ctx context.Context, teamID int64, round int) (*model.ScoreChangeRequest, error)
	// ListPending 待审批队列，按申请时间正序（先到先审）。
	ListPending(ctx context.Context) ([]model.ScoreChangeRequest, error)
	// Decide 标记申请已处理（授权或驳回），写入审批人与处理时间。
	// 不存在返回 ErrNotFound，重复处理返回 ErrConflict。
	Decide(ctx context.Context, id int64, approver string, approved bool) error
	// SetAppeal 关联申述书照片证据（appeal_evidence_id）；用于 P8b 授权改分
	// 生成改分单时从争议单继承同一张，实现「两处都挂」。不存在返回 ErrNotFound。
	SetAppeal(ctx context.Context, id int64, appealEvidenceID int64) error
	// ListByTeam 某队全部历史申请（含已处理），按时间倒序。
	ListByTeam(ctx context.Context, teamID int64) ([]model.ScoreChangeRequest, error)
}

// ---------------------------------------------------------------------------
// 争议工单（0005）
// ---------------------------------------------------------------------------

// DisputeRepo 争议工单读写。
//
// 与 ChangeRequestRepo 的关键差异：**允许再裁定**。
// ChangeRequestRepo.Decide 带 `AND NOT approved` 守卫（一单只能批一次），
// 本接口的 Decide 不加该守卫 —— 前端 P8e 明确支持「确需推翻时再裁定一次并留痕」，
// 每次裁定都写审计，本表只保留最新结论。
//
// 「同一队同一轮同一类型最多一条待裁定」由部分唯一索引 ux_disputes_one_open 兜底，
// 违反时 Create 返回 ErrDuplicate —— 去重放在数据库，因为离线补传是并发的，
// 应用层「先查再插」两个请求都会查到「没有」然后都插进去。
//
// 全部方法按当前赛事（ctx 中的 contest_id）分区，实现层自行注入，调用方无感。
type DisputeRepo interface {
	// Create 落库一条工单，回填 ID / CreatedAt / Code。
	// 同队同轮同类型已有待裁定工单时返回 ErrDuplicate。
	Create(ctx context.Context, d *model.Dispute) error
	// Get 按工单号读取；不存在返回 ErrNotFound。
	Get(ctx context.Context, id int64) (*model.Dispute, error)
	// ListOpen 待裁定队列，先到先裁（P8 队列页数据源）。
	ListOpen(ctx context.Context) ([]model.Dispute, error)
	// ListByTeam 某队全部历史工单（含已裁定 / 已撤回），按时间倒序。
	// 用于成绩单页判断该队是否被判取消资格。
	ListByTeam(ctx context.Context, teamID int64) ([]model.Dispute, error)
	// Decide 裁定：写入结论、裁定人、原因与时间。允许对已裁定工单再裁定。
	// 不存在返回 ErrNotFound。
	Decide(ctx context.Context, id int64, decider string,
		verdict model.DisputeVerdict, reason string) error
	// Withdraw 撤回：仅在待裁定状态下允许（已裁定不可撤，只能再裁定）。
	// 不存在或状态不允许返回 ErrNotFound。
	Withdraw(ctx context.Context, id int64) error
}

// ---------------------------------------------------------------------------
// 发布单元（0006）
// ---------------------------------------------------------------------------

// ReleaseUnitRepo 发布单元读写（赛项 × 组别 × 赛台）。
//
// 状态机由 service 层把关，本接口只做「按条件更新」：
// 每条 UPDATE 都带上允许的前置状态（如 `AND status IN ('handed','pending')`），
// 命中 0 行即返回 ErrNotFound —— 这样并发下两个运营同时点发布，
// 只有一个会成功，另一个拿到「已发布 / 状态已变」而不是静默重复发布。
//
// 全部方法按当前赛事（ctx 中的 contest_id）分区。
type ReleaseUnitRepo interface {
	// Ensure 按（赛项, 组别, 赛台）取发布单元，不存在则创建（幂等）。
	// 建档与取用是同一个动作：运营不需要先「建单元」再「移交」两步走。
	Ensure(ctx context.Context, eventID, groupCode string, seatID *int64) (*model.ReleaseUnit, error)
	Get(ctx context.Context, id int64) (*model.ReleaseUnit, error)
	// ListByContest 本赛事全部发布单元（P13 看板数据源），按赛项、组别排序。
	ListByContest(ctx context.Context) ([]model.ReleaseUnit, error)
	// HandOver 移交：not_handed / pending（重发后可再移交）→ handed。
	HandOver(ctx context.Context, id int64, operator string) error
	// Receive 接收：handed → pending。
	Receive(ctx context.Context, id int64, operator string) error
	// Publish 发布：handed / pending → published，清空重发标记。
	Publish(ctx context.Context, id int64, operator string) error
	// MarkRepublish 标记重发：published → pending 且 republish_required=true。
	MarkRepublish(ctx context.Context, id int64, reason string) error
}

// ---------------------------------------------------------------------------
// 裁判码（0007）
// ---------------------------------------------------------------------------

// RefereeCodeRepo 裁判码读写。
//
// 查码一律按当前赛事 —— 裁判码是赛事级凭证，换赛事必须重新建档发码。
// 码本身不建额外索引之外的保护：本期鉴权仍是插槽（api.CurrentUser），
// 裁判码只做「这个人是谁、执裁范围是什么」的解析，不签发会话。
type RefereeCodeRepo interface {
	// Create 建档一条裁判码；同赛事内码重复返回 ErrDuplicate。
	Create(ctx context.Context, c *model.RefereeCode) error
	// GetByCode 按码查档（仅当前赛事）；不存在返回 ErrNotFound。
	// 调用方据此区分「码无效」与「姓名不匹配」两种失败。
	GetByCode(ctx context.Context, code string) (*model.RefereeCode, error)
	// ListByContest 本赛事全部裁判码，按建档时间倒序。
	ListByContest(ctx context.Context) ([]model.RefereeCode, error)
	// MarkActivated 标记已激活（首登联网激活）；不存在或已作废返回 ErrNotFound。
	MarkActivated(ctx context.Context, id int64) error
}

// ---------------------------------------------------------------------------
// 赛台-队伍可写锁（0008）
// ---------------------------------------------------------------------------

// WriteLockRepo 可写锁读写。
//
// 锁的本质就是 ux_team_write_lock 唯一索引：抢占用 INSERT ... ON CONFLICT，
// 由数据库裁决谁是第一个 —— 应用层「先查再插」在两台平板同时提交时，
// 两边都会查到「没有」。
//
// 关键约定：
//   - 过期锁（expires_at < now）视为不存在，可被直接覆盖
//   - 只有持锁者能释放（释放带 `AND holder = $n`）
//   - 本仓储**不写审计**：抢锁发生在每一次提交 / 暂存，频率极高
type WriteLockRepo interface {
	// Acquire 抢占（赛台, 队伍）的写锁。
	//   - 无人持锁或锁已过期 → 拿到锁，返回 acquired=true
	//   - 自己已持锁 → 续期，返回 acquired=true
	//   - 他人持锁 → 拿不到，返回 acquired=false 并带回持锁者信息
	Acquire(ctx context.Context, seatID, teamID int64, holder, holderLabel string,
		ttl time.Duration) (*model.WriteLock, bool, error)
	// Get 查看当前锁（含过期判断所需的完整信息）；无锁返回 ErrNotFound。
	Get(ctx context.Context, seatID, teamID int64) (*model.WriteLock, error)
	// Release 释放自己的锁。只有持锁者能释放，否则命中 0 行 → ErrNotFound。
	Release(ctx context.Context, seatID, teamID int64, holder string) error
	// ForceRelease 强制释放（裁判长 / 运维处置平板掉线）。任意持锁者都可被解开。
	ForceRelease(ctx context.Context, seatID, teamID int64) error
}

// ---------------------------------------------------------------------------
// 留底证据库（0009）
// ---------------------------------------------------------------------------

// EvidenceRepo 留底证据读写。
//
// 只存**元数据**，不存二进制本体 —— 图片本体属于对象存储的职责。
// 本仓储要回答的是合规追溯真正的问题：这一轮证据齐不齐、谁产生的、上云了没有。
//
// 去重落在业务键（队伍, 轮次, 类型, 文件名）的唯一索引上：
// 补传会重试，不去重会让「证据三件」变成「七件」，反而没法判断齐不齐。
type EvidenceRepo interface {
	// Create 登记一条证据；重复（同队伍同轮次同类型同文件名）返回 ErrDuplicate。
	Create(ctx context.Context, e *model.Evidence) error
	// Get 按证据 ID 读取；不存在返回 ErrNotFound。
	Get(ctx context.Context, id int64) (*model.Evidence, error)
	// GetByDispute 取某争议工单关联的申述书照片（kind=appeal 且 dispute_id 匹配）。
	GetByDispute(ctx context.Context, disputeID int64) ([]model.Evidence, error)
	// MarkSynced 标记为已上云；不存在返回 ErrNotFound。
	MarkSynced(ctx context.Context, id int64, storageURL string) error
	// ListByTeam 某队全部证据；按轮次可筛选（round=0 表示不过滤）。
	ListByTeam(ctx context.Context, teamID int64, round int) ([]model.Evidence, error)
	// ListPending 待上云队列（断网时先落本地，联网后补传）。
	ListPending(ctx context.Context) ([]model.Evidence, error)
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
