// Package config 负责从环境变量加载服务配置。
//
// 设计原则：全部配置走环境变量，不读配置文件 —— 便于本地脚本、容器、CI 用同一套代码。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config 服务运行配置。
type Config struct {
	Addr         string // HTTP 监听地址，如 ":8080"
	DatabaseURL  string // PostgreSQL 连接串
	Dev          bool   // 开发模式：静态文件从磁盘读（改前端刷新即生效），否则用 embed
	StaticDir    string // 开发模式下的前端目录
	LogLevel     string // debug | info | warn | error
	MaxOpenConns int    // 连接池上限
}

// Load 从环境变量装载配置，缺失关键项时返回错误而不是 panic —— 让 main 决定怎么报告。
func Load() (*Config, error) {
	c := &Config{
		Addr:         env("SCORE_ADDR", ":8080"),
		DatabaseURL:  env("SCORE_DB_URL", ""),
		Dev:          envBool("SCORE_DEV", false),
		StaticDir:    env("SCORE_STATIC_DIR", "web"),
		LogLevel:     env("SCORE_LOG_LEVEL", "info"),
		MaxOpenConns: envInt("SCORE_DB_MAX_CONNS", 20),
	}

	if strings.TrimSpace(c.DatabaseURL) == "" {
		return nil, fmt.Errorf(
			"缺少环境变量 SCORE_DB_URL\n" +
				"示例：postgres://postgres@127.0.0.1:5432/neuroscore?sslmode=disable\n" +
				"提示：可先执行 source scripts/env.sh 载入本地开发默认值")
	}
	if c.MaxOpenConns <= 0 {
		c.MaxOpenConns = 20
	}
	return c, nil
}

// Redacted 返回打码后的 DSN，用于日志输出（避免把口令写进日志）。
func (c *Config) Redacted() string {
	dsn := c.DatabaseURL
	if i := strings.Index(dsn, "://"); i >= 0 {
		rest := dsn[i+3:]
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			userinfo := rest[:at]
			if colon := strings.Index(userinfo, ":"); colon >= 0 {
				return dsn[:i+3] + userinfo[:colon] + ":***" + rest[at:]
			}
		}
	}
	return dsn
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
