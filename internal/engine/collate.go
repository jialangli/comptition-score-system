package engine

import (
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// ============================================================================
// 中文姓名排序
//
// 为什么需要它：名次完全相同时（总分、用时都相同）必须有一个兜底比较器，
// 否则榜单顺序不确定 —— 现场刷一次变一次，无法解释。
//
// 选拼音序而不是 Unicode 码点序，原因有二：
//
//  1. 与前端原型一致。前端用 `localeCompare(a, b, 'zh')`，实测「破晓队」
//     排在「智造队」之前（pò < zhì），码点序则相反（智 U+667A < 破 U+7834）。
//     两端不一致会导致 demo 与真实系统名次不同，是最难排查的那类问题。
//  2. 对观众而言拼音序才是「有序」的。码点序在中文里看起来是乱序。
//
// 实际触发场景：赛事早期大量队伍尚无成绩（总分为 0、用时为 0），
// 它们之间的先后完全由这个比较器决定，且会长时间停留在大屏上。
// ============================================================================

var (
	// zhCollator 中文排序器。排序表体积较大，进程内复用一份。
	//
	// ⚠️ x/text 的 Collator.CompareString 内部会复用 c.iter(0)/c.iter(1)
	// 两个游标来推进字符串，因此**不是并发安全的**（与 bytes.Compare 不同）。
	// 用一把锁把它串行化即可 —— 榜单规模在百队量级，
	// 排序总比较次数约 n·log n ≈ 700 次，锁开销完全可以忽略。
	zhCollatorOnce sync.Once
	zhCollator     *collate.Collator
	zhCollatorMu   sync.Mutex
)

// CompareNameZh 按中文拼音序比较两个名称，返回 -1 / 0 / 1。
//
// 这是 RankOptions.NameLess 的默认实现。若调用方希望零依赖、纯码点序
// （例如跑在资源受限环境），可显式传入 CodepointNameLess。
func CompareNameZh(a, b string) int {
	if a == b {
		return 0
	}
	zhCollatorOnce.Do(func() {
		zhCollator = collate.New(language.Chinese)
	})
	zhCollatorMu.Lock()
	defer zhCollatorMu.Unlock()
	return zhCollator.CompareString(a, b)
}

// CodepointNameLess 不依赖任何排序表的兜底比较器：按 Unicode 码点升序。
// 同样是确定性顺序，只是顺序不符合中文阅读习惯。
func CodepointNameLess(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
