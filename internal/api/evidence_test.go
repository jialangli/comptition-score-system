package api_test

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 留底证据库 HTTP 集成测试
// ============================================================================

func TestEvidenceOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "6001", "留底队", "小学组")

	post := func(kind, file string) model.Evidence {
		t.Helper()
		var e model.Evidence
		ts.do(t, http.MethodPost, "/api/v1/evidence", map[string]any{
			"teamId": team.ID, "roundNo": 1, "kind": kind,
			"source": "referee_submit", "fileName": file, "operator": "张老师",
		}, "张老师").expect(t, http.StatusCreated).as(t, &e)
		return e
	}

	// ---------- 先登记两件 → 三件不齐 ----------
	post("score_sheet", "T-001_R1_sheet.pdf")
	post("signature", "T-001_R1_1240.jpg")

	var comp struct {
		Complete bool     `json:"complete"`
		Missing  []string `json:"missing"`
	}
	ts.do(t, http.MethodGet, "/api/v1/evidence/complete?teamId="+itoa(team.ID)+"&round=1",
		nil, "").expect(t, http.StatusOK).as(t, &comp)
	if comp.Complete {
		t.Fatal("缺一件时不该判定齐全")
	}
	if len(comp.Missing) != 1 || comp.Missing[0] != "提交留底截图" {
		t.Fatalf("缺失项应报「提交留底截图」，实际 %v", comp.Missing)
	}

	// ---------- 补上第三件 → 齐全 ----------
	snap := post("submit_snapshot", "T-001_R1_snap.jpg")
	ts.do(t, http.MethodGet, "/api/v1/evidence/complete?teamId="+itoa(team.ID)+"&round=1",
		nil, "").expect(t, http.StatusOK).as(t, &comp)
	if !comp.Complete || len(comp.Missing) != 0 {
		t.Fatalf("三件齐全后应 complete=true，实际 %+v", comp)
	}

	// ---------- 新建一律为「本地待同步」 ----------
	if snap.Status != model.EvLocal {
		t.Fatalf("新登记应为 local，实际 %q", snap.Status)
	}
	if snap.KindLabel != "提交留底截图" || snap.SourceLabel != "裁判提交成绩" {
		t.Fatalf("应带回中文标签：%q / %q", snap.KindLabel, snap.SourceLabel)
	}

	// ---------- 待上云队列 ----------
	var pend struct {
		Evidence []model.Evidence `json:"evidence"`
		Total    int              `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/evidence/pending", nil, "").
		expect(t, http.StatusOK).as(t, &pend)
	if pend.Total != 3 {
		t.Fatalf("应有 3 条待上云，实际 %d", pend.Total)
	}

	// ---------- 补传上云 ----------
	ts.do(t, http.MethodPost, "/api/v1/evidence/"+itoa(snap.ID)+"/synced",
		map[string]any{"storageUrl": "https://oss.example.com/x.jpg"}, "系统").
		expect(t, http.StatusOK)

	var list struct {
		Evidence []model.Evidence `json:"evidence"`
		Total    int              `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/evidence?teamId="+itoa(team.ID)+"&round=1", nil, "").
		expect(t, http.StatusOK).as(t, &list)
	if list.Total != 3 {
		t.Fatalf("该队第 1 轮应有 3 条证据，实际 %d", list.Total)
	}
	synced := 0
	for _, e := range list.Evidence {
		if e.Status == model.EvSynced {
			synced++
		}
	}
	if synced != 1 {
		t.Fatalf("应有 1 条已上云，实际 %d", synced)
	}

	// ---------- 重复登记被挡（409）----------
	ts.do(t, http.MethodPost, "/api/v1/evidence", map[string]any{
		"teamId": team.ID, "roundNo": 1, "kind": "signature",
		"source": "referee_submit", "fileName": "T-001_R1_1240.jpg", "operator": "张老师",
	}, "张老师").expect(t, http.StatusConflict)

	// ---------- 参数校验 ----------
	ts.do(t, http.MethodPost, "/api/v1/evidence", map[string]any{
		"teamId": team.ID, "roundNo": 1, "kind": "bogus",
		"source": "referee_submit", "fileName": "x.jpg",
	}, "张老师").expect(t, http.StatusBadRequest)

	ts.do(t, http.MethodPost, "/api/v1/evidence", map[string]any{
		"teamId": team.ID, "roundNo": 1, "kind": "signature",
		"source": "bogus", "fileName": "x.jpg",
	}, "张老师").expect(t, http.StatusBadRequest)
}
