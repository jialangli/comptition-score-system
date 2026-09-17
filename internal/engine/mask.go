package engine

import "strings"

// ============================================================================
// 姓名脱敏
//
// 依据需求确认单 v1.3 §6.1：现场大屏是**无访问控制的公共空间**且循环滚动，
// 展示未成年人真实全名存在合规风险。因此大屏侧必须脱敏：
//
//	保留：队名、学校、组别、奖项
//	脱敏：选手姓名 → 张*，程俊淇 → 程**
//
// 脱敏放在后端做（大屏接口直接返回脱敏后的字符串），而不是交给前端 ——
// 前端有多个入口（大屏、公示页、导出），只要有一处漏调用就会泄露。
// ============================================================================

// separatorRunes 选手名单的分隔符。
//
// 源数据（WRC 报名表 / 成绩表）的分隔符并不统一，实测同时出现过
// 「/」「、」「,」「，」「|」五种，因此按字符集切分而不是按单一切分符。
var separatorRunes = []rune{'/', '、', ',', '，', '|'}

// MaskPerson 对单个姓名脱敏：保留首字，其余最多用 2 个 * 替代。
//
//	""      → ""      （空值原样返回）
//	"张"    → "张"    （单字不脱敏，脱掉就完全无法辨识）
//	"张一"  → "张*"   （2 字 → 1 个 *）
//	"程俊淇" → "程**"  （3 字 → 2 个 *）
//	"欧阳娜娜" → "欧**"（4 字 → 仍只 2 个 *，避免 * 数量暴露姓名长度）
//
// 与前端 maskPerson 逐位一致。长度按**字符（rune）**计，
// 而非 UTF-16 码元 —— 中文姓名场景下两者相同，仅在含 emoji 时才有差异。
func MaskPerson(name string) string {
	n := []rune(strings.TrimSpace(name))
	if len(n) == 0 {
		return ""
	}
	if len(n) <= 1 {
		return string(n)
	}
	stars := len(n) - 1
	if stars > 2 {
		stars = 2
	}
	return string(n[0]) + strings.Repeat("*", stars)
}

// MaskMembers 对选手名单脱敏。输入可含多种分隔符，输出统一为 " / " 分隔。
//
//	"张一 / 李二"       → "张* / 李*"
//	"韩五、杨六、朱七"   → "韩* / 杨* / 朱*"
//	""                  → "—"（占位符，避免大屏出现空白格）
//
// 与前端 maskMembers 逐位一致。
func MaskMembers(members string) string {
	parts := strings.FieldsFunc(members, func(r rune) bool {
		for _, s := range separatorRunes {
			if r == s {
				return true
			}
		}
		return false
	})
	masked := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		masked = append(masked, MaskPerson(p))
	}
	if len(masked) == 0 {
		return "—"
	}
	return strings.Join(masked, " / ")
}

// MaskCoach 对指导教师姓名脱敏，规则同 MaskPerson，空值给占位符。
func MaskCoach(coach string) string {
	if m := MaskPerson(coach); m != "" {
		return m
	}
	return "—"
}
