package api

import (
	"net/http"

	"github.com/jialangli/comptition-score-server/internal/service"
)

// ============================================================================
// 报名导入
//
// 分两步：预览（不落库，返回四色清单）→ 提交（按勾选的行号入库）。
//
// 提交时**服务端会重新算一遍比对结果**，只采纳客户端勾选的行号。
// 这样即使预览与提交之间隔了几分钟、期间别人又导过一次，
// 也不会出现「按旧清单覆盖新数据」。
//
// 勾选粒度是**行号**而不是队伍编号：同一份文件里可能出现两条同编号的行
// （那正是「编号重复」这类冲突本身），用编号根本区分不开勾了哪一条。
// ============================================================================

// handleImportPreview POST /api/v1/imports/preview
//
// 不落库，只做比对。返回每一行的状态与字段级变化，供运营确认。
func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	var body importPreviewReq
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	preview, err := s.svc.PreviewImport(r.Context(), body.EventID, toImportRows(body.Rows))
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, preview)
}

// handleImportCommit POST /api/v1/imports/commit
//
// 入库并写导入审计 + 操作审计（与队伍写入同一事务）。
func (s *Server) handleImportCommit(w http.ResponseWriter, r *http.Request) {
	var body importPreviewReq
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	logEntry, err := s.svc.CommitImport(r.Context(), body.EventID,
		toImportRows(body.Rows), body.SelectedLines, body.Note)
	if err != nil {
		Fail(w, r, err)
		return
	}
	Created(w, logEntry)
}

// toImportRows 把请求体的行转成 service 入参。
//
// 顺手把行号补上：客户端不一定给 LineNo（前端是数组下标 + 1），
// 这里统一补齐，保证「预览返回的行号」与「提交时勾选的行号」是同一套口径。
func toImportRows(rows []importRowJSON) []service.ImportRow {
	out := make([]service.ImportRow, 0, len(rows))
	for i, r := range rows {
		line := r.LineNo
		if line <= 0 {
			line = i + 1
		}
		out = append(out, service.ImportRow{
			TeamNo:  r.No,
			Name:    r.Name,
			School:  r.School,
			Coach:   r.Coach,
			Group:   r.Group,
			Members: r.Members,
			LineNo:  line,
		})
	}
	return out
}
