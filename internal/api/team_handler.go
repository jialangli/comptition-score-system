package api

import (
	"net/http"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 队伍
//
// 两条边界由 service 与数据库共同保证，本层只负责把它们翻译成正确的状态码：
//
//	编号重复        → 409（唯一索引 ux_teams_event_no）
//	删除带成绩队伍  → 409（外键 RESTRICT）
//	缺原因          → 400（弃赛 / 改组 / 删队必须说明原因）
// ============================================================================

// handleListTeams GET /api/v1/events/{id}/teams?withdrawn=true
//
// withdrawn 默认 false：打分、排名、大屏都不该看到弃赛队伍。
// 队伍管理页显式传 true 才带出来。
func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	includeWithdrawn := queryBool(r, "withdrawn", false)
	teams, err := s.svc.ListTeams(r.Context(), pathStr(r, "id"), includeWithdrawn)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"teams": teams, "total": len(teams)})
}

// handleCreateTeam POST /api/v1/events/{id}/teams
func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	var body createTeamBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	team, err := s.svc.CreateTeam(r.Context(), model.TeamDraft{
		EventID:   pathStr(r, "id"),
		TeamNo:    body.No,
		Name:      body.Name,
		School:    body.School,
		Coach:     body.Coach,
		GroupCode: body.Group,
		Members:   body.Members,
		Source:    model.SourceManual,
	})
	if err != nil {
		Fail(w, r, err)
		return
	}
	Created(w, team)
}

// handleGetTeam GET /api/v1/teams/{id}
func (s *Server) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	team, err := s.svc.GetTeam(r.Context(), id)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, team)
}

// handleUpdateTeam PUT /api/v1/teams/{id}
//
// 语义是整体覆盖：未提供的字段会被清空（见 service.UpdateTeam 的说明）。
// 组别变化需要 reason，且会被记为「改组」而不是「改配置」。
func (s *Server) handleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	var body updateTeamBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	updated, err := s.svc.UpdateTeam(r.Context(), id, model.Team{
		Name:      body.Name,
		School:    body.School,
		Coach:     body.Coach,
		GroupCode: body.Group,
		Members:   body.Members,
	}, body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, updated)
}

// handleDeleteTeam DELETE /api/v1/teams/{id}
func (s *Server) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	// DELETE 请求体不是标准用法，但这里需要「原因」这个必填项。
	// 允许两种等价写法：请求体 {"reason": "..."} 或查询参数 ?reason=...
	var body reasonBody
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body); err != nil {
			Fail(w, r, err)
			return
		}
	}
	if body.Reason == "" {
		body.Reason = r.URL.Query().Get("reason")
	}
	if err := s.svc.DeleteTeam(r.Context(), id, body.Reason); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"deleted": id})
}

// handleWithdrawTeam POST /api/v1/teams/{id}/withdraw
//
// 弃赛 = 软删除。必须说明原因：它直接改变排名与奖项归属。
func (s *Server) handleWithdrawTeam(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	var body reasonBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	team, err := s.svc.WithdrawTeam(r.Context(), id, body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, team)
}

// handleRestoreTeam POST /api/v1/teams/{id}/restore
func (s *Server) handleRestoreTeam(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	var body reasonBody
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body); err != nil {
			Fail(w, r, err)
			return
		}
	}
	team, err := s.svc.RestoreTeam(r.Context(), id, body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, team)
}
