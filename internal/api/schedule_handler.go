package api

import (
	"net/http"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 赛台与赛程
//
// 场次有两条互斥的「队伍来源」路径，接口也刻意不合并：
//
//	正式场次（normal）→ POST /slots/{id}/teams   从队伍主库改派
//	加时赛（extra）  → POST /slots/{id}/snapshot  写场内快照，不碰主库
//
// 两条路各自校验对方的场次类型，走错会得到 400 而不是悄悄写脏数据。
// ============================================================================

// handleListSeats GET /api/v1/seats
func (s *Server) handleListSeats(w http.ResponseWriter, r *http.Request) {
	seats, err := s.svc.ListSeats(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"seats": seats, "total": len(seats)})
}

// handleCreateSeat POST /api/v1/seats
func (s *Server) handleCreateSeat(w http.ResponseWriter, r *http.Request) {
	var body seatBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	seat, err := s.svc.CreateSeat(r.Context(), body.Name, body.SortOrder)
	if err != nil {
		Fail(w, r, err)
		return
	}
	Created(w, seat)
}

// handleUpdateSeat PUT /api/v1/seats/{id}
func (s *Server) handleUpdateSeat(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	var body seatBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	seat, err := s.svc.UpdateSeat(r.Context(), id, body.Name, body.SortOrder)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, seat)
}

// handleDeleteSeat DELETE /api/v1/seats/{id}
//
// 赛台下还有场次时必须说明原因（会连带清掉这些场次）。
func (s *Server) handleDeleteSeat(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	reason := queryReason(r)
	if err := s.svc.DeleteSeat(r.Context(), id, reason); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"deleted": id})
}

// handleListSlots GET /api/v1/slots?seat=&event=
func (s *Server) handleListSlots(w http.ResponseWriter, r *http.Request) {
	seatID := int64(0)
	if raw := r.URL.Query().Get("seat"); raw != "" {
		v, err := queryInt(r, "seat", 0)
		if err != nil {
			Fail(w, r, err)
			return
		}
		seatID = int64(v)
	}
	slots, err := s.svc.ListSlots(r.Context(), seatID, r.URL.Query().Get("event"))
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"slots": slots, "total": len(slots)})
}

// handleCreateSlot POST /api/v1/slots
func (s *Server) handleCreateSlot(w http.ResponseWriter, r *http.Request) {
	var body slotBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	slot, err := s.svc.CreateSlot(r.Context(), model.SlotDraft{
		SeatID:    body.SeatID,
		Period:    body.Period,
		TimeRange: body.TimeRange,
		EventID:   body.EventID,
		GroupCode: body.Group,
		Type:      model.SlotType(body.Type),
		RoundNo:   body.Round,
	})
	if err != nil {
		Fail(w, r, err)
		return
	}
	Created(w, slot)
}

// handleDeleteSlot DELETE /api/v1/slots/{id}
func (s *Server) handleDeleteSlot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	if err := s.svc.DeleteSlot(r.Context(), id, queryReason(r)); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"deleted": id})
}

// handleAutoAssignSlot POST /api/v1/slots/{id}/auto-assign
//
// 一键自动分台：把该「赛项 + 组别 + 时段」下的在册队伍按编号升序轮流铺到各张赛台，
// 各台人数差不超过 1。结果会写「调赛台」审计。
//
// **写的是队伍级归台**（`teams.seat_id` / `seat_order`），不再是场次队伍 ——
// 场次队伍由「赛台 × 赛项 × 组别 × 轮次」派生（见 GET /slots/{id}/teams）。
func (s *Server) handleAutoAssignSlot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	count, err := s.svc.AutoAssignSlot(r.Context(), id, queryReason(r))
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"slotId": id, "count": count})
}

// handleListSlotTeams GET /api/v1/slots/{id}/teams
//
// **派生读**：正式场次返回由队伍级归台派生出的队伍（按台内顺位排序）；
// 独立场次（加时赛 / 重赛）返回场内快照。
//
// 这就是平板端「本赛台队列」的权威数据源 —— 场次 = 赛台 × 时段 × 赛项 × 组别 × 轮次，
// 一个场次的队伍列表恰好就是「这张台这个时段该上场的队伍」。
func (s *Server) handleListSlotTeams(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	detail, err := s.svc.SlotTeams(r.Context(), id)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, detail)
}

// handleAssignSlotTeams POST /api/v1/slots/{id}/teams —— **已废弃（410 Gone）**。
//
// 场次队伍改为派生之后，写入场次就等于造出第二套事实源（与「队伍级归台」打架）。
// 改派一律走 `PUT /api/v1/teams/{id}/seat`。
//
// 保留路由并明确回 410 + 替代路径，而不是直接删掉：老客户端（现场平板 / 缓存过的页面）
// 拿到的是可读指引，而不是一个容易被误读成「服务没部署对」的 404。
func (s *Server) handleAssignSlotTeams(w http.ResponseWriter, r *http.Request) {
	Fail(w, r, NewGone("该接口已废弃：场次队伍现在由「队伍级归台」派生，"+
		"改派请用 PUT /api/v1/teams/{id}/seat（写 seatId 与 seatOrder）；"+
		"只读场次队伍请用 GET /api/v1/slots/{id}/teams"))
}

// handleListSnapshot GET /api/v1/slots/{id}/snapshot
func (s *Server) handleListSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	snaps, err := s.svc.ListSnapshot(r.Context(), id)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"snapshot": snaps, "total": len(snaps)})
}

// handleSaveSnapshot POST /api/v1/slots/{id}/snapshot
//
// 仅加时赛（extra）场次可用。数据只落 slot_snapshots，**绝不写入队伍主库**。
func (s *Server) handleSaveSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	var body slotBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	snaps := make([]model.Snapshot, 0, len(body.Snapshot))
	for _, sn := range body.Snapshot {
		snaps = append(snaps, model.Snapshot{
			TeamNo: sn.No, Name: sn.Name, School: sn.School, Coach: sn.Coach,
		})
	}
	saved, err := s.svc.SaveSnapshot(r.Context(), id, snaps, body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"slotId": id, "snapshot": saved, "total": len(saved)})
}

// queryReason 从查询参数里取原因（DELETE 请求没有请求体时的写法）。
func queryReason(r *http.Request) string {
	return r.URL.Query().Get("reason")
}
