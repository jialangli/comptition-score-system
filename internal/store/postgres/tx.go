package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 事务与仓储装配
//
// querier 抽出「连接池」与「事务」的共同能力，于是同一套 Repo 实现
// 既可以跑在池上（单条读写），也可以跑在事务里（多条一起提交）。
//
// 这样做的好处是业务代码写法完全一致：
//
//	// 非事务
//	db.Repos().Teams.Create(ctx, t)
//	// 事务
//	db.WithTx(ctx, func(r store.Repos) error {
//	    r.Teams.Create(ctx, t)
//	    r.Audits.Append(ctx, entry)   // ← 与业务写入同一事务
//	    return nil
//	})
// ============================================================================

// querier 由 *pgxpool.Pool 与 pgx.Tx 共同满足。
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// newRepos 用同一个 querier 装配全部仓储。
func newRepos(q querier) store.Repos {
	return store.Repos{
		Events:    &EventStore{q: q},
		Teams:     &TeamStore{q: q},
		Scores:    &ScoreStore{q: q},
		Seats:     &SeatStore{q: q},
		Slots:     &SlotStore{q: q},
		Audits:    &AuditStore{q: q},
		Screen:    &ScreenStore{q: q},
		Snapshots: &SnapshotStore{q: q},
		CfgSnaps:  &ConfigSnapshotStore{q: q},
	}
}

// Repos 返回非事务读写入口。
//
// 用途：查询、以及「单条写入无需审计」的场景。
// 凡是要留痕的写入，一律走 WithTx。
func (d *DB) Repos() store.Repos { return newRepos(d.pool) }

// WithTx 在事务内执行 fn，并保证：
//
//   - fn 返回错误     → 回滚
//   - fn 内发生 panic → 回滚后继续 panic（不吞掉，交给上层 Recover 中间件）
//   - Commit 失败     → 返回错误（调用方据此判定本次操作事实上未生效）
//
// 注意：fn 内拿到的 Repos 与 fn 外的是不同实例，但派生于同一 querier。
// 切勿把 fn 内的 Repos 存起来在事务结束后使用 —— 那会导致
// 「用已关闭的事务执行 SQL」这类难查的错误。
func (d *DB) WithTx(ctx context.Context, fn func(store.Repos) error) error {
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return mapError(err)
	}

	defer func() {
		if p := recover(); p != nil {
			// 回滚用独立的 context：调用方的 ctx 可能已因 panic 场景被取消
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
	}()

	if err := fn(newRepos(tx)); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return err
	}
	return mapError(tx.Commit(ctx))
}
