package api

import (
	"net/http"
	"strconv"
)

// ============================================================================
// 赛台-队伍可写锁（0008）
//
// 抢不到锁**不是错误**：另一台平板该收到的是「该队正由 X 执裁」，
// 而不是一个 4xx —— 这两者给裁判的下一步动作完全不同
// （前者知道去找谁，后者只会反复重试）。
//
// 因此 acquire / check 都返回 200 + LockState，由前端按 writable 分支处理。
// ============================================================================

// handleAcquireLock POST /api/v1/locks/acquire
//
// 请求体：{ "seatId": 1, "teamId": 12, "holder": "pad-A", "holderLabel": "张老师" }
// holder 是机器标识（用于判等），holderLabel 是给人看的（提示「该队正由 X 执裁」）。
func (s *Server) handleAcquireLock(w http.ResponseWriter, r *http.Request) {
	seatID, teamID, holder, label, err := decodeLockKey(w, r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	st, err := s.svc.AcquireWriteLock(r.Context(), seatID, teamID, holder, label)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, st)
}

// handleCheckLock GET /api/v1/locks?seatId=1&teamId=12&holder=pad-A
//
// 只查询、不抢锁。平板进入打分页前的预判断用它。
func (s *Server) handleCheckLock(w http.ResponseWriter, r *http.Request) {
	seatID, err := queryInt64(r, "seatId")
	if err != nil {
		Fail(w, r, err)
		return
	}
	teamID, err := queryInt64(r, "teamId")
	if err != nil {
		Fail(w, r, err)
		return
	}
	st, err := s.svc.CheckWriteLock(r.Context(), seatID, teamID, r.URL.Query().Get("holder"))
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, st)
}

// handleReleaseLock POST /api/v1/locks/release
//
// 只释放**自己的**锁。解别人的锁会让锁形同虚设 ——
// A 正在打分，B 点一下释放就把 A 的锁解了。
func (s *Server) handleReleaseLock(w http.ResponseWriter, r *http.Request) {
	seatID, teamID, holder, _, err := decodeLockKey(w, r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	if err := s.svc.ReleaseWriteLock(r.Context(), seatID, teamID, holder); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"released": true})
}

// handleForceReleaseLock POST /api/v1/locks/force-release
//
// 裁判长 / 运维处置平板掉线、人换岗。这是 TTL 之外的即时出路 ——
// 没有它，一支被掉线平板占住的队伍要等满 TTL 才能换人执裁。
func (s *Server) handleForceReleaseLock(w http.ResponseWriter, r *http.Request) {
	seatID, teamID, _, _, err := decodeLockKey(w, r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	if err := s.svc.ForceReleaseWriteLock(r.Context(), seatID, teamID); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"released": true})
}

// decodeLockKey 解析（赛台, 队伍, 持锁者）三元组。
func decodeLockKey(w http.ResponseWriter, r *http.Request) (int64, int64, string, string, error) {
	var req struct {
		SeatID      int64  `json:"seatId"`
		TeamID      int64  `json:"teamId"`
		Holder      string `json:"holder"`
		HolderLabel string `json:"holderLabel"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return 0, 0, "", "", err
	}
	if req.SeatID <= 0 || req.TeamID <= 0 {
		return 0, 0, "", "", NewBadRequest("seatId 与 teamId 必须为正整数")
	}
	return req.SeatID, req.TeamID, req.Holder, req.HolderLabel, nil
}

// queryInt64 解析必需的正整数查询参数。
func queryInt64(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, NewBadRequest("缺少查询参数 " + name)
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return 0, NewBadRequest("查询参数 " + name + " 必须为正整数，收到 " + raw)
	}
	return v, nil
}
