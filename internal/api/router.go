package api

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/jialangli/comptition-score-server/internal/config"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/web"
)

// Server 持有所有 handler 的依赖。
//
// 只持有 service，不持有 store —— 架构铁律：api 不直接调 store。
// 这样「所有写入都经过审计埋点」就不是靠自觉，而是没有别的入口。
type Server struct {
	svc     *service.Service
	cfg     *config.Config
	startAt time.Time
}

// New 构造 API Server。
func New(svc *service.Service, cfg *config.Config) *Server {
	return &Server{svc: svc, cfg: cfg, startAt: time.Now()}
}

// Routes 注册全部路由并套上中间件链。
//
// 使用 Go 1.22+ ServeMux 的「方法 + 路径」模式（如 "GET /api/v1/healthz"），
// 路径参数用 {id} 语法，不引入第三方路由库。
//
// 中间件顺序：Recover（最外层，兜住一切 panic）→ 日志 → 操作者 → CORS。
//
// 端点清单刻意集中在这一个函数里（而不散落在各 handler 文件的 init 中），
// 目的是让「系统对外暴露了哪些能力」一眼可见 —— 评审接口权限时不必翻遍代码。
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// ---------------------------------------------------------------------
	// 健康检查
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/healthz", s.handleHealth)

	// ---------------------------------------------------------------------
	// 赛项与配置快照
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/events", s.handleListEvents)
	mux.HandleFunc("POST /api/v1/events", s.handleCreateEvent)
	mux.HandleFunc("GET /api/v1/events/{id}", s.handleGetEvent)
	mux.HandleFunc("PUT /api/v1/events/{id}", s.handleUpdateEvent)
	mux.HandleFunc("DELETE /api/v1/events/{id}", s.handleDeleteEvent)
	mux.HandleFunc("POST /api/v1/events/{id}/validate", s.handleValidateEvent)
	mux.HandleFunc("GET /api/v1/events/{id}/standings", s.handleStandings)

	// 配置快照是**全局**的（一次快照覆盖全部赛项配置），因此挂在顶层路径下，
	// 而不是嵌在 /events/{id} 里 —— 后者会让人以为快照是赛项级的。
	mux.HandleFunc("GET /api/v1/config-snapshots", s.handleListSnapshots)
	mux.HandleFunc("POST /api/v1/config-snapshots", s.handleCreateSnapshot)
	mux.HandleFunc("POST /api/v1/config-snapshots/{sid}/restore", s.handleRestoreSnapshot)

	// ---------------------------------------------------------------------
	// 队伍
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/events/{id}/teams", s.handleListTeams)
	mux.HandleFunc("POST /api/v1/events/{id}/teams", s.handleCreateTeam)
	mux.HandleFunc("GET /api/v1/teams/{id}", s.handleGetTeam)
	mux.HandleFunc("PUT /api/v1/teams/{id}", s.handleUpdateTeam)
	mux.HandleFunc("PUT /api/v1/teams/{id}/seat", s.handleAssignTeamSeat)
	mux.HandleFunc("PUT /api/v1/teams/{id}/session", s.handleSetTeamSession)
	mux.HandleFunc("DELETE /api/v1/teams/{id}", s.handleDeleteTeam)
	mux.HandleFunc("POST /api/v1/teams/{id}/withdraw", s.handleWithdrawTeam)
	mux.HandleFunc("POST /api/v1/teams/{id}/restore", s.handleRestoreTeam)

	// ---------------------------------------------------------------------
	// 打分与改分
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/teams/{id}/scores", s.handleListScores)
	mux.HandleFunc("GET /api/v1/teams/{id}/scores/{round}", s.handleGetScore)
	mux.HandleFunc("PUT /api/v1/teams/{id}/scores/{round}", s.handleSaveScore)
	mux.HandleFunc("POST /api/v1/teams/{id}/scores/{round}/change-requests", s.handleScoreChangeRequest)
	mux.HandleFunc("POST /api/v1/teams/{id}/scores/{round}/apply-change", s.handleApplyScoreChange)

	// ---------------------------------------------------------------------
	// 报名导入
	// ---------------------------------------------------------------------
	mux.HandleFunc("POST /api/v1/imports/preview", s.handleImportPreview)
	mux.HandleFunc("POST /api/v1/imports/commit", s.handleImportCommit)
	mux.HandleFunc("GET /api/v1/import-logs", s.handleListImportLogs)

	// ---------------------------------------------------------------------
	// 赛台与赛程
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/seats", s.handleListSeats)
	mux.HandleFunc("POST /api/v1/seats", s.handleCreateSeat)
	mux.HandleFunc("PUT /api/v1/seats/{id}", s.handleUpdateSeat)
	mux.HandleFunc("DELETE /api/v1/seats/{id}", s.handleDeleteSeat)

	mux.HandleFunc("GET /api/v1/slots", s.handleListSlots)
	mux.HandleFunc("POST /api/v1/slots", s.handleCreateSlot)
	mux.HandleFunc("DELETE /api/v1/slots/{id}", s.handleDeleteSlot)
	mux.HandleFunc("POST /api/v1/slots/{id}/auto-assign", s.handleAutoAssignSlot)
	// 场次队伍：只读派生（本赛台队列）。写入路径已废弃 → 410。
	mux.HandleFunc("GET /api/v1/slots/{id}/teams", s.handleListSlotTeams)
	mux.HandleFunc("POST /api/v1/slots/{id}/teams", s.handleAssignSlotTeams)
	mux.HandleFunc("GET /api/v1/slots/{id}/snapshot", s.handleListSnapshot)
	mux.HandleFunc("POST /api/v1/slots/{id}/snapshot", s.handleSaveSnapshot)

	// ---------------------------------------------------------------------
	// 赛事级规则（0019：名次编排 / 递补）
	//
	// 挂在顶层 /contest 下而不是 /events/{id} 下：作用域是**赛事**。
	// 现有路由里 {id} 一律指赛项，混在一起会让人以为递补规则是逐赛项配的。
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/contest/rules", s.handleGetContestRules)
	mux.HandleFunc("PUT /api/v1/contest/rules", s.handleUpdateContestRules)

	// ---------------------------------------------------------------------
	// 大屏（返回的姓名已脱敏）
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/screen/{eventId}", s.handleScreenPage)
	mux.HandleFunc("GET /api/v1/screen/{eventId}/config", s.handleGetScreenConfig)
	mux.HandleFunc("PUT /api/v1/screen/{eventId}/config", s.handleUpdateScreenConfig)

	// ---------------------------------------------------------------------
	// 审计
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/audit-logs", s.handleListAuditLogs)
	mux.HandleFunc("GET /api/v1/audit-summary", s.handleAuditSummary)

	// ---------------------------------------------------------------------
	// 争议工单（0005 · 前端 P8 家族）
	//
	// 队列与裁定分两层：列表页只排队，裁定动作挂在单张工单下。
	// 「同步冲突」不开 POST 入口 —— 它由 /api/v1/sync 在补传撞车时自动建立。
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/disputes", s.handleListDisputes)
	mux.HandleFunc("POST /api/v1/disputes", s.handleCreateDispute)
	mux.HandleFunc("GET /api/v1/disputes/{id}", s.handleGetDispute)
	mux.HandleFunc("POST /api/v1/disputes/{id}/decide", s.handleDecideDispute)
	mux.HandleFunc("POST /api/v1/disputes/{id}/withdraw", s.handleWithdrawDispute)
	mux.HandleFunc("GET /api/v1/teams/{id}/disputes", s.handleListTeamDisputes)

	// ---------------------------------------------------------------------
	// 发布单元与移交 / 发布状态机（0006 · 前端 P11 / P13）
	//
	// ensure 与 hand-over 按（赛项, 组别, 赛台）定位（裁判长在 P11 面对的就是这个
	// 三元组），接收 / 发布 / 标记重发按单元 ID —— 后者发生在后台队列里，
	// 运营手里已经有单元 ID，不需要再拼三元组。
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/releases", s.handleListReleases)
	mux.HandleFunc("GET /api/v1/releases/{id}", s.handleGetRelease)
	mux.HandleFunc("POST /api/v1/releases/ensure", s.handleEnsureRelease)
	mux.HandleFunc("POST /api/v1/releases/hand-over", s.handleHandOverRelease)
	mux.HandleFunc("POST /api/v1/releases/{id}/receive", s.handleReceiveRelease)
	mux.HandleFunc("POST /api/v1/releases/{id}/publish", s.handlePublishRelease)
	mux.HandleFunc("POST /api/v1/releases/{id}/republish", s.handleMarkRepublish)

	// ---------------------------------------------------------------------
	// 裁判码（0007 · 前端 P1 家族）
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/referee-codes", s.handleListRefereeCodes)
	mux.HandleFunc("POST /api/v1/referee-codes", s.handleIssueRefereeCode)
	mux.HandleFunc("POST /api/v1/referee-codes/activate", s.handleActivateReferee)

	// ---------------------------------------------------------------------
	// 赛台-队伍可写锁（0008）
	//
	// 抢不到锁返回 200 + writable=false，不是 4xx —— 见 lock_handler.go 的说明。
	// ---------------------------------------------------------------------
	mux.HandleFunc("POST /api/v1/locks/acquire", s.handleAcquireLock)
	mux.HandleFunc("GET /api/v1/locks", s.handleCheckLock)
	mux.HandleFunc("POST /api/v1/locks/release", s.handleReleaseLock)
	mux.HandleFunc("POST /api/v1/locks/force-release", s.handleForceReleaseLock)

	// ---------------------------------------------------------------------
	// 留底证据库（0009）
	// ---------------------------------------------------------------------
	mux.HandleFunc("GET /api/v1/evidence", s.handleListEvidence)
	mux.HandleFunc("POST /api/v1/evidence", s.handleRecordEvidence)
	mux.HandleFunc("GET /api/v1/evidence/pending", s.handleListPendingEvidence)
	mux.HandleFunc("GET /api/v1/evidence/complete", s.handleEvidenceComplete)
	mux.HandleFunc("POST /api/v1/evidence/{id}/synced", s.handleMarkEvidenceSynced)

	// ---------------------------------------------------------------------
	// 申述书照片（选手手写 · 裁判拍照上传）
	//
	// 复用 evidence 表的 appeal 类目：POST 落盘+登记，GET 按争议单取图、按证据 ID 出图。
	// 同一张照片经 dispute_id 挂争议工单、经改分单 appeal_evidence_id 挂改分单（两处都挂）。
	// ---------------------------------------------------------------------
	mux.HandleFunc("POST /api/v1/appeals/upload", s.handleUploadAppeal)
	mux.HandleFunc("GET /api/v1/appeals", s.handleListAppeals)
	mux.HandleFunc("GET /api/v1/appeals/{id}/file", s.handleGetAppealFile)

	// ---------------------------------------------------------------------
	// 离线批量上行
	// ---------------------------------------------------------------------
	mux.HandleFunc("POST /api/v1/sync", s.handleSync)

	// ---------------------------------------------------------------------
	// /api/v1/* 的兜底 404
	//
	// 必须在静态资源之前注册：否则拼错的接口路径会落到 FileServer，
	// 返回 Go 标准的**纯文本** "404 page not found"。
	// 前端拿到非 JSON 响应时 JSON.parse 会直接抛异常，
	// 排查起来会误以为是网络问题 —— 这类问题现场极难定位。
	//
	// 为什么要按方法逐个注册（而不能写一个不限方法的 "/api/v1/"）：
	// ServeMux 判定「GET /」与「/api/v1/」冲突 —— 前者方法更具体、
	// 后者路径更具体，无法比较出谁更特定，于是启动即 panic。
	// 逐个方法注册后，每个模式都是「方法 + 更具体路径」，冲突消失。
	// ---------------------------------------------------------------------
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		mux.HandleFunc(method+" /api/v1/", s.handleAPINotFound)
	}

	// ---------------------------------------------------------------------
	// 前端静态资源（同源部署，免 CORS）
	// ---------------------------------------------------------------------
	mux.Handle("GET /", s.staticHandler())

	// CurrentContest 要在 CurrentUser 之前或之后都行（两者互不依赖），
	// 但必须在业务 handler 之前 —— store 层每条 SQL 都从 ctx 取赛事，
	// 中间件没跑就等于全部落到默认赛事。
	return Chain(mux, Recover, LogRequests, CurrentUser, CurrentContest, CORS(s.cfg.Dev))
}

// handleAPINotFound 未匹配到任何业务路由时的 JSON 404。
func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, Envelope{
		Code:    CodeNotFound,
		Message: "接口不存在：" + r.Method + " " + r.URL.Path,
	})
}

// staticHandler 服务前端静态资源。
//
// 开发模式（SCORE_DEV=true）从磁盘目录读取，改前端刷新即生效；
// 生产模式从 embed.FS 读，实现「单个 exe 交付」。
func (s *Server) staticHandler() http.Handler {
	if s.cfg.Dev {
		return http.FileServer(http.Dir(s.cfg.StaticDir))
	}
	staticFS, err := fs.Sub(web.FS, ".")
	if err != nil {
		// embed.FS 在编译期生成，这里不会失败；若失败则返回 500。
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "静态资源初始化失败", http.StatusInternalServerError)
		})
	}
	return http.FileServer(http.FS(staticFS))
}
