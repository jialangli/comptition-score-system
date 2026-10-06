package postgres

import (
	"context"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// LockStore 赛台-队伍可写锁仓储。
//
// 锁的本质就是 ux_team_write_lock 唯一索引 —— 由数据库裁决谁是第一个，
// 不靠应用层「先查有没有锁再插」（两台平板同时提交时两边都会查到「没有」）。
type LockStore struct{ q querier }

const lockColumns = `id, seat_id, team_id, holder, holder_label, acquired_at, expires_at`

func scanLock(row interface{ Scan(...any) error }) (*model.WriteLock, error) {
	var l model.WriteLock
	if err := row.Scan(&l.ID, &l.SeatID, &l.TeamID, &l.Holder, &l.HolderLabel,
		&l.AcquiredAt, &l.ExpiresAt); err != nil {
		return nil, notFoundIfNoRows(err)
	}
	return &l, nil
}

// Acquire 抢占（赛台, 队伍）的写锁。
//
// 一条 INSERT ... ON CONFLICT 同时覆盖三种情形，由 WHERE 子句裁决：
//
//	无人持锁         → 插入成功，拿到锁
//	锁已过期         → 覆盖（过期锁视为不存在，否则平板掉线会把队伍永久锁死）
//	就是我自己持的锁 → 覆盖（续期，打完一轮继续打下一轮不该掉锁）
//	他人持锁且未过期 → WHERE 不成立，DO UPDATE 不执行 → 返回 0 行 → 拿不到
func (s *LockStore) Acquire(ctx context.Context, seatID, teamID int64,
	holder, holderLabel string, ttl time.Duration) (*model.WriteLock, bool, error) {
	cid := store.CurrentContest(ctx)
	now := time.Now()
	expires := now.Add(ttl)
	var l model.WriteLock

	err := s.q.QueryRow(ctx, `
		INSERT INTO team_write_locks
			(contest_id, seat_id, team_id, holder, holder_label, acquired_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (contest_id, seat_id, team_id) DO UPDATE
			SET holder       = EXCLUDED.holder,
			    holder_label = EXCLUDED.holder_label,
			    acquired_at  = EXCLUDED.acquired_at,
			    expires_at   = EXCLUDED.expires_at
			WHERE team_write_locks.expires_at < EXCLUDED.acquired_at
			   OR team_write_locks.holder = EXCLUDED.holder
		RETURNING `+lockColumns, cid, seatID, teamID, holder, holderLabel, now, expires).
		Scan(&l.ID, &l.SeatID, &l.TeamID, &l.Holder, &l.HolderLabel, &l.AcquiredAt, &l.ExpiresAt)
	if err == nil {
		return &l, true, nil
	}
	if !isNoRows(err) {
		return nil, false, mapError(err)
	}
	// 他人持锁且未过期 → 拿不到。把持锁者信息带回去，前端提示「该队正由 X 执裁」
	cur, err := s.Get(ctx, seatID, teamID)
	if err != nil {
		return nil, false, err
	}
	return cur, false, nil
}

// Get 查看当前锁；无锁返回 ErrNotFound。
func (s *LockStore) Get(ctx context.Context, seatID, teamID int64) (*model.WriteLock, error) {
	return scanLock(s.q.QueryRow(ctx, `
		SELECT `+lockColumns+` FROM team_write_locks
		WHERE contest_id=$1 AND seat_id=$2 AND team_id=$3`,
		store.CurrentContest(ctx), seatID, teamID))
}

// Release 释放**自己的**锁。带 `AND holder = $n` —— 否则 A 正在打分，
// B 点一下释放就把 A 的锁解了，锁形同虚设。
func (s *LockStore) Release(ctx context.Context, seatID, teamID int64, holder string) error {
	tag, err := s.q.Exec(ctx, `
		DELETE FROM team_write_locks
		WHERE contest_id=$1 AND seat_id=$2 AND team_id=$3 AND holder=$4`,
		store.CurrentContest(ctx), seatID, teamID, holder)
	return oneRow(tag, err)
}

// ForceRelease 强制释放（裁判长 / 运维处置平板掉线、人换岗）。
func (s *LockStore) ForceRelease(ctx context.Context, seatID, teamID int64) error {
	tag, err := s.q.Exec(ctx, `
		DELETE FROM team_write_locks
		WHERE contest_id=$1 AND seat_id=$2 AND team_id=$3`,
		store.CurrentContest(ctx), seatID, teamID)
	return oneRow(tag, err)
}
