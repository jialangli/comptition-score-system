package engine

import "github.com/jialangli/comptition-score-server/internal/model"

// ============================================================================
// 牌面计数与红牌后果
//
// 与计分 / 排名同一原则：纯函数、确定性、可重复执行。
//
// 口径（2026/10/09 产品定调）：
//
//	黄牌 —— 记满阈值即升级 1 张红牌**并清零黄牌计数**（循环计数），因此
//	        **裁判端黄牌计数器的上限 = 阈值**；黄牌**本身永远不产生任何后果**。
//	红牌 —— **当场生效的取消比赛资格**：该队成绩**保留**（照常算分、照常留痕），
//	        但**不参与名次与奖项分配**。
//
// 三条「退出」路径的区别（现场极易混淆，故在此写死口径）：
//
//	弃赛          队伍主动退赛，可恢复；不进榜单。
//	红牌取消资格  **当场生效**、保留成绩，只取消名次与奖项（本文件实现）。
//	裁定取消资格  裁判长在裁定台按证据判定，**成绩作废**、不进榜单，不可撤销。
//
// 后两者不是一件事，也不互相触发：红牌不会把成绩作废，裁定作废也不依赖红牌。
// ============================================================================

// Cards 归一后的牌面计数。
type Cards struct {
	Yellow      int // 黄牌计数（当前周期，0..阈值-1）
	Red         int // 红牌总数（直接记的 + 黄牌累计升级出来的）
	UpgradedRed int // 其中由黄牌累计升级而来的部分
}

// ResolveCards 按赛项的牌面规则归一一条成绩记录的牌面。
//
// 黄牌计数器关闭时（rules.EnabledOrDefault() == false）**整个黄牌体系停用**：
// 黄牌不累计、也不升级红牌，只剩下裁判直接记的红牌。
// 这正是「关掉开关」在现场的语义 —— 不是只把界面藏起来，而是规则真的不生效。
func ResolveCards(rec model.ScoreRecord, rules *model.CardRules) Cards {
	yellow := rec.Yellow
	if yellow < 0 {
		yellow = 0
	}
	red := rec.Red
	if red < 0 {
		red = 0
	}

	upgraded := rec.UpgradedRed
	if upgraded < 0 {
		upgraded = 0
	}

	if rules.EnabledOrDefault() {
		if th := rules.ThresholdOrDefault(); th > 0 {
			// 满 th 张黄牌升级 1 张红牌，**余数留在黄牌计数上**（口径见文件头）。
			// 老数据里 yellow 可能仍是「累计值」（>= th）→ 在此一并归一，结果与旧口径一致；
			// 新数据由 NormalizeCards 在录入端先归零，这里自然不做额外折算。
			upgraded += yellow / th
			yellow = yellow % th
		}
	}

	return Cards{Yellow: yellow, Red: red + upgraded, UpgradedRed: upgraded}
}

// NormalizeCards 写路径归一：把「要记的黄牌总数」折算为 (黄牌计数, 升级红牌数)。
//
// 与 ResolveCards 同一口径，供录入端（记满即转红并清零）与服务端落库共用 ——
// 两端各算一套必然会漂移。黄牌计数器关闭时**不折算**（黄牌不累计、不升级）。
func NormalizeCards(yellow int, rules *model.CardRules) (int, int) {
	if yellow < 0 {
		yellow = 0
	}
	if !rules.EnabledOrDefault() {
		return yellow, 0
	}
	th := rules.ThresholdOrDefault()
	if th <= 0 {
		return yellow, 0
	}
	return yellow % th, yellow / th
}

// DisqualifiedByCards 该队是否因红牌被取消比赛资格。
//
// 判据：该队**任一轮**成绩记录归一后红牌数 > 0。
// 多轮取「任一轮命中」而不是「最好那一轮」—— 红牌取消资格是参赛资格的处置，
// 不因为另一轮没犯规就复活。
func DisqualifiedByCards(recs []model.ScoreRecord, rules *model.CardRules) bool {
	for i := range recs {
		if ResolveCards(recs[i], rules).Red > 0 {
			return true
		}
	}
	return false
}
