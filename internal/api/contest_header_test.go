package api_test

// ============================================================================
// 0004 多赛事维度：HTTP 层验证 X-Contest 请求头
//
// 验证 X-Contest 请求头真的能把请求限定到指定赛事分区。
//
// 为什么必须走 HTTP 层：store 层的隔离已有 service 用例覆盖，但「中间件有没有被
// 装配进 Chain」是另一回事 —— 一旦 CurrentContest 没接上，所有请求会静默落到
// 默认赛事，store 层写得再对也没用。这类「装配遗漏」只能从入口验证。
// ============================================================================

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/api"
	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// TestContestHeaderScopesRequestToContest 同一份赛项配置在 A、B 两场各建一次
// （复合主键 (contest_id,id) 允许同名共存），A 场只能看到自己那份，不带头的
// 默认赛事看不到任何 A/B 数据。
func TestContestHeaderScopesRequestToContest(t *testing.T) {
	ts := newTestServer(t)

	// 两场赛事都要先在 contests 表里有记录（外键 RESTRICT 要求）。
	ctx := context.Background()
	for _, id := range []string{"ct_api_a", "ct_api_b"} {
		if err := ts.db.Repos().Contests.Create(ctx, &model.Contest{
			ID: id, Name: "赛事" + id, Status: model.ContestLive,
		}); err != nil {
			t.Fatalf("建赛事 %s 失败: %v", id, err)
		}
	}

	body := brainPlanetBody() // 标准的合法赛项配置（已在其它用例验证过）

	// A 场创建赛项
	ts.doWith(t, http.MethodPost, "/api/v1/events", body, "运营A",
		func(r *http.Request) { r.Header.Set(api.ContestHeader, "ct_api_a") },
	).expect(t, http.StatusCreated)

	// B 场创建同名赛项 —— 复合主键允许共存
	ts.doWith(t, http.MethodPost, "/api/v1/events", body, "运营B",
		func(r *http.Request) { r.Header.Set(api.ContestHeader, "ct_api_b") },
	).expect(t, http.StatusCreated)

	// A 场只见自己那份
	var listA struct {
		Events []model.Event `json:"events"`
		Total  int           `json:"total"`
	}
	ts.doWith(t, http.MethodGet, "/api/v1/events", nil, "",
		func(r *http.Request) { r.Header.Set(api.ContestHeader, "ct_api_a") },
	).expect(t, http.StatusOK).as(t, &listA)
	if listA.Total != 1 || len(listA.Events) != 1 {
		t.Fatalf("A 场应只见 1 个赛项，实为 total=%d len=%d", listA.Total, len(listA.Events))
	}
	if listA.Events[0].ID != "brain_planet" {
		t.Errorf("A 场赛项 id 应为 brain_planet，实为 %s", listA.Events[0].ID)
	}

	// B 场也只见自己那份（验证没被 A 覆盖）
	var listB struct {
		Events []model.Event `json:"events"`
		Total  int           `json:"total"`
	}
	ts.doWith(t, http.MethodGet, "/api/v1/events", nil, "",
		func(r *http.Request) { r.Header.Set(api.ContestHeader, "ct_api_b") },
	).expect(t, http.StatusOK).as(t, &listB)
	if listB.Total != 1 || listB.Events[0].ID != "brain_planet" {
		t.Fatalf("B 场应只见 1 个同名赛项，实为 total=%d", listB.Total)
	}

	// 不带 X-Contest → 回落默认赛事，看不到 A/B 的任何数据
	var listDefault struct {
		Events []model.Event `json:"events"`
		Total  int           `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/events", nil, "").expect(t, http.StatusOK).as(t, &listDefault)
	if listDefault.Total != 0 {
		t.Errorf("默认赛事不应看到 A/B 的赛项，实为 %d 个", listDefault.Total)
	}

	// 兜底赛事必须始终存在（TruncateAll 后会重建，否则所有写入都会被外键拦下）
	if _, err := ts.db.Repos().Contests.Get(ctx, store.DefaultContestID); err != nil {
		t.Errorf("默认赛事 %s 应始终存在: %v", store.DefaultContestID, err)
	}
}

// doWith 与 testServer.do 等价，但允许调用方在发出请求前改写 *http.Request
// （用于设置 X-Contest 等自定义请求头）。不修改 do，避免动到既有用例。
func (ts *testServer) doWith(t *testing.T, method, path string, body any, operator string, mut func(r *http.Request)) resp {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		rdr = bytes.NewReader(raw)
	}

	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if operator != "" {
		req.Header.Set(api.OperatorHeader, operator)
	}
	if mut != nil {
		mut(req)
	}

	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)

	out := resp{Status: rec.Code}
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是合法 JSON：%s\n%s", rec.Body.String(), err)
	}
	out.Code, out.Msg, out.Data = env.Code, env.Message, env.Data
	return out
}
