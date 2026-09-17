package api

import (
	"net/http"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// ============================================================================
// 榜单与大屏
//
// 榜单按**组别**分组返回，而不是一张混合大表 —— 公示表的样例明确
// 「小学组与中学组各自有冠军/亚军/季军」，名次与奖项都是组内独立的。
//
// 大屏接口返回的姓名已在后端脱敏。这不是「顺手做的」：
// 大屏的入口不止一个，前端只要有一处忘了调用脱敏函数就会泄露未成年人真名，
// 因此把脱敏放在唯一的出口上。
// ============================================================================

// handleStandings GET /api/v1/events/{id}/standings
//
// 查询参数：
//
//	group=小学组        只看某个组别（默认返回全部组别）
//	signed=1           只统计已签字轮次（**正式公示前应开启**）
//	awardComplete=1    只给完成全部任务录入的队伍发奖（**正式公示前应开启**）
//	withdrawn=1        把弃赛队伍也计入（默认不计）
func (s *Server) handleStandings(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.Standings(r.Context(), pathStr(r, "id"), service.StandingsOptions{
		Group:             r.URL.Query().Get("group"),
		IncludeWithdrawn:  queryBool(r, "withdrawn", false),
		OnlySigned:        queryBool(r, "signed", false),
		AwardOnlyComplete: queryBool(r, "awardComplete", false),
	})
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, res)
}

// handleScreenPage GET /api/v1/screen/{eventId}?page=1
//
// 返回一屏数据（默认每屏 10 条）与分页元信息。前端按 5 秒轮询即可，
// 不做 WebSocket 推送 —— 轮询在这个规模下够用且实现简单。
func (s *Server) handleScreenPage(w http.ResponseWriter, r *http.Request) {
	page, err := queryInt(r, "page", 1)
	if err != nil {
		Fail(w, r, err)
		return
	}
	data, err := s.svc.ScreenPage(r.Context(), pathStr(r, "eventId"), page)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, data)
}

// handleGetScreenConfig GET /api/v1/screen/{eventId}/config
//
// 未配置过时返回默认值（每屏 10 条 / 停留 60 秒），不返回 404 ——
// 大屏页面永远要能渲染。
func (s *Server) handleGetScreenConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.svc.GetScreenConfig(r.Context(), pathStr(r, "eventId"))
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, cfg)
}

// handleUpdateScreenConfig PUT /api/v1/screen/{eventId}/config
//
// pinned 非空 = 运营「置顶 / 锁定本场」，前端据此停止轮播。
// 锁定与解除锁定都会留痕 —— 现场「屏幕被谁定住了」必须能被回答。
func (s *Server) handleUpdateScreenConfig(w http.ResponseWriter, r *http.Request) {
	var body screenConfigBody
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	cfg, err := s.svc.UpdateScreenConfig(r.Context(), &model.ScreenConfig{
		EventID:     pathStr(r, "eventId"),
		PageSize:    body.PageSize,
		IntervalSec: body.IntervalSec,
		Pinned:      body.Pinned,
	}, body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, cfg)
}
