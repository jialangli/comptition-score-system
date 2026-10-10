package postgres

import (
	"context"
	"strconv"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 赛台 / 场次 / 加时赛快照
//
// 赛台是赛事级资源（不带赛项归属），同一赛台可跨时段、跨赛项复用；
// 一个「赛台 × 时段」只能有一个场次，由 slots 的唯一约束保证。
// ============================================================================

// SeatStore 赛台仓储。
type SeatStore struct{ q querier }

const seatColumns = `id, name, sort_order, created_at`

func scanSeat(row interface{ Scan(...any) error }) (*model.Seat, error) {
	var s model.Seat
	if err := row.Scan(&s.ID, &s.Name, &s.SortOrder, &s.CreatedAt); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	return &s, nil
}

// Create 新增赛台。
func (s *SeatStore) Create(ctx context.Context, seat *model.Seat) error {
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO seats (name, sort_order) VALUES ($1,$2)
		RETURNING id, created_at`,
		seat.Name, seat.SortOrder).Scan(&seat.ID, &seat.CreatedAt))
}

// Get 读取赛台。
func (s *SeatStore) Get(ctx context.Context, id int64) (*model.Seat, error) {
	return scanSeat(s.q.QueryRow(ctx, `SELECT `+seatColumns+` FROM seats WHERE id=$1`, id))
}

// List 返回全部赛台，按运营配置的顺序排列。
func (s *SeatStore) List(ctx context.Context) ([]model.Seat, error) {
	rows, err := s.q.Query(ctx, `SELECT `+seatColumns+` FROM seats ORDER BY sort_order, id`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.Seat
	for rows.Next() {
		seat, err := scanSeat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *seat)
	}
	return out, mapError(rows.Err())
}

// Update 更新赛台名称与排序。
func (s *SeatStore) Update(ctx context.Context, seat *model.Seat) error {
	tag, err := s.q.Exec(ctx, `UPDATE seats SET name=$2, sort_order=$3 WHERE id=$1`,
		seat.ID, seat.Name, seat.SortOrder)
	return affected(tag, err)
}

// Delete 删除赛台；其下场次与场次内的队伍绑定 / 快照会级联清理。
func (s *SeatStore) Delete(ctx context.Context, id int64) error {
	// 先让归到该台的队伍退回「未排台」，且**顺位一并归零**。
	//
	// teams.seat_id 的外键是 ON DELETE SET NULL，它只置空 seat_id，
	// 会把 seat_order 留在原位 —— 库里于是留下「未排台但顺位 3」这种怪状态。
	// 顺位对未排台的队伍没有意义，在这里一起清掉，好过让每个读点都记住
	// 「seat_id 为 NULL 时忽略 seat_order」。
	//
	// 不带 contest_id 过滤：seats 是全局资源（表里没有赛事维度），
	// 归到该台的队伍不论属哪场赛事都要退回未排台。
	if _, err := s.q.Exec(ctx,
		`UPDATE teams SET seat_order = 0 WHERE seat_id = $1`, id); err != nil {
		return mapError(err)
	}
	tag, err := s.q.Exec(ctx, `DELETE FROM seats WHERE id=$1`, id)
	return affected(tag, err)
}

// ---------------------------------------------------------------------------

// SlotStore 场次仓储。
type SlotStore struct{ q querier }

const slotColumns = `id, seat_id, period, time_range, event_id, group_code, slot_type, round_no, created_at`

func scanSlot(row interface{ Scan(...any) error }) (*model.Slot, error) {
	var s model.Slot
	var typ string
	if err := row.Scan(&s.ID, &s.SeatID, &s.Period, &s.TimeRange, &s.EventID,
		&s.GroupCode, &typ, &s.RoundNo, &s.CreatedAt); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	s.Type = model.SlotType(typ)
	return &s, nil
}

// Create 新增场次。
func (s *SlotStore) Create(ctx context.Context, slot *model.Slot) error {
	if slot.Type == "" {
		slot.Type = model.SlotNormal
	}
	if slot.RoundNo == 0 {
		slot.RoundNo = model.RoundOfPeriod(slot.Period)
	}
	// contest_id 必须显式写：列上有 DEFAULT 'ct_default'，不给就会落到默认赛事，
	// 而派生队伍是按赛事过滤的 —— 场次的赛事维度错了，队伍一个都派生不出来。
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO slots (seat_id, period, time_range, event_id, group_code, slot_type, round_no, contest_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, created_at`,
		slot.SeatID, slot.Period, slot.TimeRange, slot.EventID,
		slot.GroupCode, string(slot.Type), slot.RoundNo,
		store.CurrentContest(ctx)).Scan(&slot.ID, &slot.CreatedAt))
}

// Get 读取场次，并带出正式场次**派生**出的队伍与独立场次的场内快照。
func (s *SlotStore) Get(ctx context.Context, id int64) (*model.Slot, error) {
	slot, err := scanSlot(s.q.QueryRow(ctx, `SELECT `+slotColumns+` FROM slots WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := s.loadDerivedTeams(ctx, slot); err != nil {
		return nil, err
	}
	if slot.Type.Extra() {
		snaps, err := (&SnapshotStore{q: s.q}).List(ctx, id)
		if err != nil {
			return nil, err
		}
		slot.Snapshot = snaps
	}
	return slot, nil
}

// List 按赛台 / 赛项筛选场次；两者传 0 与空串表示不过滤。
func (s *SlotStore) List(ctx context.Context, seatID int64, eventID string) ([]model.Slot, error) {
	rows, err := s.q.Query(ctx, `
		SELECT `+slotColumns+` FROM slots
		WHERE ($1 = 0 OR seat_id = $1) AND ($2 = '' OR event_id = $2)
		ORDER BY seat_id, period, id`, seatID, eventID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.Slot
	for rows.Next() {
		slot, err := scanSlot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *slot)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	// 列表场景把派生队伍与快照也带上，避免调用方逐个 Get（赛台页需要一次看到全部）
	for i := range out {
		if err := s.loadDerivedTeams(ctx, &out[i]); err != nil {
			return nil, err
		}
		if out[i].Type.Extra() {
			snaps, err := (&SnapshotStore{q: s.q}).List(ctx, out[i].ID)
			if err != nil {
				return nil, err
			}
			out[i].Snapshot = snaps
		}
	}
	return out, nil
}

// UpdateMeta 更新场次元信息（不动队伍 —— 队伍只由「队伍级归台」决定）。
func (s *SlotStore) UpdateMeta(ctx context.Context, slot *model.Slot) error {
	tag, err := s.q.Exec(ctx, `
		UPDATE slots SET seat_id=$2, period=$3, time_range=$4, event_id=$5,
		                 group_code=$6, slot_type=$7, round_no=$8
		WHERE id=$1`,
		slot.ID, slot.SeatID, slot.Period, slot.TimeRange, slot.EventID,
		slot.GroupCode, string(slot.Type), slot.RoundNo)
	return affected(tag, err)
}

// Delete 删除场次（场内快照级联清理；slot_teams 已是历史表，同场次的行一并级联）。
func (s *SlotStore) Delete(ctx context.Context, id int64) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM slots WHERE id=$1`, id)
	return affected(tag, err)
}

// loadDerivedTeams 派生正式场次的队伍 ID。
//
// **唯一事实源是队伍级归台**（`teams.seat_id` / `seat_order` / `session`）：
// 场次队伍不再存储，`slot_teams` 退化为历史值（本方法不再读它）。
//
// 判据与前端 `slotTeams()` 逐字同口径：
//
//	同赛台（seat_id）&& 同赛项（event_id）&& 同组别（group_code）
//	&& 在册（status='active'）&& 该队参赛轮次覆盖本场次轮次
//
// 独立场次（加时赛 / 重赛）不走派生 —— 它的队伍来自场内快照。
func (s *SlotStore) loadDerivedTeams(ctx context.Context, slot *model.Slot) error {
	if slot.Type.Extra() {
		return nil
	}
	rows, err := s.q.Query(ctx, `
		SELECT id FROM teams
		WHERE seat_id = $1 AND event_id = $2 AND group_code = $3
		  AND status = 'active' AND contest_id = $4
		  AND (session = 'both' OR session = $5)
		ORDER BY seat_order, id`,
		slot.SeatID, slot.EventID, slot.GroupCode,
		store.CurrentContest(ctx), strconv.Itoa(slot.RoundNo))
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return mapError(err)
		}
		slot.TeamIDs = append(slot.TeamIDs, id)
	}
	return mapError(rows.Err())
}

// ---------------------------------------------------------------------------

// SnapshotStore 加时赛场内快照仓储。
//
// 这张表的存在本身就是一条约束：加时赛队伍的数据落在独立表里，
// 主库 teams 表绝不会因此多出一行。
type SnapshotStore struct{ q querier }

// Save 整体替换某场次的快照（先删后插，需在事务内调用）。
func (s *SnapshotStore) Save(ctx context.Context, slotID int64, snaps []model.Snapshot) error {
	if _, err := s.q.Exec(ctx,
		`DELETE FROM slot_snapshots WHERE slot_id=$1 AND contest_id=$2`,
		slotID, store.CurrentContest(ctx)); err != nil {
		return mapError(err)
	}
	for i := range snaps {
		sn := &snaps[i]
		if _, err := s.q.Exec(ctx, `
			INSERT INTO slot_snapshots (slot_id, team_no, name, school, coach, contest_id)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (contest_id, slot_id, team_no) DO UPDATE SET
				name=EXCLUDED.name, school=EXCLUDED.school, coach=EXCLUDED.coach`,
			slotID, sn.TeamNo, sn.Name, sn.School, sn.Coach,
			store.CurrentContest(ctx)); err != nil {
			return mapError(err)
		}
	}
	return nil
}

// List 读取某场次的快照，按编号升序。
func (s *SnapshotStore) List(ctx context.Context, slotID int64) ([]model.Snapshot, error) {
	rows, err := s.q.Query(ctx, `
		SELECT id, slot_id, team_no, name, school, coach
		FROM slot_snapshots WHERE slot_id=$1 AND contest_id=$2 ORDER BY team_no`,
		slotID, store.CurrentContest(ctx))
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.Snapshot
	for rows.Next() {
		var sn model.Snapshot
		if err := rows.Scan(&sn.ID, &sn.SlotID, &sn.TeamNo, &sn.Name, &sn.School, &sn.Coach); err != nil {
			return nil, mapError(err)
		}
		out = append(out, sn)
	}
	return out, mapError(rows.Err())
}
