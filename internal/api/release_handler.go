package api

import (
	"net/http"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 发布单元与移交 / 发布状态机（0006）
//
// P13 看板是本组接口的唯一「读」入口 —— 裁判长移交后不必回各 P11 单页逐一确认，
// 一眼就能看到整场还有哪些发布单元没发出去。
//
// 写操作按「谁在什么环节」分开，不做成一个泛用的 PATCH：
// 移交 / 接收 / 发布是三个不同角色在不同时刻的动作，合成一个接口
// 就得靠请求体里的枚举字段区分，反而把这个状态机藏起来了。
// ============================================================================

// handleListReleases GET /api/v1/releases
//
// P13 看板数据源：本赛事全部发布单元 + 四态分组计数。
func (s *Server) handleListReleases(w http.ResponseWriter, r *http.Request) {
	units, err := s.svc.ReleaseUnits(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}

	counts := map[string]int{
		string(model.ReleaseNotHanded): 0,
		string(model.ReleaseHanded):    0,
		string(model.ReleasePending):   0,
		string(model.ReleasePublished): 0,
	}
	republish := 0
	for _, u := range units {
		counts[string(u.Status)]++
		if u.RepublishRequired {
			republish++
		}
	}

	OK(w, map[string]any{
		"units":     units,
		"total":     len(units),
		"counts":    counts,
		"republish": republish,
	})
}

// handleEnsureRelease POST /api/v1/releases/ensure
//
// 请求体：{ "eventId": "...", "groupCode": "小学组", "seatId": 12 }
// 幂等：已存在则直接返回，不存在则创建。
func (s *Server) handleEnsureRelease(w http.ResponseWriter, r *http.Request) {
	ev, group, seatID, err := decodeReleaseKey(w, r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	u, err := s.svc.EnsureReleaseUnit(r.Context(), ev, group, seatID)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, u)
}

// handleHandOverRelease POST /api/v1/releases/hand-over
//
// 裁判长在 P11 点「确认并移交」。入口按（赛项, 组别, 赛台）定位而不是按 ID ——
// 裁判长面对的就是这个三元组。
func (s *Server) handleHandOverRelease(w http.ResponseWriter, r *http.Request) {
	ev, group, seatID, err := decodeReleaseKey(w, r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	u, err := s.svc.HandOverRelease(r.Context(), ev, group, seatID)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, u)
}

// handleReceiveRelease POST /api/v1/releases/{id}/receive
func (s *Server) handleReceiveRelease(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	u, err := s.svc.ReceiveRelease(r.Context(), id)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, u)
}

// handlePublishRelease POST /api/v1/releases/{id}/publish
//
// 运营按下发布键。未移交的单元会被拒绝（须先移交）。
func (s *Server) handlePublishRelease(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	u, err := s.svc.PublishRelease(r.Context(), id)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, u)
}

// handleMarkRepublish POST /api/v1/releases/{id}/republish
//
// 请求体：{ "reason": "改分生效，需重发" }
// 已发布后又有改分 / 裁定生效时调用；状态回退到待发布并置重发标记。
func (s *Server) handleMarkRepublish(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		Fail(w, r, err)
		return
	}
	if len([]rune(strings.TrimSpace(req.Reason))) < 2 {
		Fail(w, r, NewBadRequest("标记重发必须说明原因（至少 2 个字）"))
		return
	}
	u, err := s.svc.MarkRepublish(r.Context(), id, req.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, u)
}

// handleGetRelease GET /api/v1/releases/{id}
func (s *Server) handleGetRelease(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	u, err := s.svc.ReleaseUnit(r.Context(), id)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, u)
}

// decodeReleaseKey 解析（赛项, 组别, 赛台）三元组。
func decodeReleaseKey(w http.ResponseWriter, r *http.Request) (string, string, *int64, error) {
	var req struct {
		EventID   string `json:"eventId"`
		GroupCode string `json:"groupCode"`
		SeatID    *int64 `json:"seatId"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return "", "", nil, err
	}
	req.EventID = strings.TrimSpace(req.EventID)
	req.GroupCode = strings.TrimSpace(req.GroupCode)
	if req.EventID == "" || req.GroupCode == "" {
		return "", "", nil, NewBadRequest("eventId 与 groupCode 必填")
	}
	if req.SeatID != nil && *req.SeatID <= 0 {
		return "", "", nil, NewBadRequest("seatId 必须为正整数或省略")
	}
	return req.EventID, req.GroupCode, req.SeatID, nil
}
