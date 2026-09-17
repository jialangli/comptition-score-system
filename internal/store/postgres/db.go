// Package postgres 是 store.DB 的 PostgreSQL 实现，基于 pgx/v5。
//
// 为什么用 pgx 而不是 database/sql：
//   - 原生支持 JSONB 的读写映射（本项目的规则字段大量使用 JSONB）
//   - 自带连接池 pgxpool，省掉一层封装
//   - 更精确的类型映射与错误码，便于把唯一约束冲突识别成 store.ErrDuplicate
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jialangli/comptition-score-server/internal/config"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// DB 包装 pgx 连接池，实现 store.DB。
type DB struct {
	pool *pgxpool.Pool
}

// Open 建立连接池并立即做一次连通性探测 —— 宁可启动失败，也不要带着坏连接跑起来。
func Open(ctx context.Context, cfg *config.Config) (*DB, error) {
	pc, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析数据库连接串失败: %w", err)
	}

	pc.MaxConns = int32(cfg.MaxOpenConns)
	pc.MinConns = 1
	pc.MaxConnLifetime = time.Hour
	pc.MaxConnIdleTime = 30 * time.Minute
	pc.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("创建数据库连接池失败: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("数据库连通性检查失败（请确认 PG 已启动、库已建好）: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Ping 连通性检查。
func (d *DB) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// Close 释放连接池。
func (d *DB) Close() { d.pool.Close() }

// mapError 把驱动错误翻译成语义化错误，避免 pgx 的错误码泄漏到上层。
//
// 参考 PostgreSQL 错误码：
//
//	23505 = unique_violation       → ErrDuplicate（如违反「一号一队」）
//	23503 = foreign_key_violation  → ErrInUse（如删除仍被成绩引用的队伍）
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return fmt.Errorf("%w: %s", store.ErrDuplicate, pgErr.ConstraintName)
		case "23503":
			return fmt.Errorf("%w: %s", store.ErrInUse, pgErr.ConstraintName)
		case "23514":
			return fmt.Errorf("%w: 违反检查约束 %s", store.ErrConflict, pgErr.ConstraintName)
		}
	}
	return err
}
