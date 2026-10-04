// Package store 定义存储层契约。
//
// 约定（架构铁律）：
//   - SQL 只允许出现在 store/<driver> 子包内，例如 store/postgres
//   - 本包只放接口与语义化错误，让 service 层依赖抽象而非具体驱动
//   - 驱动错误（如 pgx 的 pgErr）不得泄漏到本包之外，统一转成本包的语义化错误
package store

import (
	"context"
	"errors"
)

// 语义化错误。service / api 层据此映射 HTTP 状态码，不关心底层是 PG 还是别的库。
var (
	ErrNotFound  = errors.New("资源不存在")
	ErrConflict  = errors.New("数据冲突")
	ErrDuplicate = errors.New("编号重复")   // 违反唯一约束（如「一号一队」）
	ErrInUse     = errors.New("资源仍被引用") // 违反外键 RESTRICT
)

// DB 是存储层的聚合入口。
//
// 说明：这里刻意保持精简（当前阶段只暴露连接生命周期），
// 各业务域的读写方法会在实现 P3 时按需加入，避免先写一堆无人调用的接口方法。
type DB interface {
	// Ping 做一次连通性检查，供 /healthz 使用。
	Ping(ctx context.Context) error
	// Close 释放连接池。
	Close()
}

// ============================================================================
// 当前赛事（0004 多赛事维度）
//
// 定义在本包而不是 service 包，是因为 **store/postgres 要读它**：
// 那里每条 SQL 都得带上当前赛事做分区，而 store/postgres 不能 import service
// （service → store/postgres，反向就成环了）。
//
// 与 store 的语义化错误同一个摆放理由：谁都可能用、且必须口径一致的东西，
// 放在依赖图的最底层。
// ============================================================================

type contestCtxKey struct{}

// DefaultContestID 未指定赛事时的兜底，对应 0004 迁移插入的 ct_default。
//
// 0004 把所有存量数据都归入 ct_default，所以「不指定赛事」等价于
// 「迁移前的单赛事行为」—— 现有调用方无需改动即可继续工作。
const DefaultContestID = "ct_default"

// WithContest 把当前赛事写入 context（api 层中间件调用）。
func WithContest(ctx context.Context, contestID string) context.Context {
	return context.WithValue(ctx, contestCtxKey{}, contestID)
}

// CurrentContest 取当前赛事 ID。
//
// 刻意不返回空串：空串写进 SQL 会匹配不到任何行，表现为「查什么都是空」，
// 而看不出是没带赛事标识导致的。宁可用一个显式默认赛事。
func CurrentContest(ctx context.Context) string {
	if v, ok := ctx.Value(contestCtxKey{}).(string); ok && v != "" {
		return v
	}
	return DefaultContestID
}
