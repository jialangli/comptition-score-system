package api_test

// ============================================================================
// 开赛就绪度端点：GET /api/v1/readiness（只读聚合）
//
// service 层已验证口径，这里验证「接线」：
//  1. 路由真的挂上了（不是 404 / 410）
//  2. 走完整中间件链后 X-Contest 仍生效（两场赛事各算各的）
//  3. 响应契约：ready / counters / items / gaps 字段都在
// ============================================================================

import (
	"context"
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/api"
	"github.com/jialangli/comptition-score-server/internal/model"
)

// readinessData 与 service.ReadinessReport 对齐的最小解包结构。
type readinessData struct {
	Ready    bool           `json:"ready"`
	Counters map[string]int `json:"counters"`
	Items    []struct {
		Key string `json:"key"`
		OK  bool   `json:"ok"`
	} `json:"items"`
	Gaps []string `json:"gaps"`
}

func fetchReadiness(t *testing.T, ts *testServer, contest string) readinessData {
	t.Helper()
	var out readinessData
	r := ts.doWith(t, http.MethodGet, "/api/v1/readiness", nil, "",
		func(req *http.Request) {
			if contest != "" {
				req.Header.Set(api.ContestHeader, contest)
			}
		})
	r.expect(t, http.StatusOK).as(t, &out)
	return out
}

// TestReadinessEndpointEmptyAndContestScoped 空赛事全红；X-Contest 切赛事各算各的。
func TestReadinessEndpointEmptyAndContestScoped(t *testing.T) {
	ts := newTestServer(t)

	// 默认赛事（不带头）：空 → 第一项红、ready=false、gaps 有排班缺口
	d := fetchReadiness(t, ts, "")
	if d.Ready {
		t.Errorf("空赛事不应 ready")
	}
	if d.Counters["events"] != 0 {
		t.Errorf("空赛事 events 应为 0: %+v", d.Counters)
	}
	if len(d.Items) == 0 || d.Items[0].OK {
		t.Errorf("items 应存在且第一项为红")
	}
	found := false
	for _, it := range d.Items {
		if it.Key == "场次已排班" {
			found = true
		}
	}
	if !found {
		t.Errorf("items 应含「场次已排班」（0021 排班接口已接）")
	}

	// A 场建一个赛项（先补 contests 行，外键要求）
	if err := ts.db.Repos().Contests.Create(context.Background(), &model.Contest{
		ID: "ct_rd_api", Name: "就绪度 API 场", Status: model.ContestLive,
	}); err != nil {
		t.Fatalf("建赛事失败: %v", err)
	}
	ts.doWith(t, http.MethodPost, "/api/v1/events", brainPlanetBody(), "运营A",
		func(r *http.Request) { r.Header.Set(api.ContestHeader, "ct_rd_api") },
	).expect(t, http.StatusCreated)

	dA := fetchReadiness(t, ts, "ct_rd_api")
	if dA.Counters["events"] != 1 {
		t.Errorf("A 场应看到自己的 1 个赛项: %+v", dA.Counters)
	}
	dDefault := fetchReadiness(t, ts, "")
	if dDefault.Counters["events"] != 0 {
		t.Errorf("默认赛事不应看到 A 场的赛项: %+v", dDefault.Counters)
	}
}
