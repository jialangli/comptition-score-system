package api_test

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 离线补传 → 同步冲突自动建单（E 项联动）
//
// 核心不变式：**不静默覆盖**。两台平板各打一份时，后上传的那份既不能把先前的
// 成绩盖掉，也不能让两份成绩静静躺着 —— 必须变成一张待裁定工单进 P8 队列
// （P12 note 7 口径）。
//
// 反面不变式同样重要：**同一台设备的续传 / 断线重传不能被误判成冲突**，
// 否则每次网络重试都会凭空生成一张工单。
// ============================================================================

type syncConflictRes struct {
	No          string `json:"no"`
	RoundNo     int    `json:"roundNo"`
	OK          bool   `json:"ok"`
	Action      string `json:"action"`
	Error       string `json:"error"`
	Conflict    bool   `json:"conflict"`
	DisputeID   int64  `json:"disputeId"`
	DisputeCode string `json:"disputeCode"`
}

type syncBatchResp struct {
	Total      int               `json:"total"`
	Succeeded  int               `json:"succeeded"`
	Failed     int               `json:"failed"`
	Conflicted int               `json:"conflicted"`
	Results    []syncConflictRes `json:"results"`
}

func TestSyncConflictAutoDispute(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "2001", "补传冲突队", "小学组")

	// push 模拟一台平板补传一条成绩。
	push := func(clientID string, focus float64) syncBatchResp {
		t.Helper()
		var out syncBatchResp
		ts.do(t, http.MethodPost, "/api/v1/sync", map[string]any{
			"scores": []map[string]any{{
				"eventId": ev.ID, "no": "2001", "roundNo": 1, "clientId": clientID,
				"tasks": map[string]any{"focus": focus, "build": 80}, "time": 100,
			}},
		}, "离线同步").expect(t, http.StatusOK).as(t, &out)
		return out
	}

	// ① 设备 A 首次上行 —— 服务端没有这条，正常入库
	out := push("pad-A", 80)
	if out.Total != 1 || out.Succeeded != 1 || out.Conflicted != 0 {
		t.Fatalf("首次上行应成功入库且不冲突：%+v", out)
	}

	// ② 同一台设备续传（断线重传）—— 必须仍然放行，不能被误判成冲突
	out = push("pad-A", 85)
	if out.Succeeded != 1 || out.Conflicted != 0 {
		t.Fatalf("同一设备续传不应判为冲突，否则每次重传都会凭空建单：%+v", out)
	}

	var before model.ScoreRecord
	ts.do(t, http.MethodGet, "/api/v1/teams/"+itoa(team.ID)+"/scores/1", nil, "").
		expect(t, http.StatusOK).as(t, &before)
	focusBefore := before.TaskValues["focus"]

	// ③ 另一台设备 pad-B 补传同队同轮 —— 冲突：不覆盖 + 自动建单
	out = push("pad-B", 30)
	if out.Conflicted != 1 || out.Succeeded != 0 {
		t.Fatalf("另一设备补传应判为冲突且不覆盖：%+v", out)
	}
	if !out.Results[0].Conflict {
		t.Fatalf("结果应标记 conflict，供前端提示「已转人工裁定」：%+v", out.Results[0])
	}
	if out.Results[0].DisputeCode == "" || out.Results[0].DisputeID == 0 {
		t.Fatalf("应带回工单号与 ID，便于前端跳转裁定台：%+v", out.Results[0])
	}

	// 关键断言：服务端的成绩没有被 pad-B 盖掉
	var after model.ScoreRecord
	ts.do(t, http.MethodGet, "/api/v1/teams/"+itoa(team.ID)+"/scores/1", nil, "").
		expect(t, http.StatusOK).as(t, &after)
	if after.TaskValues["focus"] != focusBefore {
		t.Fatalf("冲突时不应覆盖服务端成绩：期望 %v（pad-A 上传的），实际 %v",
			focusBefore, after.TaskValues["focus"])
	}

	// ④ 工单进了 P8 待裁定队列，且来源 / 类型可与人工上报区分
	var q struct {
		Disputes []model.Dispute `json:"disputes"`
		Total    int             `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/disputes", nil, "").expect(t, http.StatusOK).as(t, &q)
	if q.Total != 1 {
		t.Fatalf("队列应有 1 条同步冲突工单，实际 %d 条", q.Total)
	}
	if q.Disputes[0].Kind != model.DisputeSync {
		t.Fatalf("类型应为 sync_conflict，实际 %q", q.Disputes[0].Kind)
	}
	if q.Disputes[0].Source != model.SourceSystem {
		t.Fatalf("来源应为 system，实际 %q", q.Disputes[0].Source)
	}

	// ⑤ 第三台设备再撞一次 → 仍判冲突，但工单幂等，不能变成 2 条
	out = push("pad-C", 55)
	if out.Conflicted != 1 {
		t.Fatalf("第三次撞车仍应判冲突：%+v", out)
	}
	ts.do(t, http.MethodGet, "/api/v1/disputes", nil, "").expect(t, http.StatusOK).as(t, &q)
	if q.Total != 1 {
		t.Fatalf("补传重试应幂等，队列仍为 1 条，实际 %d 条", q.Total)
	}
}
