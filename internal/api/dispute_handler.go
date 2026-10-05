package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 争议工单（0005）
//
// 对应前端 P8 家族。两个建单入口共用一张表，但**入口是分开的**：
//
//	POST /api/v1/disputes            人工上报（source=referee）
//	系统补传冲突                      由 /api/v1/sync 内部调用 service 建单，不开 HTTP 入口
//
// 「同步冲突」刻意不接受人工上报：它不是人能观察到的现象，
// 让人去提只会产生来源与事实不符的工单。
// ============================================================================

// handleListDisputes GET /api/v1/disputes
//
// 查询参数：
//
//	teamId=12   给了就返回该队全部历史工单（含已裁定 / 已撤回），按时间倒序
//	            不给则返回**待裁定队列**（P8 队列页数据源），先到先裁
func (s *Server) handleListDisputes(w http.ResponseWriter, r *http.Request) {
	if raw := strings.TrimSpace(r.URL.Query().Get("teamId")); raw != "" {
		teamID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || teamID <= 0 {
			Fail(w, r, NewBadRequest("teamId 必须为正整数，收到 "+raw))
			return
		}
		list, err := s.svc.TeamDisputes(r.Context(), teamID)
		if err != nil {
			Fail(w, r, err)
			return
		}
		OK(w, map[string]any{"disputes": list, "total": len(list)})
		return
	}

	list, err := s.svc.OpenDisputes(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"disputes": list, "total": len(list), "pending": len(list)})
}

// handleCreateDispute POST /api/v1/disputes
//
// 请求体：{ "teamId": 12, "roundNo": 1, "kind": "duplicate", "reason": "..." }
func (s *Server) handleCreateDispute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TeamID  int64  `json:"teamId"`
		RoundNo int    `json:"roundNo"`
		Kind    string `json:"kind"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		Fail(w, r, err)
		return
	}
	if req.TeamID <= 0 {
		Fail(w, r, NewBadRequest("teamId 必须为正整数"))
		return
	}
	if req.RoundNo != 1 && req.RoundNo != 2 {
		Fail(w, r, NewBadRequest("roundNo 只能是 1 或 2"))
		return
	}
	kind, err := parseDisputeKind(req.Kind)
	if err != nil {
		Fail(w, r, err)
		return
	}
	if kind == model.DisputeSync {
		Fail(w, r, NewBadRequest(
			"同步冲突工单由系统补传时自动建立，不接受人工上报；请改用 duplicate 或 other"))
		return
	}

	d, err := s.svc.ReportDispute(r.Context(), req.TeamID, req.RoundNo, kind, req.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	Created(w, d)
}

// handleGetDispute GET /api/v1/disputes/{id}
func (s *Server) handleGetDispute(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	d, err := s.svc.Dispute(r.Context(), id)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, d)
}

// handleDecideDispute POST /api/v1/disputes/{id}/decide
//
// 请求体：{ "verdict": "uphold", "reason": "..." }
//
// verdict 三档对应前端三个页面：
//
//	uphold     维持原判（P8a）
//	adjust     授权改分（P8b）
//	disqualify 取消资格（P8c）
//
// 允许对已裁定工单再执行一次（推翻原结论），每次都留痕 —— P8e 口径。
func (s *Server) handleDecideDispute(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	var req struct {
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		Fail(w, r, err)
		return
	}
	verdict := model.DisputeVerdict(strings.TrimSpace(req.Verdict))
	switch verdict {
	case model.VerdictUphold, model.VerdictAdjust, model.VerdictDisqualify:
	default:
		Fail(w, r, NewBadRequest(
			"verdict 必须是 uphold / adjust / disqualify，收到 "+req.Verdict))
		return
	}

	if err := s.svc.DecideDispute(r.Context(), id, verdict, req.Reason); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"id": id, "verdict": verdict, "verdictLabel": verdict.Label()})
}

// handleWithdrawDispute POST /api/v1/disputes/{id}/withdraw
//
// 上报人在裁判长裁定前撤回。已裁定的不可撤（只能再裁定一次）。
func (s *Server) handleWithdrawDispute(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	if err := s.svc.WithdrawDispute(r.Context(), id); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"id": id, "status": model.DisputeWithdrawn})
}

// handleListTeamDisputes GET /api/v1/teams/{id}/disputes
//
// 某队全部历史工单。成绩单页据此判断该队是否被判取消资格
// （前端在队名后标红「（成绩作废）」）。
func (s *Server) handleListTeamDisputes(w http.ResponseWriter, r *http.Request) {
	teamID, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	list, err := s.svc.TeamDisputes(r.Context(), teamID)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"disputes": list, "total": len(list)})
}

// parseDisputeKind 校验争议类型。
//
// 只接受迁移里 CHECK 约束列出的三个值 —— 在这里挡住，
// 比让数据库抛 23514 检查约束冲突后再翻译成 400 更早、也更可读。
func parseDisputeKind(raw string) (model.DisputeKind, error) {
	kind := model.DisputeKind(strings.TrimSpace(raw))
	switch kind {
	case model.DisputeDuplicate, model.DisputeSync, model.DisputeOther:
		return kind, nil
	}
	return "", NewBadRequest("kind 必须是 duplicate / sync_conflict / other，收到 " + raw)
}
