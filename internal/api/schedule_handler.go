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
// 就近自动分配：把该「赛项 + 组别 + 时段」下的在册队伍按编号轮流铺到各张赛台，
// 各台人数差不超过 1。结果会写「调赛台」审计。
func (s *Server) handleAutoAssignSlot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	assigned, err := s.svc.AutoAssignSlot(r.Context(), id, queryReason(r))
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"slotId": id, "teamIds": assigned, "count": len(assigned)})
}

// handleAssignSlotTeams POST /api/v1/slots/{id}/teams
//
// 手动改派：运营对自动分配结果不满意时的直接干预手段。
func (s *Server) handleAssignSlotTeams(w http.ResponseWriter, r *http.Request) {
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
	if err := s.svc.AssignSlotTeams(r.Context(), id, body.TeamIDs, body.Reason); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"slotId": id, "teamIds": body.TeamIDs})
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
