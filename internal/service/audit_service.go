package service

import (
	"context"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 审计：统一留痕入口
// ============================================================================

// log 在事务内写一条审计。
//
// 这是本包唯一的留痕写法 —— 其它方法不要自己 new 一个 AuditEntry 再调
// r.Audits.Append，否则「操作人从哪来」「reason 要不要校验」这些规则
// 会散落各处并逐渐走样。
//
// 必须在 s.tx(...) 的闭包内调用，此时 r 绑定在同一个事务上。
func log(ctx context.Context, r store.Repos, action model.AuditAction,
	target, before, after, reason string) error {
	return r.Audits.Append(ctx, model.AuditEntry{
		Operator: CurrentUser(ctx),
		Action:   action,
		Target:   target,
		Before:   before,
		After:    after,
		Reason:   reason,
	})
}

// logApproved 写一条「需授权才能执行」的审计：同时记录操作人与审批人。
//
// 与 log 分开而不是给它加参数：绝大多数操作不需要审批，
// 多一个恒为空的参数只会在每个调用点上引入噪音。
// 目前唯一的使用者是改分（ApplyScoreChange）。
func logApproved(ctx context.Context, r store.Repos, action model.AuditAction,
	approver, target, before, after, reason string) error {
	return r.Audits.Append(ctx, model.AuditEntry{
		Operator: CurrentUser(ctx),
		Approver: approver,
		Action:   action,
		Target:   target,
		Before:   before,
		After:    after,
		Reason:   reason,
	})
}

// AuditLogs 查询操作审计（时间倒序）。
func (s *Service) AuditLogs(ctx context.Context, f store.AuditFilter) ([]model.AuditLog, error) {
	return s.ro().Audits.List(ctx, f)
}

// AuditSummary 按动作统计审计条数。
//
// 用于验证「六类操作都留了痕」，也用于运营后台的概览卡片。
func (s *Service) AuditSummary(ctx context.Context) (map[string]int, error) {
	return s.ro().Audits.CountByAction(ctx)
}

// RequiredAuditActions 返回必须留痕的动作清单。
//
// 交给调用方做覆盖度检查（如「本次赛事是否已经产生过弃赛记录」），
// 避免把业务判断写死在 service 里。
func (s *Service) RequiredAuditActions() []model.AuditAction {
	return model.AllAuditActions()
}

// ImportLogs 查询报名导入记录。
func (s *Service) ImportLogs(ctx context.Context, limit int) ([]model.ImportLog, error) {
	return s.ro().Audits.ListImports(ctx, limit)
}
