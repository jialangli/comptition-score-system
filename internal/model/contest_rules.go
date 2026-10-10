package model

import "time"

// ============================================================================
// 赛事级规则（一个赛事一份）
//
// ⚠️ 与「赛项规则」不是一个层级：赛项规则（tasks / scoreRule / penaltyRule…）描述**怎么打分**，
// 本文件描述**赛事怎么编排**（名次空缺补不补）。前者在 events，前者一份赛项一份；
// 后者在 contest_rules，一个赛事一份、所有赛项共用。
// ============================================================================

// SubstituteMode 名次编排：被裁定「取消资格」的队伍留下的名次空缺怎么处理。
//
// 背景：取消资格的队伍**成绩作废、整行不进榜单**，但它在原榜上占过的那个名次位置怎么办，
// 前端做成了赛事级开关（页面「资格与递补」），因此后端必须能表达同一件事：
//
//	none（默认）不递补 —— 位置留空，后续队伍**保留原名次**。公示表上会出现 4 → 6 这种空洞，
//	            这正是想要的效果：「第 5 名被取消资格」这件事对外可被看见，
//	            且不会因为一次裁定就让后面所有人的名次都变了。
//	rank        按名次顺延 —— 空缺不补，后续队伍名次前移，编号连续。
//
// 取值与前端 `substituteRule.mode` **逐字一致**（'none' / 'rank'），
// 避免"前端一个词、后端另一个词"这种必然漂移。
type SubstituteMode string

const (
	SubstituteNone SubstituteMode = "none" // 不递补（空缺不补）—— 默认
	SubstituteRank SubstituteMode = "rank" // 按名次顺延
)

// Valid 是否为合法取值。
func (m SubstituteMode) Valid() bool {
	return m == SubstituteNone || m == SubstituteRank
}

// OrDefault 空值 / 非法值一律按默认（不递补）解释。
//
// 🔴 判据只此一处：调用方不要自己写 `if m == "" { ... }`。
// 前端 substituteMode() 的写法是 `=== 'rank' ? 'rank' : 'none'`，
// 也就是说**任何非 'rank' 的值都落到 'none'** —— 这里必须同口径，
// 否则一个脏值会让两端名次算法分叉（少名次 / 多名次都看不出来是它）。
func (m SubstituteMode) OrDefault() SubstituteMode {
	if m == SubstituteRank {
		return SubstituteRank
	}
	return SubstituteNone
}

// KeepGap 是否保留名次空缺（= 是否"不递补"）。
//
// engine.Rank 用它决定名次怎么发：
//
//	true  —— 名次 = 该队在「含作废队」的原榜上的位置（于是留出空洞）
//	false —— 名次 = 在有资格队伍里连续发放（于是前移）
//
// ⚠️ 不要把「红牌取消比赛资格」也算进来：那些队伍**留在榜内**、名次置 0，
// 而且排序时本来就排在最后，因此不产生空洞。见 engine.Rank 的注释。
func (m SubstituteMode) KeepGap() bool { return m.OrDefault() == SubstituteNone }

// Label 中文名（与前端 substituteModeName() 同文案，界面与留痕都读它）。
func (m SubstituteMode) Label() string {
	if m.OrDefault() == SubstituteRank {
		return "按名次顺延"
	}
	return "不递补（空缺不补）"
}

// ContestRules 赛事级规则。
type ContestRules struct {
	ContestID      string         `json:"contestId"`
	SubstituteMode SubstituteMode `json:"substituteMode"`
	// SubstituteNote 为什么这么设。切换递补规则会改公示名次，
	// 必须能回答"当时是谁、基于什么改的"（与前端 note 字段同义）。
	SubstituteNote string    `json:"substituteNote"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// Normalize 空值补齐为默认口径。
//
// 读路径也要调它：库里没有行（= 从未设置过）时 store 返回的是零值结构，
// 不归一就会把 `""` 当成一种取值往外传，而两端都只认 'none' / 'rank'。
func (r *ContestRules) Normalize() {
	if r == nil {
		return
	}
	r.SubstituteMode = r.SubstituteMode.OrDefault()
}
