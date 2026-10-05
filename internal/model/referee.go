package model

import "time"

// ============================================================================
// 裁判码（0007）
//
// 前端 P1 家族的登录链路：姓名 + 6 位裁判码**双因子**，校验通过后按后台预绑的
// 「赛项 × 组别 × 赛台」自动绑定执裁范围（P1.5 / P1.6 只读展示）。
//
// 三条必须落实到数据结构的口径：
//
//  1. **只验码不够**，必须同时验姓名。而且失败要能区分原因 ——
//     码无效与姓名不匹配在前端是两个独立页面（P1.5b / P1.5c），
//     所以 service 层要给出不同语义的错误，不能统一成「登录失败」。
//  2. **执裁范围后台预绑**，登录时不选择。赛项 / 组别 / 赛台 / 身份
//     （裁判长 vs 裁判）全在建档时写入，登录只校验并原样返回。
//     现场可互换平板，但执裁范围固定 —— 错评由实际签字裁判负责。
//  3. **赛事级凭证**：换赛事必须重新建档发码，旧码在新赛事无效。
//     由 contest_id 分区保证，本模型不持有该字段（分区对调用方透明）。
// ============================================================================

// RefereeRole 裁判身份。决定登录后分流到哪个工作台。
type RefereeRole string

const (
	RoleReferee RefereeRole = "referee" // 普通裁判 → P2 当前比赛页
	RoleChief   RefereeRole = "chief"   // 裁判长   → P7 裁判长工作台
)

// RefereeStatus 裁判码状态。
type RefereeStatus string

const (
	RefereeUnused    RefereeStatus = "unused"    // 建档未用
	RefereeActivated RefereeStatus = "activated" // 已激活（首登联网激活后）
	RefereeRevoked   RefereeStatus = "revoked"   // 已作废
)

// RefereeCode 一条裁判码档案。
type RefereeCode struct {
	ID   int64       `json:"id"`
	Code string      `json:"code"` // 6 位无歧义字符
	Name string      `json:"name"` // 裁判姓名（双因子的另一半）
	Role RefereeRole `json:"role"`

	// 预绑执裁范围：后台建档时写入，登录时只读返回
	EventID   string `json:"eventId"`
	GroupCode string `json:"groupCode"`
	SeatID    *int64 `json:"seatId,omitempty"`

	Status      RefereeStatus `json:"status"`
	ActivatedAt *time.Time    `json:"activatedAt,omitempty"`
	CreatedAt   time.Time     `json:"createdAt"`
}

// RoleLabel 身份中文名。
func (r RefereeRole) Label() string {
	if r == RoleChief {
		return "裁判长"
	}
	return "裁判"
}

// StatusLabel 状态中文名。
func (s RefereeStatus) Label() string {
	switch s {
	case RefereeUnused:
		return "建档未用"
	case RefereeActivated:
		return "已激活"
	case RefereeRevoked:
		return "已作废"
	}
	return string(s)
}

// ScopeText 执裁范围文案（登录成功后展示，如「脑机星球 · 小学组 · A 赛台」）。
//
// seatName 由 service 层查出后传入 —— 本模型只持有 seat_id，
// 不为了拼一句话去反向依赖赛台仓储。
func (c RefereeCode) ScopeText(eventName, seatName string) string {
	scope := eventName
	if c.GroupCode != "" {
		scope += " · " + c.GroupCode
	}
	if seatName != "" {
		scope += " · " + seatName
	}
	return scope
}
