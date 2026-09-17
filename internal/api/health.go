package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// Version 服务版本号。构建时可用 -ldflags "-X ...=v1.0.0" 注入。
var Version = "dev"

// healthResp 健康检查响应。
type healthResp struct {
	Status    string `json:"status"`   // ok | degraded
	Database  string `json:"database"` // ok | error
	Version   string `json:"version"`
	UptimeSec int    `json:"uptimeSec"`
	Time      string `json:"time"`
}

// handleHealth GET /api/v1/healthz
//
// 数据库不可用时也返回 HTTP 200 + status=degraded（而不是 5xx），
// 让运维脚本能区分「进程还活着但依赖挂了」与「进程已经死了」两种情况。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	status, dbState := "ok", "ok"
	if err := s.svc.Store().Ping(ctx); err != nil {
		status, dbState = "degraded", "error"
		slog.Warn("健康检查：数据库不可用", "err", err)
	}

	OK(w, healthResp{
		Status:    status,
		Database:  dbState,
		Version:   Version,
		UptimeSec: int(time.Since(s.startAt).Seconds()),
		Time:      time.Now().Format(time.RFC3339),
	})
}
