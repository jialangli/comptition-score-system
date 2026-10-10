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
	// 归到该台的队伍会退回「未排台」（外键 SET NULL + 存储层顺位归零）——
	// 这是删台最容易被忽略的副作用，数量写进留痕：
	// 现场问「我删了台，那些队伍去哪了」时，审计要能直接回答，而不是让人去猜。
	onSeat, err := s.ro().Teams.ListBySeat(ctx, id)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(r store.Repos) error {
		if err := r.Seats.Delete(ctx, id); err != nil {
			return err
		}
		return log(ctx, r, model.ActSeat, "赛台 "+seat.Name,
			fmt.Sprintf("含 %d 个场次", len(slots)),
			fmt.Sprintf("已删除（%d 支归台队伍回到未排台）", len(onSeat)), reason)
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

// 手动改派场次队伍：**已废弃**（2026-10-10）。
//
// 场次队伍改为由「队伍级归台」派生之后，写入场次就等于造出第二套事实源 ——
// 两处不一致时谁也说不清以哪边为准。改派一律走 `PUT /teams/{id}/seat`（写归台）。
//
// 这里刻意**不保留同名方法**：留着它，下一个改动的人就会继续调用它，
// 而 API 层已经对该路由返回 410（见 api.handleAssignSlotTeams）。
//
// AutoAssignSlot 一键自动分台：把该场次所在「赛项 + 组别 + 时段」的队伍
// 按编号升序轮流铺到各张赛台 —— 写的是**队伍级归台**（`teams.seat_id` / `seat_order`），
// 不再是场次队伍。
//
// 算法（三条，都是为了现场能解释清楚）：
//
//  1. 找出同一「赛项 + 组别 + 时段」下的全部**正式场次**（人多时一个组会占多张台）
//  2. 取该赛项该组别的**在册**队伍，按**编号升序**（弃赛队不参与）
//  3. 轮流（round-robin）铺到各张赛台 → 各台人数差不超过 1，顺序稳定；
//     台内顺位按分配顺序**从 1 起**（顺位是「台内该组别的序号」：
//     派生按组别切分场次，同一张台上的不同组别互不干扰，不必跨组别续号）
//
// 为什么用编号而不引入别的排序依据：真实赛制应以 WRC 导出的**叫号表**为序，
// 叫号表尚未接入，编号序是当前唯一确定且可解释的顺序。
// 结果不满意时运营可逐个改派（PUT /teams/{id}/seat）。
//
// 副作用要说清：**只动本赛项本组别的队伍**；其中原本归在别处的会被重新落位。
// 返回实际分配的队伍数。
func (s *Service) AutoAssignSlot(ctx context.Context, slotID int64, reason string) (int, error) {
	slot, err := s.ro().Slots.Get(ctx, slotID)
	if err != nil {
		return 0, err
	}
	if slot.Type.Extra() {
		return 0, &model.FieldError{
			Field: "slotId",
			Msg:   slot.Type.Display() + "的队伍请直接导入场内快照，不参与自动分台",
		}
	}

	// 1. 同一赛项 + 组别 + 时段的全部正式场次（= 本组别在该时段用到的各张赛台）
	all, err := s.ro().Slots.List(ctx, 0, slot.EventID)
	if err != nil {
		return 0, err
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
		return 0, fmt.Errorf("场次 #%d 没有可用于分配的赛台", slotID)
	}

	// 2. 该赛项该组别的在册队伍，按编号升序
	teams, err := s.ro().Teams.ListByEvent(ctx, slot.EventID, false)
	if err != nil {
		return 0, err
	}
	var pool []model.Team
	for _, t := range teams {
		if t.GroupCode == slot.GroupCode {
			pool = append(pool, t)
		}
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].TeamNo < pool[j].TeamNo })
	if len(pool) == 0 {
		return 0, &model.FieldError{Field: "slotId", Msg: "该赛项该组别没有在册队伍"}
	}

	// 3. 轮流铺到各张赛台（记录每队的目标台与台内顺位）
	type target struct {
		seatID int64
		order  int
	}
	plan := make(map[int64]target, len(pool)) // teamID → 目标
	counts := make([]int, len(siblings))
	for i, t := range pool {
		idx := i % len(siblings)
		counts[idx]++
		plan[t.ID] = target{seatID: siblings[idx].SeatID, order: counts[idx]}
	}

	if reason == "" {
		parts := make([]string, 0, len(siblings))
		for i, sl := range siblings {
			parts = append(parts, fmt.Sprintf("赛台 #%d %d 队", sl.SeatID, counts[i]))
		}
		reason = fmt.Sprintf("按编号升序自动分台（%s %s，%d 支队伍 → %d 张赛台）",
			slot.EventID, slot.GroupCode, len(pool), len(siblings))
	}

	detail := describeSeatCounts(siblings, counts)
	if err := s.tx(ctx, func(r store.Repos) error {
		for i := range pool {
			tg := plan[pool[i].ID]
			seatID := tg.seatID
			if err := r.Teams.SetSeat(ctx, pool[i].ID, &seatID, tg.order); err != nil {
				return err
			}
		}
		return log(ctx, r, model.ActSeat, slotLabel(slot),
			fmt.Sprintf("%d 支队伍待分台", len(pool)), detail, reason)
	}); err != nil {
		return 0, err
	}
	return len(pool), nil
}

// SlotTeamRow 场次队伍的一行（派生读 / 快照读的结果）。
//
// Source 区分两种来源，前端据此决定「改派」是否可用：
//
//	main     正式场次：由**队伍级归台**派生 —— 队伍在队伍主库里有档案，可改派
//	snapshot 独立场次（加时赛 / 重赛）：来自**场内快照**，不写队伍主库，不可改派
type SlotTeamRow struct {
	TeamID int64  `json:"teamId,omitempty"`
	No     string `json:"no"`
	Name   string `json:"name"`
	School string `json:"school"`
	Coach  string `json:"coach,omitempty"`
	Group  string `json:"group,omitempty"`
	Order  int    `json:"order,omitempty"` // 台内顺位（正式场次）
	Source string `json:"source"`
}

// SlotTeamsResult 场次的队伍列表（连场次本身一起回，前端一次拿到全部上下文）。
type SlotTeamsResult struct {
	Slot   model.Slot    `json:"slot"`
	Teams  []SlotTeamRow `json:"teams"`
	Total  int           `json:"total"`
	Source string        `json:"source"` // main / snapshot
}

// SlotTeams 读取某场次的队伍 —— **派生**，不读 slot_teams。
//
// 平板端「本赛台队列」的数据源：场次 = 赛台 × 时段 × 赛项 × 组别 × 轮次，
// 场次的队伍列表恰好就是「这张台这个时段该上场的队伍」，顺序 = 台内顺位。
//
// 派生规则与 store 的 loadDerivedTeams 同源：直接用 `Slots.Get` 已填好的 `TeamIDs`
// （判据只写一处，两套 SQL 必然漂移），再按 ID 批量补详情、**按原顺序重排** ——
// 这个顺序就是现场叫号顺序，在补详情这一步丢了顺序，队列视图就白做了。
func (s *Service) SlotTeams(ctx context.Context, slotID int64) (*SlotTeamsResult, error) {
	slot, err := s.ro().Slots.Get(ctx, slotID)
	if err != nil {
		return nil, err
	}
	res := &SlotTeamsResult{Slot: *slot, Source: "main", Teams: []SlotTeamRow{}}
	if slot.Type.Extra() {
		res.Source = "snapshot"
		for _, sn := range slot.Snapshot {
			res.Teams = append(res.Teams, SlotTeamRow{
				No: sn.TeamNo, Name: sn.Name, School: sn.School,
				Coach: sn.Coach, Source: "snapshot",
			})
		}
		res.Total = len(res.Teams)
		return res, nil
	}
	if len(slot.TeamIDs) == 0 {
		return res, nil
	}
	teams, err := s.ro().Teams.ListByIDs(ctx, slot.TeamIDs)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]model.Team, len(teams))
	for _, t := range teams {
		byID[t.ID] = t
	}
	for _, id := range slot.TeamIDs {
		t, ok := byID[id]
		if !ok {
			continue // 派生与补详情之间队伍被删了：跳过，不塞一行空的
		}
		res.Teams = append(res.Teams, SlotTeamRow{
			TeamID: t.ID, No: t.TeamNo, Name: t.Name, School: t.School,
			Coach: t.Coach, Group: t.GroupCode, Order: t.SeatOrder, Source: "main",
		})
	}
	res.Total = len(res.Teams)
	return res, nil
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
	if !slot.Type.Extra() {
		return nil, &model.FieldError{
			Field: "slotId",
			Msg:   "只有独立场次（加时赛 / 重赛）才能使用场内快照，正式场次的队伍由队伍级归台派生",
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

// describeSeatCounts 把「各赛台分到几队」压成留痕用的短串。
func describeSeatCounts(slots []model.Slot, counts []int) string {
	parts := make([]string, 0, len(slots))
	for i, sl := range slots {
		parts = append(parts, fmt.Sprintf("赛台 #%d：%d 队", sl.SeatID, counts[i]))
	}
	return strings.Join(parts, "；")
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
