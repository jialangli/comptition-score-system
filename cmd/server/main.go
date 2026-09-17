// Command server 是赛事统分后台的服务入口。
//
// 装配顺序：读配置 → 初始化日志 → 连数据库 → 装 API → 起 HTTP → 等退出信号 → 优雅关闭。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jialangli/comptition-score-server/internal/api"
	"github.com/jialangli/comptition-score-server/internal/config"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("服务异常退出", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogger(cfg.LogLevel)

	slog.Info("启动赛事统分后台", "addr", cfg.Addr, "dev", cfg.Dev, "db", cfg.Redacted())

	// 监听 Ctrl+C / SIGTERM，触发优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	slog.Info("数据库连接成功")

	// 装配顺序体现分层依赖：db（store）→ service → api。
	// api 只拿到 service，拿不到 db —— 这条边界是「所有写入都经过审计埋点」
	// 在结构上的保证，不是靠约定。
	svc := service.New(db)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(svc, cfg).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("HTTP 服务已监听", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("收到退出信号，开始优雅关闭")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	slog.Info("服务已关闭")
	return nil
}

// setupLogger 按配置初始化结构化日志（文本格式，便于本地阅读）。
func setupLogger(level string) {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lv})))
}
