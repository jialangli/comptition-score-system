package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 赛台-队伍可写锁（0008）
//
// 这是 E 项的**预防**机制 —— 此前做的「补传冲突自动建单」是事后处置。
// 只建单不预防的代价：要等补传撞车才发现，而那时两份成绩都已经打完了，
// 裁判长还得在 P8 里二选一。加锁把绝大多数冲突消灭在发生之前。
//
// 抢不到锁**不返回错误**：另一台平板该显示的是「该队正由 X 执裁」，
// 而不是「操作失败」—— 这两者给裁判的下一步动作完全不同。
//
// ⚠️ 本文件**不写审计**：抢锁发生在每一次提交 / 暂存，频率极高，
// 写审计会冲垮 audit_logs、稀释真正需要追溯的操作。
// ============================================================================

// DefaultLockTTL 默认锁时长。
//
// 取值要覆盖「一台平板执裁完一支队伍」的时间（含排队等待、争议处理）。
// 太短会在执裁中途掉锁，太长则平板掉线后要等更久。
// 锁有 TTL 兜底，所以偏长一点更安全 —— 掉线的平板可以由裁判长强制解锁。
const DefaultLockTTL = 2 * time.Hour

// AcquireWriteLock 抢占（赛台, 队伍）的写锁。
//
// 三种结果都由 LockState 表达，不抛错：
//
//	拿到 / 续期成功 → Acquired=true, Writable=true
//	他人持锁未过期  → Acquired=false, Writable=false, 带回持锁者信息
func (s *Service) AcquireWriteLock(ctx context.Context, seatID, teamID int64,
	holder, holderLabel string) (*model.LockState, error) {
	if holder == "" {
		return nil, ErrReasonRequired // 复用「必填」语义：没有持锁者标识的锁无法判等
	}
	lock, ok, err := s.ro().Locks.Acquire(ctx, seatID, teamID, holder, holderLabel, DefaultLockTTL)
	if err != nil {
		return nil, err
	}
	if ok {
		return &model.LockState{Acquired: true, Writable: true,
			Holder: lock.Holder, HolderLabel: lock.HolderLabel}, nil
	}
	return &model.LockState{Acquired: false, Writable: false,
		Holder: lock.Holder, HolderLabel: lock.HolderLabel,
		Message: heldByMessage(lock.HolderLabel)}, nil
}

// CheckWriteLock 查询当前是否可写（**不抢锁**，用于平板进入打分页前置判断）。
func (s *Service) CheckWriteLock(ctx context.Context, seatID, teamID int64,
	holder string) (*model.LockState, error) {
	lock, err := s.ro().Locks.Get(ctx, seatID, teamID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// 无人持锁
			return &model.LockState{Acquired: false, Writable: true}, nil
		}
		return nil, err
	}
	// 过期锁视为不存在
	if lock.IsExpired(time.Now()) {
		return &model.LockState{Acquired: false, Writable: true}, nil
	}
	if holder != "" && lock.Holder == holder {
		return &model.LockState{Acquired: true, Writable: true,
			Holder: lock.Holder, HolderLabel: lock.HolderLabel}, nil
	}
	return &model.LockState{Acquired: false, Writable: false,
		Holder: lock.Holder, HolderLabel: lock.HolderLabel,
		Message: heldByMessage(lock.HolderLabel)}, nil
}

// ReleaseWriteLock 释放**自己的**锁。
func (s *Service) ReleaseWriteLock(ctx context.Context, seatID, teamID int64,
	holder string) error {
	if holder == "" {
		return ErrReasonRequired
	}
	return s.ro().Locks.Release(ctx, seatID, teamID, holder)
}

// ForceReleaseWriteLock 强制释放（裁判长 / 运维处置平板掉线、人换岗）。
//
// 这是 TTL 之外的另一条出路：TTL 是自动兜底，强制解锁是人工即时处置。
// 没有它，一支被掉线平板占住的队伍要等满 TTL 才能换人执裁。
func (s *Service) ForceReleaseWriteLock(ctx context.Context, seatID, teamID int64) error {
	return s.ro().Locks.ForceRelease(ctx, seatID, teamID)
}

// heldByMessage 给另一台平板的提示：「该队正由 X 执裁」。
func heldByMessage(holderLabel string) string {
	if holderLabel == "" {
		return "该队正由另一台平板执裁（只读）"
	}
	return fmt.Sprintf("该队正由 %s 执裁（只读）", holderLabel)
}
