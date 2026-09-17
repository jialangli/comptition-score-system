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
