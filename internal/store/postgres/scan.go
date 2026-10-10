package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

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

// jsonArg 把 JSONB 参数归一化，避免「空集合」被写成 SQL NULL。
//
// 背景：`groups JSONB NOT NULL DEFAULT '[]'` 这类列上，传 SQL NULL 会直接违反
// NOT NULL 报错；而 Go 的 **nil slice** 经 pgx 的 JSON 编码器恰好被当成 SQL NULL。
// 所以集合类型里的空值要归一成 `[]` / `{}`（JSON 的 null / [] / {} 三态里只保留一种）。
//
// ⚠️ 兜底必须走反射，不能只列具体类型：2026-10-10 加 `events.phases`（[]model.Phase）时，
// 就是因为这份白名单里没有它，「清空阶段」直接撞 NOT NULL 报错 ——
// 这是清单式写法的老毛病：**每加一个列就漏一个**。
//
// 唯一的例外是空 map[string]float64 → SQL NULL（该列可空，`null` 表示"未配置"
// 与 `{}` 语义不同）；它当前没有调用点，保留原行为不动。
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
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Slice:
		if rv.IsNil() || rv.Len() == 0 {
			return reflect.MakeSlice(rv.Type(), 0, 0).Interface()
		}
	case reflect.Map:
		if rv.IsNil() || rv.Len() == 0 {
			return reflect.MakeMap(rv.Type()).Interface()
		}
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
