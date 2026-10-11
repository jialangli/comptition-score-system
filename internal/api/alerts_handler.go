package api

import "net/http"

// ============================================================================
// 聚合告警（图集 12 缺口补齐）—— 只读派生
//
// demo「告警收件箱」的后端对应物：从改分申请 / 争议工单**现算**出待办、超时、
// 自批、取消资格四类告警，不新增存储、不留审计。告警与台账是同一份数据的
// 两种投影，天然不会「漏发 / 重复发」。
// ============================================================================

// handleListAlerts GET /api/v1/alerts
func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	rep, err := s.svc.Alerts(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, rep)
}
