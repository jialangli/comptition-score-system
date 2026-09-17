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
	mux.HandleFunc("POST /api/v1/slots/{id}/teams", s.handleAssignSlotTeams)
	mux.HandleFunc("GET /api/v1/slots/{id}/snapshot", s.handleListSnapshot)
	mux.HandleFunc("POST /api/v1/slots/{id}/snapshot", s.handleSaveSnapshot)

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

	return Chain(mux, Recover, LogRequests, CurrentUser, CORS(s.cfg.Dev))
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
