package api

import (
	"net/http"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 赛项与配置快照
//
// 设计取舍：请求体直接复用 model.Event（而不是另建一套 DTO）。
// 理由是本项目的字段名从一开始就与前端 Schema v1 逐字对齐，
// 再包一层 DTO 只会引入「两处字段名需要同步维护」这个长期负担。
// 代价是客户端不能多传未知字段（decodeJSON 开了 DisallowUnknownFields），
// 这恰好是我们想要的：字段拼错会 400，而不是被静默丢掉。
// ============================================================================

// updateEventBody 修改赛项配置的请求体。
type updateEventBody struct {
	model.Event
	// Reason 改动原因，会写进审计。留空时 service 会给一个默认说明。
	Reason string `json:"reason"`
}

// handleListEvents GET /api/v1/events
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.svc.ListEvents(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"events": events, "total": len(events)})
}

// handleCreateEvent POST /api/v1/events
//
// 校验不通过时返回 400，且 data 里带完整校验结论（error + warning），
// 让界面一次把所有问题标出来。
func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	var ev model.Event
	if err := decodeJSON(w, r, &ev); err != nil {
		Fail(w, r, err)
		return
	}
	created, err := s.svc.CreateEvent(r.Context(), &ev)
	if err != nil {
		Fail(w, r, err)
		return
	}
	Created(w, created)
}

// handleGetEvent GET /api/v1/events/{id}
func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	ev, err := s.svc.GetEvent(r.Context(), pathStr(r, "id"))
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, ev)
}

// handleUpdateEvent PUT /api/v1/events/{id}
//
// 路径里的 id 优先于请求体里的 id —— 允许客户端不带 id 只传配置。
func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	id := pathStr(r, "id")
	var body updateEventBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	body.ID = id
	updated, err := s.svc.UpdateEvent(r.Context(), &body.Event, body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, updated)
}

// handleDeleteEvent DELETE /api/v1/events/{id}
//
// 被队伍或场次引用时返回 409（数据库外键 RESTRICT 兜底）——
// 带成绩的赛项不允许一键消失。
func (s *Server) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteEvent(r.Context(), pathStr(r, "id")); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"deleted": pathStr(r, "id")})
}

// handleValidateEvent POST /api/v1/events/{id}/validate
//
// 两种用法：
//
//	带请求体 → 校验**请求体里的配置**（保存前预检，配着表单一起提交）
//	空请求体 → 校验**库里已存的配置**（巡检「当前这版配置有没有问题」）
//
// 只有校验结论、不写库，所以永远返回 200；有 error 时看 data.errors。
func (s *Server) handleValidateEvent(w http.ResponseWriter, r *http.Request) {
	var ev *model.Event
	if r.ContentLength == 0 {
		stored, err := s.svc.GetEvent(r.Context(), pathStr(r, "id"))
		if err != nil {
			Fail(w, r, err)
			return
		}
		ev = stored
	} else {
		var body updateEventBody
		if err := decodeJSON(w, r, &body); err != nil {
			Fail(w, r, err)
			return
		}
		body.ID = pathStr(r, "id")
		ev = &body.Event
	}

	res := s.svc.ValidateConfig(ev)
	OK(w, map[string]any{
		"ok":       res.OK(),
		"errors":   res.Errors,
		"warnings": res.Warnings,
	})
}

// ---------------------------------------------------------------------------
// 配置快照
// ---------------------------------------------------------------------------

// handleListSnapshots GET /api/v1/config-snapshots
func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 20)
	if err != nil {
		Fail(w, r, err)
		return
	}
	snaps, err := s.svc.ListConfigSnapshots(r.Context(), limit)
	if err != nil {
		Fail(w, r, err)
		return
	}
	// payload 体积可能较大（含全部赛项配置），列表接口只给元信息，
	// 需要回滚时由 restore 端点自己去读那一份。
	type meta struct {
		ID        int64  `json:"id"`
		Note      string `json:"note"`
		CreatedAt string `json:"createdAt"`
	}
	out := make([]meta, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, meta{ID: sn.ID, Note: sn.Note, CreatedAt: sn.CreatedAt.Format("2006-01-02 15:04:05")})
	}
	OK(w, map[string]any{"snapshots": out, "total": len(out)})
}

// handleCreateSnapshot POST /api/v1/config-snapshots
func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	var body reasonBody
	// 快照允许空请求体（不写备注）
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &body); err != nil {
			Fail(w, r, err)
			return
		}
	}
	id, err := s.svc.CreateConfigSnapshot(r.Context(), body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	Created(w, map[string]any{"id": id})
}

// handleRestoreSnapshot POST /api/v1/config-snapshots/{sid}/restore
func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	sid, err := pathInt64(r, "sid")
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
	if err := s.svc.RestoreConfigSnapshot(r.Context(), sid, body.Reason); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"restored": sid})
}
