package postgres

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// AuditStore 审计日志仓储。
type AuditStore struct{ q querier }

// Append 写入一条操作审计。
//
// 必须在事务内与业务写入一起调用（见 store.Store.WithTx），
// 否则会出现「数据改了但没留痕」—— 这正是争议场景下最要命的漏洞。
func (s *AuditStore) Append(ctx context.Context, e model.AuditEntry) error {
	_, err := s.q.Exec(ctx, `
		INSERT INTO audit_logs (operator, approver, action, target, before_val, after_val, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.Operator, e.Approver, string(e.Action), e.Target, e.Before, e.After, e.Reason)
	return mapError(err)
}

// List 按条件查询审计，时间倒序。
func (s *AuditStore) List(ctx context.Context, f store.AuditFilter) ([]model.AuditLog, error) {
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return "$" + strconv.Itoa(len(args)) }

	if f.Action != "" {
		where = append(where, "action = "+arg(f.Action))
	}
	if f.Operator != "" {
		where = append(where, "operator = "+arg(f.Operator))
	}
	if f.Approver != "" {
		where = append(where, "approver = "+arg(f.Approver))
	}
	if f.Since != nil {
		where = append(where, "created_at >= "+arg(*f.Since))
	}
	if f.Until != nil {
		where = append(where, "created_at <= "+arg(*f.Until))
	}

	q := `SELECT id, operator, approver, action, target, before_val, after_val, reason, created_at
	      FROM audit_logs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC, id DESC"
	if f.Limit > 0 {
		q += " LIMIT " + arg(f.Limit)
	}
	if f.Offset > 0 {
		q += " OFFSET " + arg(f.Offset)
	}

	rows, err := s.q.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.AuditLog
	for rows.Next() {
		var l model.AuditLog
		var action string
		if err := rows.Scan(&l.ID, &l.Operator, &l.Approver, &action, &l.Target,
			&l.Before, &l.After, &l.Reason, &l.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		l.Action = model.AuditAction(action)
		out = append(out, l)
	}
	return out, mapError(rows.Err())
}

// CountByAction 按动作统计条数。
//
// 用途：验证「六类操作都留了痕」。返回 map 而不是固定结构，
// 便于将来新增动作时不必改签名。
func (s *AuditStore) CountByAction(ctx context.Context) (map[string]int, error) {
	rows, err := s.q.Query(ctx, `SELECT action, count(*)::int FROM audit_logs GROUP BY action`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var action string
		var n int
		if err := rows.Scan(&action, &n); err != nil {
			return nil, mapError(err)
		}
		out[action] = n
	}
	return out, mapError(rows.Err())
}

// AppendImport 写入一条报名导入审计并回填 ID。
func (s *AuditStore) AppendImport(ctx context.Context, l *model.ImportLog) error {
	if l.Source == "" {
		l.Source = "excel"
	}
	if l.Status == "" {
		l.Status = "success"
	}
	if l.Detail == nil {
		l.Detail = []any{}
	}
	return mapError(s.q.QueryRow(ctx, `
		INSERT INTO import_logs (source, operator, summary, detail, status)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id, created_at`,
		l.Source, l.Operator, l.Summary, l.Detail, l.Status,
	).Scan(&l.ID, &l.CreatedAt))
}

// ListImports 按时间倒序读取导入记录。
func (s *AuditStore) ListImports(ctx context.Context, limit int) ([]model.ImportLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.Query(ctx, `
		SELECT id, source, operator, summary, detail, status, created_at
		FROM import_logs ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var out []model.ImportLog
	for rows.Next() {
		var l model.ImportLog
		var summaryRaw, detailRaw []byte
		if err := rows.Scan(&l.ID, &l.Source, &l.Operator, &summaryRaw,
			&detailRaw, &l.Status, &l.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		if err := scanJSONB(summaryRaw, &l.Summary); err != nil {
			return nil, fmt.Errorf("导入汇总字段异常: %w", err)
		}
		if err := scanJSONB(detailRaw, &l.Detail); err != nil {
			return nil, fmt.Errorf("导入明细字段异常: %w", err)
		}
		out = append(out, l)
	}
	return out, mapError(rows.Err())
}
