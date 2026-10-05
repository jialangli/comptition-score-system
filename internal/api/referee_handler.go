package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 裁判码（0007）
//
// 三个端点对应三件事：赛前建档发码、后台查看、平板上激活。
//
// 激活是**唯一**面向平板端的入口，且刻意设计成幂等：
// 换平板、重装 App 都要能重新激活，否则裁判换台设备就再也登不进去。
// 激活之后凭据缓存在本机，断网登录由平板端判定，不再回后端 ——
// 所以这里不签发任何会话令牌。
// ============================================================================

// handleIssueRefereeCode POST /api/v1/referee-codes
//
// 请求体：{ "name": "张老师", "role": "chief", "eventId": "...", "groupCode": "小学组", "seatId": 12 }
// role 省略时为 referee（普通裁判）。
func (s *Server) handleIssueRefereeCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		EventID   string `json:"eventId"`
		GroupCode string `json:"groupCode"`
		SeatID    *int64 `json:"seatId"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		Fail(w, r, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		Fail(w, r, NewBadRequest("name 必填（裁判码是姓名 + 码双因子，缺一半不成立）"))
		return
	}
	if req.SeatID != nil && *req.SeatID <= 0 {
		Fail(w, r, NewBadRequest("seatId 必须为正整数或省略"))
		return
	}

	c, err := s.svc.IssueRefereeCode(r.Context(), req.Name,
		model.RefereeRole(strings.TrimSpace(req.Role)),
		strings.TrimSpace(req.EventID), strings.TrimSpace(req.GroupCode), req.SeatID)
	if err != nil {
		Fail(w, r, err)
		return
	}
	Created(w, map[string]any{
		"code": c, "roleLabel": c.Role.Label(), "statusLabel": c.Status.Label(),
	})
}

// handleListRefereeCodes GET /api/v1/referee-codes
//
// 本赛事全部裁判码（含激活状态）。注意这是**赛事级**凭证，
// 切到别的赛事就看不到这里的码 —— 旧码在新赛事里本就无效。
func (s *Server) handleListRefereeCodes(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.RefereeCodes(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}
	activated := 0
	for _, c := range list {
		if c.Status == model.RefereeActivated {
			activated++
		}
	}
	OK(w, map[string]any{"codes": list, "total": len(list), "activated": activated})
}

// handleActivateReferee POST /api/v1/referee-codes/activate
//
// 请求体：{ "code": "A2B3C4", "name": "张老师" }
//
// 失败按语义区分，前端据此分流到不同页面：
//
//	400 姓名与裁判码不匹配 → P1.5c
//	404 裁判码无效         → P1.5b
//	409 裁判码已作废
//
// 成功返回预绑的执裁范围（赛项 / 组别 / 赛台）与身份，平板端缓存后即可离线登录。
func (s *Server) handleActivateReferee(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		Fail(w, r, err)
		return
	}

	c, err := s.svc.ActivateReferee(r.Context(), req.Code, req.Name)
	if err != nil {
		Fail(w, r, err)
		return
	}

	// 赛项名只作展示用，查不到也不该让登录失败 —— 执裁范围以 ID 为准。
	eventName := c.EventID
	if ev, e := s.svc.GetEvent(r.Context(), c.EventID); e == nil && ev != nil {
		eventName = ev.Name
	}

	OK(w, map[string]any{
		"name":      c.Name,
		"role":      c.Role,
		"roleLabel": c.Role.Label(),
		"eventId":   c.EventID,
		"groupCode": c.GroupCode,
		"seatId":    c.SeatID,
		"scope":     c.ScopeText(eventName, seatLabel(c.SeatID)),
		"status":    c.Status,
	})
}

// seatLabel 赛台展示文案。
func seatLabel(seatID *int64) string {
	if seatID == nil {
		return ""
	}
	return "赛台 #" + strconv.FormatInt(*seatID, 10)
}
