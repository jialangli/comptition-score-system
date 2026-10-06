package service_test

import (
	"testing"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// ============================================================================
// 赛台-队伍可写锁（0008）—— 打到真实 PG
//
// 这是 E 项的**预防**机制（自动建单是事后处置）。
// 核心不变式：同台同队同一时刻只有一台平板可写，且抢不到时要能告诉对方是谁在执裁。
// ============================================================================

// newLockFixture 建赛项 + 一支队伍，返回队伍 ID。
func newLockFixture(t *testing.T, svc *service.Service, no string) int64 {
	t.Helper()
	ctx := operatorCtx("运营A")
	ev, err := svc.CreateEvent(ctx, brainPlanetEvent())
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	team, err := svc.CreateTeam(ctx, model.TeamDraft{
		EventID: ev.ID, TeamNo: no, Name: "抢锁队", GroupCode: "小学组",
	})
	if err != nil {
		t.Fatalf("建队伍失败: %v", err)
	}
	return team.ID
}

// TestWriteLockExclusive 同台同队只有一台平板可写；抢不到时带回持锁者。
func TestWriteLockExclusive(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newLockFixture(t, svc, "3001")
	const seatID = 1
	ctx := operatorCtx("裁判A")

	// ① pad-A 抢到
	st, err := svc.AcquireWriteLock(ctx, seatID, teamID, "pad-A", "张老师")
	if err != nil {
		t.Fatalf("首次抢锁失败: %v", err)
	}
	if !st.Acquired || !st.Writable {
		t.Fatalf("首次抢锁应成功：%+v", st)
	}

	// ② pad-B 抢不到，且必须知道是谁在执裁
	stB, err := svc.AcquireWriteLock(operatorCtx("裁判B"), seatID, teamID, "pad-B", "李老师")
	if err != nil {
		t.Fatalf("查询抢锁结果不应返回错误（要给出持锁者）: %v", err)
	}
	if stB.Acquired || stB.Writable {
		t.Fatalf("他人持锁时不应拿到锁：%+v", stB)
	}
	if stB.Holder != "pad-A" || stB.HolderLabel != "张老师" {
		t.Fatalf("应带回持锁者信息：%+v", stB)
	}
	if stB.Message == "" {
		t.Fatal("应给出可直接展示的提示（「该队正由 X 执裁」）")
	}

	// ③ pad-A 释放后 pad-B 可以抢到
	if err := svc.ReleaseWriteLock(ctx, seatID, teamID, "pad-A"); err != nil {
		t.Fatalf("释放自己的锁失败: %v", err)
	}
	stB2, err := svc.AcquireWriteLock(operatorCtx("裁判B"), seatID, teamID, "pad-B", "李老师")
	if err != nil {
		t.Fatalf("释放后抢锁失败: %v", err)
	}
	if !stB2.Acquired {
		t.Fatalf("释放后应能抢到：%+v", stB2)
	}
}

// TestWriteLockSelfRenewAndIsolation 自己续期不掉锁；别人解不开自己的锁。
func TestWriteLockSelfRenewAndIsolation(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newLockFixture(t, svc, "3002")
	const seatID = 2
	ctxA := operatorCtx("裁判A")

	if _, err := svc.AcquireWriteLock(ctxA, seatID, teamID, "pad-A", "张老师"); err != nil {
		t.Fatalf("抢锁失败: %v", err)
	}
	// 自己再抢 → 续期，仍成功（打完第 1 轮继续打第 2 轮不该掉锁）
	st, err := svc.AcquireWriteLock(ctxA, seatID, teamID, "pad-A", "张老师")
	if err != nil {
		t.Fatalf("续期失败: %v", err)
	}
	if !st.Acquired {
		t.Fatalf("自己的锁应可续期：%+v", st)
	}

	// 别人尝试释放 → 解不开（否则 A 正在打分被 B 一点就掉了）
	if err := svc.ReleaseWriteLock(operatorCtx("裁判B"), seatID, teamID, "pad-B"); err == nil {
		t.Fatal("不应能释放他人持有的锁")
	}
	chk, err := svc.CheckWriteLock(operatorCtx("裁判B"), seatID, teamID, "pad-B")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if chk.Writable {
		t.Fatal("他人释放后锁应仍在")
	}

	// 裁判长强制释放（平板掉线处置）
	if err := svc.ForceReleaseWriteLock(operatorCtx("裁判长C"), seatID, teamID); err != nil {
		t.Fatalf("强制释放失败: %v", err)
	}
	chk2, err := svc.CheckWriteLock(operatorCtx("裁判B"), seatID, teamID, "pad-B")
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if !chk2.Writable {
		t.Fatal("强制释放后应可写")
	}
}

// TestWriteLockExpiry 过期锁视为不存在，可被直接抢占。
//
// 没有 TTL 的锁会把一支队伍**永久锁死**（平板没电 / 掉线），比不加锁更糟。
func TestWriteLockExpiry(t *testing.T) {
	svc, db := newSvc(t)
	teamID := newLockFixture(t, svc, "3003")
	const seatID = 3
	ctx := operatorCtx("裁判A")

	// 直接用仓储造一把**已经过期**的锁（TTL 取负）
	if _, ok, err := db.Repos().Locks.Acquire(ctx, seatID, teamID, "pad-A", "张老师",
		-1*time.Minute); err != nil || !ok {
		t.Fatalf("造过期锁失败: ok=%v err=%v", ok, err)
	}

	// pad-B 应当能直接抢到
	st, err := svc.AcquireWriteLock(operatorCtx("裁判B"), seatID, teamID, "pad-B", "李老师")
	if err != nil {
		t.Fatalf("抢占过期锁失败: %v", err)
	}
	if !st.Acquired {
		t.Fatalf("过期锁应可被抢占：%+v", st)
	}
	if st.Holder != "pad-B" {
		t.Fatalf("抢占后持锁者应为 pad-B，实际 %q", st.Holder)
	}
}

// TestWriteLockDifferentSeats 不同赛台互不影响（锁的粒度含赛台）。
func TestWriteLockDifferentSeats(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newLockFixture(t, svc, "3004")
	ctx := operatorCtx("裁判A")

	if _, err := svc.AcquireWriteLock(ctx, 1, teamID, "pad-A", "张老师"); err != nil {
		t.Fatalf("抢锁失败: %v", err)
	}
	// 同一队伍换个赛台 → 另一把锁，互不影响
	st, err := svc.AcquireWriteLock(operatorCtx("裁判B"), 2, teamID, "pad-B", "李老师")
	if err != nil {
		t.Fatalf("换赛台抢锁失败: %v", err)
	}
	if !st.Acquired {
		t.Fatalf("不同赛台应是不同的锁：%+v", st)
	}
}
