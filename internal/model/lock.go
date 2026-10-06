package model

import "time"

// ============================================================================
// 赛台-队伍可写锁（0008）
//
// P12 note 7 的**预防**机制（E 项此前的自动建单是**事后**处置）：
//
//	绑定赛台后，同台同一队伍仅允许一台平板持有可写锁，
//	另一台对该队只读并提示「该队正由 X 执裁」。
//
// 锁的粒度是（赛台, 队伍），**不含轮次** —— 同一台平板要连续打完该队的
// 第 1 轮和第 2 轮，按轮次划分会在两轮之间留下被别人抢走的窗口。
// ============================================================================

// WriteLock 一把可写锁。
type WriteLock struct {
	ID          int64     `json:"id"`
	SeatID      int64     `json:"seatId"`
	TeamID      int64     `json:"teamId"`
	Holder      string    `json:"holder"`      // 持锁者机器标识（clientId），判等用它
	HolderLabel string    `json:"holderLabel"` // 展示名：「该队正由 X 执裁」里的 X
	AcquiredAt  time.Time `json:"acquiredAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// IsExpired 是否已过期。过期锁视为不存在，可被直接抢占。
func (l WriteLock) IsExpired(now time.Time) bool { return now.After(l.ExpiresAt) }

// LockState 抢锁 / 查锁的结果。
//
// 抢不到时**不返回错误**，而是把持锁者信息带回去 —— 另一台平板要显示的
// 是「该队正由 X 执裁」而不是「操作失败」，这两者给裁判的下一步动作完全不同。
type LockState struct {
	// Acquired 是否拿到了锁（查询语义下表示「当前无人持锁或就是你自己」）
	Acquired bool `json:"acquired"`
	// Writable 调用方是否可以写。查询场景下用它，不改动锁。
	Writable    bool   `json:"writable"`
	Holder      string `json:"holder,omitempty"`
	HolderLabel string `json:"holderLabel,omitempty"`
	// Message 给前端直接展示的提示，如「该队正由 张老师 执裁」
	Message string `json:"message,omitempty"`
}
