package api

import (
	"net/http"
	"time"

	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 审计
//
// 审计是「争议追溯」的唯一依据，因此这一组接口只读、不分页裁剪逻辑做得保守：
// 默认按时间倒序返回，允许按动作 / 操作人 / 审批人 / 时间区间筛选。
//
// 关于保留期：需求确认单要求 ≥2 年。本期不做自动归档，
// 数据一直留着（表上已有 created_at 索引）；归档策略留到运维阶段再定。
// ============================================================================

// handleListAuditLogs GET /api/v1/audit-logs
//
// 查询参数：
//
//	action=改分       按动作筛选（改配置/改分/弃赛/改组/调赛台/删队/导入队伍）
//	operator=运营A    按操作人筛选
//	approver=裁判长C  按审批人筛选（只有需授权的操作才有值）
//	since=2026-09-17T00:00:00+08:00 / until=...  按时间区间
//	limit=100&offset=0
func (s *Server) handleListAuditLogs(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 100)
	if err != nil {
		Fail(w, r, err)
		return
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		Fail(w, r, err)
		return
	}
	since, err := queryTime(r, "since")
	if err != nil {
		Fail(w, r, err)
		return
	}
	until, err := queryTime(r, "until")
	if err != nil {
		Fail(w, r, err)
		return
	}

	logs, err := s.svc.AuditLogs(r.Context(), store.AuditFilter{
		Action:   r.URL.Query().Get("action"),
		Operator: r.URL.Query().Get("operator"),
		Approver: r.URL.Query().Get("approver"),
		Since:    since,
		Until:    until,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"logs": logs, "total": len(logs)})
}

// handleAuditSummary GET /api/v1/audit-summary
//
// 返回「六类必留痕操作」的条数统计，用于验证留痕覆盖率。
// 同时把「必须留痕的动作清单」一起返回，避免前端硬编码那份清单。
func (s *Server) handleAuditSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := s.svc.AuditSummary(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}

	required := s.svc.RequiredAuditActions()
	missing := make([]string, 0)
	for _, action := range required {
		if summary[string(action)] == 0 {
			missing = append(missing, string(action))
		}
	}

	OK(w, map[string]any{
		"counts":         summary,
		"required":       required,
		"missingActions": missing,
	})
}

// handleListImportLogs GET /api/v1/import-logs?limit=50
func (s *Server) handleListImportLogs(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		Fail(w, r, err)
		return
	}
	logs, err := s.svc.ImportLogs(r.Context(), limit)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"logs": logs, "total": len(logs)})
}

// queryTime 解析 RFC3339 时间查询参数；空值返回 nil。
//
// 只接受带时区的 RFC3339：审计查询跨越「美国赛区 / 北京赛区」时，
// 一个不带时区的时间会静默按服务器本地时区解释并查出错误区间。
func queryTime(r *http.Request, name string) (*time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, NewBadRequest(name + " 必须是 RFC3339 时间（如 2026-09-17T10:00:00+08:00）")
	}
	return &t, nil
}
