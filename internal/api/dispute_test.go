package api_test

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 争议工单 HTTP 集成测试（打到真实 PG）
//
// 重点验证「参数校验在 HTTP 层就挡住」与「语义错误映射成正确的状态码」：
// 非法类型 400、同步冲突不接受人工上报 400、重复上报 409、已裁定不可撤 409。
// ============================================================================

func TestDisputesOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "9101", "争议队", "小学组")

	// ---------- 上报 ----------
	var created model.Dispute
	ts.do(t, http.MethodPost, "/api/v1/disputes", map[string]any{
		"teamId": team.ID, "roundNo": 1, "kind": "duplicate", "reason": "同一轮出现两份成绩",
	}, "裁判A").expect(t, http.StatusCreated).as(t, &created)

	if created.Status != model.DisputePending {
		t.Fatalf("新工单应为待裁定，实际 %q", created.Status)
	}
	if created.Source != model.SourceReferee {
		t.Fatalf("来源应为 referee，实际 %q", created.Source)
	}

	// ---------- 待裁定队列 ----------
	var list struct {
		Disputes []model.Dispute `json:"disputes"`
		Total    int             `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/disputes", nil, "").expect(t, http.StatusOK).as(t, &list)
	if list.Total != 1 {
		t.Fatalf("队列应有 1 条，实际 %d 条", list.Total)
	}

	// ---------- 参数校验：都在 HTTP 层挡住，不落到数据库 ----------
	ts.do(t, http.MethodPost, "/api/v1/disputes", map[string]any{
		"teamId": team.ID, "roundNo": 1, "kind": "bogus", "reason": "类型不合法",
	}, "裁判A").expect(t, http.StatusBadRequest)

	ts.do(t, http.MethodPost, "/api/v1/disputes", map[string]any{
		"teamId": team.ID, "roundNo": 3, "kind": "duplicate", "reason": "轮次不合法",
	}, "裁判A").expect(t, http.StatusBadRequest)

	// 同步冲突由系统补传自动建立，不接受人工上报
	ts.do(t, http.MethodPost, "/api/v1/disputes", map[string]any{
		"teamId": team.ID, "roundNo": 1, "kind": "sync_conflict", "reason": "人工想提同步冲突",
	}, "裁判A").expect(t, http.StatusBadRequest)

	// 原因过短
	ts.do(t, http.MethodPost, "/api/v1/disputes", map[string]any{
		"teamId": team.ID, "roundNo": 2, "kind": "duplicate", "reason": "短",
	}, "裁判A").expect(t, http.StatusBadRequest)

	// ---------- 重复上报 → 409 ----------
	ts.do(t, http.MethodPost, "/api/v1/disputes", map[string]any{
		"teamId": team.ID, "roundNo": 1, "kind": "duplicate", "reason": "又点了一次",
	}, "裁判A").expect(t, http.StatusConflict)

	// ---------- 裁定 ----------
	ts.do(t, http.MethodPost, "/api/v1/disputes/"+itoa(created.ID)+"/decide", map[string]any{
		"verdict": "disqualify", "reason": "确认重复提交，取消资格",
	}, "裁判长C").expect(t, http.StatusOK)

	var got model.Dispute
	ts.do(t, http.MethodGet, "/api/v1/disputes/"+itoa(created.ID), nil, "").
		expect(t, http.StatusOK).as(t, &got)
	if got.Status != model.DisputeDecided {
		t.Fatalf("裁定后应为已裁定，实际 %q", got.Status)
	}
	if got.Verdict == nil || *got.Verdict != model.VerdictDisqualify {
		t.Fatalf("结论应为 disqualify，实际 %v", got.Verdict)
	}
	if got.Decider != "裁判长C" {
		t.Fatalf("裁定人应为裁判长C，实际 %q", got.Decider)
	}

	// 非法结论
	ts.do(t, http.MethodPost, "/api/v1/disputes/"+itoa(created.ID)+"/decide", map[string]any{
		"verdict": "maybe", "reason": "结论不合法",
	}, "裁判长C").expect(t, http.StatusBadRequest)

	// ---------- 已裁定不可撤回 → 409 ----------
	ts.do(t, http.MethodPost, "/api/v1/disputes/"+itoa(created.ID)+"/withdraw", nil, "裁判A").
		expect(t, http.StatusConflict)

	// ---------- 撤回一条新工单 ----------
	var second model.Dispute
	ts.do(t, http.MethodPost, "/api/v1/disputes", map[string]any{
		"teamId": team.ID, "roundNo": 2, "kind": "duplicate", "reason": "手滑提错了",
	}, "裁判A").expect(t, http.StatusCreated).as(t, &second)

	ts.do(t, http.MethodPost, "/api/v1/disputes/"+itoa(second.ID)+"/withdraw", nil, "裁判A").
		expect(t, http.StatusOK)

	var afterWithdraw model.Dispute
	ts.do(t, http.MethodGet, "/api/v1/disputes/"+itoa(second.ID), nil, "").
		expect(t, http.StatusOK).as(t, &afterWithdraw)
	if afterWithdraw.Status != model.DisputeWithdrawn {
		t.Fatalf("撤回后应为已撤回，实际 %q", afterWithdraw.Status)
	}

	// 两条都已结，队列应为空
	ts.do(t, http.MethodGet, "/api/v1/disputes", nil, "").expect(t, http.StatusOK).as(t, &list)
	if list.Total != 0 {
		t.Fatalf("队列应为空，实际 %d 条", list.Total)
	}

	// ---------- 按队伍查历史（含已裁定 / 已撤回）----------
	var hist struct {
		Disputes []model.Dispute `json:"disputes"`
		Total    int             `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/teams/"+itoa(team.ID)+"/disputes", nil, "").
		expect(t, http.StatusOK).as(t, &hist)
	if hist.Total != 2 {
		t.Fatalf("该队历史应有 2 条（已裁定 + 已撤回），实际 %d 条", hist.Total)
	}

	// ---------- 不存在的工单 ----------
	ts.do(t, http.MethodGet, "/api/v1/disputes/999999", nil, "").expect(t, http.StatusNotFound)
}
