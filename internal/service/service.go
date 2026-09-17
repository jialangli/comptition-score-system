// Package service 是用例编排层：定义事务边界、做业务校验、写审计留痕。
//
// 分层职责（架构铁律）：
//
//	service 不写 SQL（只调 store 接口）、不解析 HTTP（那是 api 层的事）
//	service 依赖 engine 做纯计算（校验、计分、排名）
//
// 本包最重要的两条不变式：
//
//  1. **凡是会改变业务数据的操作，都与它的审计记录写在同一个事务里**。
//     统一通过 `s.tx(ctx, func(r store.Repos) error {...})` 落地，
//     而不是每个方法自己拼装。这样「忘了留痕」不是靠自觉，而是靠没有别的写法。
//  2. **改配置 / 改分这类高危操作，先留档再改**：旧配置进 config_snapshots，
//     新旧差异进 audit_logs 的 before/after 字段。事后能回答「谁在什么时候把什么改成了什么」。
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jialangli/comptition-score-server/internal/engine"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// Service 用例编排入口。所有方法并发安全（内部只持有存储层抽象）。
type Service struct {
	st store.Store
}

// New 构造 Service。
func New(st store.Store) *Service { return &Service{st: st} }

// Store 暴露存储层，供健康检查等基础设施使用。
func (s *Service) Store() store.Store { return s.st }

// tx 在事务内执行写入 + 审计。
//
// 之所以单独包一层而不是让各方法直接调 WithTx：把「写业务」与「写审计」
// 绑成一个动作，各业务方法只需关心自己的两条写入，不必重复考虑事务。
func (s *Service) tx(ctx context.Context, fn func(store.Repos) error) error {
	return s.st.WithTx(ctx, fn)
}

// ro 返回非事务读入口。
func (s *Service) ro() store.Repos { return s.st.Repos() }

// ============================================================================
// 当前用户（鉴权插槽）
//
// 本期不做鉴权（架构 Non-goal），但请求上下文里已经预留了操作人。
// 下一期接入「姓名 + 裁判码」登录时，只需在 middleware 里把登录结果塞进 ctx，
// 本包所有审计自动带上真实操作人，**不需要改任何方法签名**。
// ============================================================================

type userCtxKey struct{}

// WithUser 把操作人写入 context（api 层中间件调用）。
func WithUser(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, userCtxKey{}, name)
}

// DefaultOperator 未登录时的兜底操作人。
//
// 刻意不返回空串：审计记录里出现空白操作人等于没留痕，
// 出争议时无法定位。宁可写一个显式的「未登录」也不留空。
const DefaultOperator = "未登录"

// CurrentUser 取当前操作人。
func CurrentUser(ctx context.Context) string {
	if v, ok := ctx.Value(userCtxKey{}).(string); ok && v != "" {
		return v
	}
	return DefaultOperator
}

// ============================================================================
// 语义化错误
//
// api 层据此映射 HTTP 状态码，业务层不关心「400 还是 409」。
// ============================================================================

// ValidationError 配置校验未通过（阻断保存）。
//
// 携带完整的校验结论（含 warning），让前端能一次性把问题都标出来，
// 而不是改一个报一个。
type ValidationError struct {
	Result engine.ValidationResult
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("赛项配置校验未通过（%d 项）：%s",
		len(e.Result.Errors), firstMessage(e.Result.Errors))
}

// Unwrap 让 errors.Is(err, ...) 仍能匹配底层错误（当前无包装，留作扩展）。
func (e *ValidationError) Unwrap() error { return nil }

// ErrReasonRequired 需要说明原因的操作没给原因。
//
// 「解锁必须说明原因」这类规则落到代码上就是它。
var ErrReasonRequired = errors.New("该操作必须说明原因")

// ErrScoreSubmitted 成绩已提交签字，不允许直接修改。
//
// 依据需求确认单：裁判提交后禁止直接改分，须发起改分申请并经裁判长授权。
var ErrScoreSubmitted = errors.New("成绩已提交签字，禁止直接修改，请发起改分申请")

// ErrConflictRows 导入存在冲突行，必须人工裁决。
var ErrConflictRows = errors.New("导入数据存在冲突，请先裁决后再入库")

// ErrNothingSelected 导入了但一行都没勾选。
//
// 单独成一个语义错误，是为了让 api 层返回 400 而不是 500 ——
// 「什么都没选就点了提交」是用户的正常误操作，不是服务端故障。
var ErrNothingSelected = errors.New("未勾选任何行，没有可入库的数据")

func firstMessage(issues []engine.Issue) string {
	if len(issues) == 0 {
		return ""
	}
	return issues[0].Msg
}

// requireReason 校验「必须说明原因」的操作。
func requireReason(reason string) error {
	if len([]rune(reason)) < 2 {
		return ErrReasonRequired
	}
	return nil
}
