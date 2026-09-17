package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 赛台与赛程用例
//
// 已确认的赛台模型（需求确认单 v1.3）：
//
//	赛台是**赛事级资源**，不带赛项归属 —— 今天比火星救援、明天比未来之城都行
//	赛台数量由运营按参赛人数自行配置
//	「赛台 × 时段」= 一个场次；同一赛项人多时可以同时占多张台
//	分配目标：就近（人数均衡），且运营可以随时手动改派
//
// 所有涉及队伍位置变化的操作都记「调赛台」，这是六类必留痕操作之一。
// ============================================================================

// ListSeats 列出全部赛台。
func (s *Service) ListSeats(ctx context.Context) ([]model.Seat, error) {
	return s.ro().Seats.List(ctx)
}

// CreateSeat 新增赛台。
func (s *Service) CreateSeat(ctx context.Context, name string, order int) (*model.Seat, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &model.FieldError{Field: "name", Msg: "赛台名称不能为空"}
	}
	seat := &model.Seat{Name: name, SortOrder: order}
	if err := s.ro().Seats.Create(ctx, seat); err != nil {
		return nil, err
	}
	return seat, nil
}

// UpdateSeat 修改赛台名称与顺序。
func (s *Service) UpdateSeat(ctx context.Context, id int64, name string, order int) (*model.Seat, error) {
	seat, err := s.ro().Seats.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) != "" {
		seat.Name = strings.TrimSpace(name)
	}
	seat.SortOrder = order
	if err := s.ro().Seats.Update(ctx, seat); err != nil {
		return nil, err
	}
	return seat, nil
}

// DeleteSeat 删除赛台。
//
// 赛台下的场次会级联删除（含队伍绑定与加时赛快照），因此在有场次时必须先说明原因。
func (s *Service) DeleteSeat(ctx context.Context, id int64, reason string) error {
	seat, err := s.ro().Seats.Get(ctx, id)
	if err != nil {
		return err
	}
	slots, err := s.ro().Slots.List(ctx, id, "")
	if err != nil {
		return err
	}
	if len(slots) > 0 {
		if err := requireReason(reason); err != nil {
			return fmt.Errorf("赛台「%s」下还有 %d 个场次，删除会连带清除这些场次：%w",
				seat.Name, len(slots), err)
		}
	}
	if reason == "" {
		reason = "赛台调整"
	}
	return s.tx(ctx, func(r store.Repos) error {
		if err := r.Seats.Delete(ctx, id); err != nil {
			return err
		}
		return log(ctx, r, model.ActSeat, "赛台 "+seat.Name,
			fmt.Sprintf("含 %d 个场次", len(slots)), "已删除", reason)
	})
}

// ListSlots 列出场次（seatID=0 / eventID="" 表示不过滤）。
func (s *Service) ListSlots(ctx context.Context, seatID int64, eventID string) ([]model.Slot, error) {
	return s.ro().Slots.List(ctx, seatID, eventID)
}

// CreateSlot 新建场次（赛台 × 时段）。
//
// 同一赛台同一时段只能有一个场次，由数据库唯一约束兜底 ——
// 违反时返回 store.ErrDuplicate，界面提示「该赛台这个时段已排了别的比赛」。
func (s *Service) CreateSlot(ctx context.Context, d model.SlotDraft) (*model.Slot, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	ev, err := s.ro().Events.Get(ctx, d.EventID)
	if err != nil {
		return nil, err
	}
	if !ev.GroupAllowed(d.GroupCode) {
		return nil, &model.FieldError{
			Field: "group",
			Msg:   fmt.Sprintf("组别「%s」不属于赛项「%s」", d.GroupCode, ev.Name),
		}
	}
	if _, err := s.ro().Seats.Get(ctx, d.SeatID); err != nil {
		return nil, err
	}

	slot := &model.Slot{
		SeatID: d.SeatID, Period: d.Period, TimeRange: d.TimeRange,
		EventID: d.EventID, GroupCode: d.GroupCode, Type: d.Type,
	}
	if err := s.ro().Slots.Create(ctx, slot); err != nil {
		return nil, err
	}
	return slot, nil
}

// DeleteSlot 删除场次。
func (s *Service) DeleteSlot(ctx context.Context, id int64, reason string) error {
	slot, err := s.ro().Slots.Get(ctx, id)
	if err != nil {
		return err
	}
	if reason == "" {
		reason = "场次调整"
	}
	return s.tx(ctx, func(r store.Repos) error {
		if err := r.Slots.Delete(ctx, id); err != nil {
			return err
		}
		return log(ctx, r, model.ActSeat, slotLabel(slot),
			describeSlot(slot), "已删除", reason)
	})
}

// AssignSlotTeams 手动改派场次队伍（运营现场调整的主入口）。
func (s *Service) AssignSlotTeams(ctx context.Context, slotID int64, teamIDs []int64, reason string) error {
	slot, err := s.ro().Slots.Get(ctx, slotID)
	if err != nil {
		return err
	}
	if slot.Type == model.SlotExtra {
		return &model.FieldError{
			Field: "slotId",
			Msg:   "加时赛场次的队伍以场内快照为准，不能从主库改派",
		}
	}
	if reason == "" {
		reason = "现场手动调整赛台分配"
	}
	before := describeTeamIDs(slot.TeamIDs)
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Slots.SetTeams(ctx, slotID, teamIDs); err != nil {
			return err
		}
		return log(ctx, r, model.ActSeat, slotLabel(slot), before, describeTeamIDs(teamIDs), reason)
	}); err != nil {
		return err
	}
	return nil
}

// AutoAssignSlot 对该场次所在「赛项 + 组别 + 时段」做就近自动分配。
//
// 算法（三条，都是为了现场能解释清楚）：
//
//  1. 找出同一「赛项 + 组别 + 时段」下的全部赛台（人多时一个组会占多张台）
//  2. 取该赛项该组别的全部在册队伍，**按编号升序**
//  3. 轮流（round-robin）铺到各张赛台 → 各台人数差不超过 1，顺序稳定
//
// 为什么用编号而不引入别的排序依据：真实赛制里应该以 WRC 导出的**叫号表**为序，
// 叫号表尚未接入（P6），编号序是当前唯一确定且可解释的顺序。
// 运营对结果不满意时可以直接手动改派（AssignSlotTeams）。
func (s *Service) AutoAssignSlot(ctx context.Context, slotID int64, reason string) ([]int64, error) {
	slot, err := s.ro().Slots.Get(ctx, slotID)
	if err != nil {
		return nil, err
	}
	if slot.Type == model.SlotExtra {
		return nil, &model.FieldError{Field: "slotId", Msg: "加时赛场次请直接导入场内快照"}
	}

	// 1. 同一时段下、承载同一赛项同一组别的全部赛台场次
	all, err := s.ro().Slots.List(ctx, 0, slot.EventID)
	if err != nil {
		return nil, err
	}
	var siblings []model.Slot
	for _, sl := range all {
		if sl.Type == model.SlotNormal && sl.Period == slot.Period &&
			sl.GroupCode == slot.GroupCode {
			siblings = append(siblings, sl)
		}
	}
	sort.Slice(siblings, func(i, j int) bool {
		if siblings[i].SeatID != siblings[j].SeatID {
			return siblings[i].SeatID < siblings[j].SeatID
		}
		return siblings[i].ID < siblings[j].ID
	})
	// 该场次自身必然满足筛选条件，因此不会为空；这里只是避免将来改筛选逻辑时
	// 出现 i % 0 的 panic。
	if len(siblings) == 0 {
		return nil, fmt.Errorf("场次 #%d 没有可用于分配的赛台", slotID)
	}

	// 2. 该赛项该组别的在册队伍，按编号升序
	teams, err := s.ro().Teams.ListByEvent(ctx, slot.EventID, false)
	if err != nil {
		return nil, err
	}
	var pool []model.Team
	for _, t := range teams {
		if t.GroupCode == slot.GroupCode {
			pool = append(pool, t)
		}
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].TeamNo < pool[j].TeamNo })

	// 3. 轮流铺到各张赛台
	buckets := make(map[int64][]int64, len(siblings))
	for i, t := range pool {
		target := siblings[i%len(siblings)]
		buckets[target.ID] = append(buckets[target.ID], t.ID)
	}

	if reason == "" {
		reason = fmt.Sprintf("按就近自动分配（%s %s，%d 支队伍 → %d 张赛台）",
			slot.EventID, slot.GroupCode, len(pool), len(siblings))
	}

	if err := s.tx(ctx, func(r store.Repos) error {
		for _, sl := range siblings {
			ids := buckets[sl.ID]
			if ids == nil {
				ids = []int64{}
			}
			if err := r.Slots.SetTeams(ctx, sl.ID, ids); err != nil {
				return err
			}
			if err := log(ctx, r, model.ActSeat, slotLabel(&sl),
				describeTeamIDs(sl.TeamIDs), describeTeamIDs(ids), reason); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return buckets[slotID], nil
}

// SaveSnapshot 写入加时赛场内快照。
//
// 依据需求确认单：「加时赛队伍以运营导入的为准，采用场内快照，**不污染主库**」。
// 这个方法只写 slot_snapshots 表，绝不碰 teams。
// 重复编号自动去重（保留首次），避免同一队在同一场次里出现两次。
func (s *Service) SaveSnapshot(ctx context.Context, slotID int64,
	snaps []model.Snapshot, reason string) ([]model.Snapshot, error) {

	slot, err := s.ro().Slots.Get(ctx, slotID)
	if err != nil {
		return nil, err
	}
	if slot.Type != model.SlotExtra {
		return nil, &model.FieldError{
			Field: "slotId",
			Msg:   "只有加时赛（独立场次）才能使用场内快照，正式场次的队伍来自主库",
		}
	}

	// 去重 + 校验：编号必填纯数字、队名必填
	seen := map[string]bool{}
	clean := make([]model.Snapshot, 0, len(snaps))
	for i := range snaps {
		sn := snaps[i]
		sn.TeamNo = strings.TrimSpace(sn.TeamNo)
		sn.Name = strings.TrimSpace(sn.Name)
		if sn.TeamNo == "" || !isAllDigits(sn.TeamNo) {
			return nil, &model.FieldError{Field: "no", Msg: "加时赛队伍编号必须为纯数字：" + sn.TeamNo}
		}
		if sn.Name == "" {
			return nil, &model.FieldError{Field: "name", Msg: "加时赛队伍名称不能为空（编号 " + sn.TeamNo + "）"}
		}
		if seen[sn.TeamNo] {
			continue
		}
		seen[sn.TeamNo] = true
		clean = append(clean, sn)
	}
	if reason == "" {
		reason = "加时赛队伍导入（场内快照，不入主库）"
	}

	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Snapshots.Save(ctx, slotID, clean); err != nil {
			return err
		}
		return log(ctx, r, model.ActSeat, slotLabel(slot),
			fmt.Sprintf("快照 %d 条", len(slot.Snapshot)),
			fmt.Sprintf("快照 %d 条：%s", len(clean), describeSnapshots(clean)),
			reason)
	}); err != nil {
		return nil, err
	}
	return clean, nil
}

// ListSnapshot 读取场次的场内快照。
func (s *Service) ListSnapshot(ctx context.Context, slotID int64) ([]model.Snapshot, error) {
	return s.ro().Snapshots.List(ctx, slotID)
}

// ---------------------------------------------------------------------------

func slotLabel(s *model.Slot) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("赛台#%d %s %s %s", s.SeatID, s.Period, s.EventID, s.GroupCode)
}

func describeSlot(s *model.Slot) string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("时段=%s(%s)；赛项=%s；组别=%s；类型=%s；队伍 %d 支",
		s.Period, s.TimeRange, s.EventID, s.GroupCode, s.Type.Display(), len(s.TeamIDs))
}

// describeTeamIDs 把队伍 ID 列表压成可读短串。
//
// 审计是给人看的：一整串 ID 没有价值，超过 8 支就只留数量与前几个。
func describeTeamIDs(ids []int64) string {
	if len(ids) == 0 {
		return "空"
	}
	parts := make([]string, 0, len(ids))
	for i, id := range ids {
		if i == 8 {
			parts = append(parts, fmt.Sprintf("…等 %d 支", len(ids)))
			break
		}
		parts = append(parts, fmt.Sprintf("#%d", id))
	}
	return strings.Join(parts, "、")
}

func describeSnapshots(snaps []model.Snapshot) string {
	parts := make([]string, 0, len(snaps))
	for i, sn := range snaps {
		if i == 6 {
			parts = append(parts, fmt.Sprintf("…等 %d 支", len(snaps)))
			break
		}
		parts = append(parts, sn.TeamNo+" "+sn.Name)
	}
	if len(parts) == 0 {
		return "空"
	}
	return strings.Join(parts, "、")
}
