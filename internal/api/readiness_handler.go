package api

import "net/http"

// ============================================================================
// 开赛就绪度（只读聚合）
//
// demo「总览」页 readinessOf / readinessItems 在后端的同一口径实现。
// 数据全部来自各业务表的既有事实，本端点**不新增任何存储、不写审计**；
// 唯一做的是把散在各域的计数拼成一份「能不能开赛」的结论，供非 demo
// 客户端（大屏看板 / 运营中台）与 demo 用同一个口径。
// ============================================================================

// handleReadiness GET /api/v1/readiness
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	rep, err := s.svc.Readiness(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, rep)
}
