package api_test

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 赛台-队伍可写锁 HTTP 集成测试
//
// 关键：抢不到锁**返回 200 而不是 4xx** —— 另一台平板要收到「该队正由 X 执裁」
// 并据此决定下一步，而不是拿到一个错误后反复重试。
// ============================================================================

func TestLocksOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "5001", "抢锁队", "小学组")
	const seatID = 1

	// ---------- pad-A 抢到 ----------
	var st model.LockState
	ts.do(t, http.MethodPost, "/api/v1/locks/acquire", map[string]any{
		"seatId": seatID, "teamId": team.ID, "holder": "pad-A", "holderLabel": "张老师",
	}, "裁判A").expect(t, http.StatusOK).as(t, &st)
	if !st.Acquired || !st.Writable {
		t.Fatalf("首次抢锁应成功：%+v", st)
	}

	// ---------- pad-B 抢不到，但仍返回 200 ----------
	var stB model.LockState
	ts.do(t, http.MethodPost, "/api/v1/locks/acquire", map[string]any{
		"seatId": seatID, "teamId": team.ID, "holder": "pad-B", "holderLabel": "李老师",
	}, "裁判B").expect(t, http.StatusOK).as(t, &stB)
	if stB.Acquired || stB.Writable {
		t.Fatalf("他人持锁时不应拿到锁：%+v", stB)
	}
	if stB.Holder != "pad-A" || stB.HolderLabel != "张老师" {
		t.Fatalf("应带回持锁者信息：%+v", stB)
	}
	if stB.Message == "" {
		t.Fatal("应给出可直接展示的提示")
	}

	// ---------- 查询可写状态 ----------
	var chk model.LockState
	ts.do(t, http.MethodGet,
		"/api/v1/locks?seatId="+itoa(seatID)+"&teamId="+itoa(team.ID)+"&holder=pad-A",
		nil, "").expect(t, http.StatusOK).as(t, &chk)
	if !chk.Writable {
		t.Fatalf("持锁者查询应可写：%+v", chk)
	}

	// ---------- 别人解不开自己的锁 ----------
	ts.do(t, http.MethodPost, "/api/v1/locks/release", map[string]any{
		"seatId": seatID, "teamId": team.ID, "holder": "pad-B",
	}, "裁判B").expect(t, http.StatusNotFound)

	// ---------- 自己释放 ----------
	ts.do(t, http.MethodPost, "/api/v1/locks/release", map[string]any{
		"seatId": seatID, "teamId": team.ID, "holder": "pad-A",
	}, "裁判A").expect(t, http.StatusOK)

	// ---------- 释放后可再抢 ----------
	ts.do(t, http.MethodPost, "/api/v1/locks/acquire", map[string]any{
		"seatId": seatID, "teamId": team.ID, "holder": "pad-B", "holderLabel": "李老师",
	}, "裁判B").expect(t, http.StatusOK).as(t, &stB)
	if !stB.Acquired {
		t.Fatalf("释放后应能抢到：%+v", stB)
	}

	// ---------- 强制释放（平板掉线处置）----------
	ts.do(t, http.MethodPost, "/api/v1/locks/force-release", map[string]any{
		"seatId": seatID, "teamId": team.ID,
	}, "裁判长C").expect(t, http.StatusOK)

	// ---------- 参数校验 ----------
	ts.do(t, http.MethodPost, "/api/v1/locks/acquire", map[string]any{
		"seatId": 0, "teamId": team.ID, "holder": "pad-A",
	}, "裁判A").expect(t, http.StatusBadRequest)
	ts.do(t, http.MethodGet, "/api/v1/locks", nil, "").expect(t, http.StatusBadRequest) // 缺 seatId
}
