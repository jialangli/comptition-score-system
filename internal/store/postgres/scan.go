package postgres

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 扫描与取值辅助
// ============================================================================

// scanJSONB 把可能为 NULL 的 JSONB 列反序列化到 dst。
//
// 为什么不直接 scan 到目标类型：可空 JSONB 在 SQL NULL 与 JSON null 两种形态下
// pgx 的行为不一致（前者置零值、后者交给 json.Unmarshal）。先落到 []byte
// 再显式判断，两种形态都能正确处理，也顺带兜住历史脏数据。
func scanJSONB(raw []byte, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("解析 JSONB 失败: %w", err)
	}
	return nil
}

// notFoundIfNoRows 把 pgx.ErrNoRows 翻译成 store.ErrNotFound，并保留其它错误。
//
// 各 Repo 统一走这里，避免驱动错误码泄漏到 service 层。
func notFoundIfNoRows(err error) error {
	if err == nil {
		return nil
	}
	if isNoRows(err) {
		return store.ErrNotFound
	}
	return mapError(err)
}

// isNoRows 判断是否为「查无此行」。
//
// 用 errors.Is 而不是 == 比较：pgx 在部分路径上会包装该错误。
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// jsonArg 把可空 JSONB 参数归一化：
//
//   - 空 slice / 空 map / nil → SQL NULL（而不是 JSON 的 null / [] / {}）
//   - 其余原样交给 pgx 的 JSON 编码器
//
// 之所以要区分「空」与「null」：`groups JSONB NOT NULL DEFAULT '[]'` 这类列上，
// 传入 JSON null 虽不违反 NOT NULL（JSON null 不是 SQL NULL），但语义模糊，
// 后续被当作「未配置」还是「空数组」全靠猜。
func jsonArg(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []string:
		if len(x) == 0 {
			return []string{}
		}
		return x
	case []any:
		if len(x) == 0 {
			return []any{}
		}
		return x
	case map[string]any:
		if len(x) == 0 {
			return map[string]any{}
		}
		return x
	case map[string]float64:
		if len(x) == 0 {
			return nil
		}
		return x
	}
	return v
}

// nonEmpty 返回非空字符串或 nil，用于把「空串」写成 SQL NULL。
func nonEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
