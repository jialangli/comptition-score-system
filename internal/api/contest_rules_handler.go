package api

import (
	"net/http"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 赛事级规则（0019）
//
// 挂在顶层 `/contest/rules` 而不是 `/events/{id}/rules`：
// 作用域是**赛事**（一个赛事一份），而 {id} 在别的路由里一律指**赛项** ——
// 放在赛项路径下会让人以为递补规则是每个赛项单独配的。
// ============================================================================

// handleGetContestRules GET /api/v1/contest/rules
//
// 未设置过时返回**默认口径**（不递补）而不是 404：界面需要永远能渲染出
// 「当前生效的口径是什么」，运营不该先"创建配置"才能看到它。
func (s *Server) handleGetContestRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.svc.ContestRules(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, rules)
}

// handleUpdateContestRules PUT /api/v1/contest/rules
//
// 切换递补规则会直接改变公示名次（被裁定取消资格的队伍留下的位置补不补），
// 因此走事务 + 留痕（Service 内做）。同值重复提交会短路，不刷台账。
func (s *Server) handleUpdateContestRules(w http.ResponseWriter, r *http.Request) {
	var body contestRulesBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	rules, err := s.svc.SetSubstituteMode(
		r.Context(), model.SubstituteMode(body.SubstituteMode), body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, rules)
}
