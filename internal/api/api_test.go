package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jialangli/comptition-score-server/internal/api"
	"github.com/jialangli/comptition-score-server/internal/config"
	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store/postgres"
)

// ============================================================================
// HTTP 层测试
//
// 走的是**完整的中间件链 + 真实的 PostgreSQL**：
// req → Recover → 日志 → 操作者 → CORS → mux → handler → service → DB。
//
// 不用 mock 的理由：这一层的价值恰恰在于「接线是否正确」——
// 路径参数名写错、状态码映射错、中间件没把操作者传下去，
// 这些都不是靠假仓储能测出来的。所以我宁可让它依赖真库并在缺失时跳过。
// ============================================================================

const defaultTestDSN = "postgres://postgres@127.0.0.1:5432/neuroscore_test_api?sslmode=disable"

type testServer struct {
	handler http.Handler
	svc     *service.Service
	db      *postgres.DB
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	db, err := postgres.Open(ctx, &config.Config{DatabaseURL: dsn, MaxOpenConns: 8})
	if err != nil {
		t.Skipf("跳过：连不上测试库\n  原因：%v\n  修复：bash scripts/test_db.sh", err)
	}
	t.Cleanup(db.Close)

	if err := db.TruncateAll(context.Background()); err != nil {
		t.Fatalf("清空测试库失败: %v", err)
	}

	svc := service.New(db)
	srv := api.New(svc, &config.Config{StaticDir: "testdata/no-such-dir", Dev: false})
	return &testServer{handler: srv.Routes(), svc: svc, db: db}
}

// resp 解包后的响应。
type resp struct {
	Status int
	Code   int
	Msg    string
	Data   json.RawMessage
}

// do 发起一次请求，走完整中间件链。
func (ts *testServer) do(t *testing.T, method, path string, body any, operator string) resp {
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

// expect 断言状态码与业务码。
func (r resp) expect(t *testing.T, status int) resp {
	t.Helper()
	if r.Status != status {
		t.Fatalf("HTTP 状态 = %d，期望 %d（message=%q data=%s）", r.Status, status, r.Msg, r.Data)
	}
	if status < 400 && r.Code != api.CodeOK {
		t.Fatalf("业务码 = %d，期望 0（message=%q）", r.Code, r.Msg)
	}
	if status >= 400 && r.Code == api.CodeOK {
		t.Fatalf("错误响应不应带业务码 0（message=%q）", r.Msg)
	}
	return r
}

// as 把 data 反序列化到目标类型。
func (r resp) as(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal(r.Data, dst); err != nil {
		t.Fatalf("data 反序列化失败: %v\n原始 data：%s", err, r.Data)
	}
}

// ---------------------------------------------------------------------------

func brainPlanetBody() map[string]any {
	return map[string]any{
		"id":     "brain_planet",
		"name":   "脑机星球",
		"groups": []string{"小学组", "初中组"},
		"tasks": []map[string]any{
			{"id": "focus", "name": "专注力任务", "type": "numeric", "maxScore": 100, "weight": 0.5, "control": "slider"},
			{"id": "build", "name": "搭建任务", "type": "numeric", "maxScore": 100, "weight": 0.5, "control": "slider"},
		},
		"scoreRule":   map[string]any{"template": "weighted_sum", "params": map[string]any{}},
		"bonusRules":  []map[string]any{{"template": "time_bonus", "params": map[string]any{"perSecond": 0.5, "cap": 10}}},
		"penaltyRule": map[string]any{"template": "per_card", "params": map[string]any{"yellow": 5, "red": 15}},
		"rankRule": map[string]any{
			"tieBreak":   []string{"score", "time"},
			"awardTiers": map[string]float64{"一等奖": 0.1, "二等奖": 0.2, "三等奖": 0.3},
		},
	}
}

// createEvent 建赛项并返回。
func (ts *testServer) createEvent(t *testing.T, body map[string]any) model.Event {
	t.Helper()
	var ev model.Event
	ts.do(t, http.MethodPost, "/api/v1/events", body, "运营A").expect(t, http.StatusCreated).as(t, &ev)
	return ev
}

// createTeam 建队伍并返回。
func (ts *testServer) createTeam(t *testing.T, eventID, no, name, group string) model.Team {
	t.Helper()
	var team model.Team
	ts.do(t, http.MethodPost, "/api/v1/events/"+eventID+"/teams", map[string]any{
		"no": no, "name": name, "group": group, "school": "测试学校", "coach": "张老师",
		// 带上选手名单：大屏脱敏是必须验证的能力，队伍没有名单就测不到
		"members": "张一 / 李二",
	}, "运营A").expect(t, http.StatusCreated).as(t, &team)
	return team
}

// ---------------------------------------------------------------------------

func TestHealthz(t *testing.T) {
	ts := newTestServer(t)
	var data struct {
		Status   string `json:"status"`
		Database string `json:"database"`
	}
	ts.do(t, http.MethodGet, "/api/v1/healthz", nil, "").expect(t, http.StatusOK).as(t, &data)
	if data.Status != "ok" || data.Database != "ok" {
		t.Fatalf("健康检查异常：%+v", data)
	}
}

func TestEventLifecycleOverHTTP(t *testing.T) {
	ts := newTestServer(t)

	// 建赛项 → 201
	ev := ts.createEvent(t, brainPlanetBody())
	if ev.ID != "brain_planet" || len(ev.Tasks) != 2 {
		t.Fatalf("建赛项返回异常：%+v", ev)
	}

	// 列表与单查
	var list struct {
		Events []model.Event `json:"events"`
		Total  int           `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/events", nil, "").expect(t, http.StatusOK).as(t, &list)
	if list.Total != 1 || len(list.Events[0].Tasks) != 2 {
		t.Fatalf("列表应带出任务项：%+v", list)
	}
	ts.do(t, http.MethodGet, "/api/v1/events/brain_planet", nil, "").expect(t, http.StatusOK)
	ts.do(t, http.MethodGet, "/api/v1/events/不存在", nil, "").expect(t, http.StatusNotFound)

	// 空请求体校验 → 校验库里已存的配置，永远 200
	var vres struct {
		OK       bool          `json:"ok"`
		Errors   []engineIssue `json:"errors"`
		Warnings []engineIssue `json:"warnings"`
	}
	ts.do(t, http.MethodPost, "/api/v1/events/brain_planet/validate", nil, "").
		expect(t, http.StatusOK).as(t, &vres)
	if !vres.OK {
		t.Fatalf("合规配置应校验通过：%+v", vres)
	}

	// 带请求体校验 → 校验体里的配置，错配置返回 200 + ok=false
	badBody := brainPlanetBody()
	badBody["tasks"] = []map[string]any{
		{"id": "focus", "name": "专注力任务", "type": "numeric", "maxScore": 100, "weight": 0.5},
		{"id": "focus", "name": "重复 id", "type": "numeric", "maxScore": 100, "weight": 0.5},
	}
	ts.do(t, http.MethodPost, "/api/v1/events/brain_planet/validate", badBody, "").
		expect(t, http.StatusOK).as(t, &vres)
	if vres.OK || len(vres.Errors) == 0 {
		t.Fatalf("重复任务 id 应报错：%+v", vres)
	}

	// 保存非法配置 → 400，且 data 里带完整校验结论
	bad := ts.do(t, http.MethodPut, "/api/v1/events/brain_planet", badBody, "运营A")
	bad.expect(t, http.StatusBadRequest)
	var out struct {
		Errors []engineIssue `json:"errors"`
	}
	bad.as(t, &out)
	if len(out.Errors) == 0 {
		t.Fatal("400 响应应在 data 里带出具体校验问题")
	}

	// 正常改配置 → 200，并写入审计
	updated := brainPlanetBody()
	tasks := updated["tasks"].([]map[string]any)
	tasks[0]["weight"] = 0.6
	tasks[1]["weight"] = 0.4
	updated["tasks"] = tasks
	updated["reason"] = "赛前规则复核"
	ts.do(t, http.MethodPut, "/api/v1/events/brain_planet", updated, "运营B").expect(t, http.StatusOK)

	var logs struct {
		Logs []model.AuditLog `json:"logs"`
	}
	ts.do(t, http.MethodGet, "/api/v1/audit-logs?action=改配置", nil, "").expect(t, http.StatusOK).as(t, &logs)
	found := false
	for _, l := range logs.Logs {
		if l.Reason == "赛前规则复核" && l.Operator == "运营B" {
			found = true
		}
	}
	if !found {
		t.Fatalf("改配置应留痕且操作人取自请求头：%+v", logs.Logs)
	}

	// 配置快照
	var snaps struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/config-snapshots", nil, "").expect(t, http.StatusOK).as(t, &snaps)
	if snaps.Total < 2 {
		t.Fatalf("应有「新建基线 + 改配置前留档」至少 2 份快照，实际 %d", snaps.Total)
	}

	// 有队伍时删赛项 → 409
	ts.createTeam(t, ev.ID, "1001", "星河队", "小学组")
	ts.do(t, http.MethodDelete, "/api/v1/events/brain_planet", nil, "运营A").expect(t, http.StatusConflict)
}

func TestTeamLifecycleOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())

	team := ts.createTeam(t, ev.ID, "1001", "星河队", "小学组")
	if team.ID == 0 || team.Status != model.TeamActive {
		t.Fatalf("建队伍返回异常：%+v", team)
	}

	// 「一号一队」→ 409
	dup := ts.do(t, http.MethodPost, "/api/v1/events/"+ev.ID+"/teams", map[string]any{
		"no": "1001", "name": "重复编号队", "group": "小学组",
	}, "运营A")
	dup.expect(t, http.StatusConflict)
	if !strings.Contains(dup.Msg, "一号一队") {
		t.Errorf("409 提示应点明「一号一队」：%q", dup.Msg)
	}

	// 组别不属于本赛项 → 400
	ts.do(t, http.MethodPost, "/api/v1/events/"+ev.ID+"/teams", map[string]any{
		"no": "1009", "name": "错组队", "group": "高中组",
	}, "运营A").expect(t, http.StatusBadRequest)

	path := "/api/v1/teams/" + itoa(team.ID)

	// 弃赛必须说明原因 → 400
	ts.do(t, http.MethodPost, path+"/withdraw", map[string]any{"reason": ""}, "运营A").
		expect(t, http.StatusBadRequest)

	// 正常弃赛 → 200
	var withdrawn model.Team
	ts.do(t, http.MethodPost, path+"/withdraw", map[string]any{"reason": "选手临时退赛"}, "运营A").
		expect(t, http.StatusOK).as(t, &withdrawn)
	if withdrawn.Status != model.TeamWithdrawn {
		t.Fatalf("弃赛后状态 = %q", withdrawn.Status)
	}

	// 默认列表不含弃赛队伍；withdrawn=1 才带出来
	var active, all struct {
		Teams []model.Team `json:"teams"`
		Total int          `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/events/"+ev.ID+"/teams", nil, "").expect(t, http.StatusOK).as(t, &active)
	ts.do(t, http.MethodGet, "/api/v1/events/"+ev.ID+"/teams?withdrawn=1", nil, "").expect(t, http.StatusOK).as(t, &all)
	if active.Total != 0 || all.Total != 1 {
		t.Fatalf("弃赛过滤不对：默认 %d 支 / 含弃赛 %d 支", active.Total, all.Total)
	}

	// 恢复 → 200
	ts.do(t, http.MethodPost, path+"/restore", nil, "运营A").expect(t, http.StatusOK)

	// 改组缺原因 → 400
	ts.do(t, http.MethodPut, path, map[string]any{
		"name": "星河队", "group": "初中组", "reason": "",
	}, "运营A").expect(t, http.StatusBadRequest)

	// 改组带原因 → 200
	var regrouped model.Team
	ts.do(t, http.MethodPut, path, map[string]any{
		"name": "星河队", "group": "初中组", "school": "测试学校", "members": "张一 / 李二",
		"reason": "现场核实组别报错",
	}, "运营A").expect(t, http.StatusOK).as(t, &regrouped)
	if regrouped.GroupCode != "初中组" {
		t.Fatalf("改组未生效：%+v", regrouped)
	}

	// 录一轮成绩后删队 → 409（外键 RESTRICT）
	ts.do(t, http.MethodPut, path+"/scores/1", map[string]any{
		"tasks": map[string]any{"focus": 80, "build": 80}, "time": 100, "signed": true,
	}, "裁判A").expect(t, http.StatusOK)
	del := ts.do(t, http.MethodDelete, path, map[string]any{"reason": "试图删掉带成绩的队伍"}, "运营A")
	del.expect(t, http.StatusConflict)
}

func TestImportOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())

	rows := []map[string]any{
		{"no": "1001", "name": "星河队", "school": "杭州实验小学", "coach": "张老师", "group": "小学组", "members": "张一 / 李二"},
		{"no": "1002", "name": "追光队", "school": "杭州第二实验小学", "coach": "孙老师", "group": "小学组", "members": "王三 / 赵四"},
		{"no": "1002", "name": "追光队（重复行）", "group": "小学组"},
		{"no": "1004", "name": "极客队", "group": "高中组"},
	}

	var preview struct {
		Rows    []importDiffRow `json:"rows"`
		Summary map[string]int  `json:"summary"`
	}
	ts.do(t, http.MethodPost, "/api/v1/imports/preview", map[string]any{
		"eventId": ev.ID, "rows": rows,
	}, "运营A").expect(t, http.StatusOK).as(t, &preview)

	if preview.Summary["insert"] != 2 || preview.Summary["conflict"] != 2 {
		t.Fatalf("预览四色不对：%+v", preview.Summary)
	}
	if len(preview.Rows) != 4 || preview.Rows[0].Line != 1 {
		t.Fatalf("预览应补齐行号：%+v", preview.Rows)
	}
	for _, r := range preview.Rows {
		if r.Status == "conflict" && r.Selectable {
			t.Errorf("冲突行不应可勾选：%+v", r)
		}
	}

	// 勾选冲突行 → 409
	ts.do(t, http.MethodPost, "/api/v1/imports/commit", map[string]any{
		"eventId": ev.ID, "rows": rows, "selectedLines": []int{1, 4},
	}, "运营A").expect(t, http.StatusConflict)

	// 只勾合法行 → 201
	var logEntry struct {
		ID      int64          `json:"id"`
		Summary map[string]int `json:"summary"`
		Status  string         `json:"status"`
	}
	ts.do(t, http.MethodPost, "/api/v1/imports/commit", map[string]any{
		"eventId": ev.ID, "rows": rows, "selectedLines": []int{1, 2}, "note": "首次导入",
	}, "运营A").expect(t, http.StatusCreated).as(t, &logEntry)
	if logEntry.ID == 0 || logEntry.Summary["insert"] != 2 {
		t.Fatalf("入库审计异常：%+v", logEntry)
	}

	var teams struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/events/"+ev.ID+"/teams", nil, "").expect(t, http.StatusOK).as(t, &teams)
	if teams.Total != 2 {
		t.Fatalf("入库后应有 2 支队伍，实际 %d", teams.Total)
	}

	// 未勾选任何行 → 400（明确报错而不是静默什么都不做）
	ts.do(t, http.MethodPost, "/api/v1/imports/commit", map[string]any{
		"eventId": ev.ID, "rows": rows,
	}, "运营A").expect(t, http.StatusBadRequest)

	// 导入日志
	var logs struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/import-logs", nil, "").expect(t, http.StatusOK).as(t, &logs)
	if logs.Total != 1 {
		t.Fatalf("导入日志应为 1 条，实际 %d", logs.Total)
	}
}

func TestScoreAndStandingsOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	// 注意：路径里用的是**数据库主键 ID**，不是队伍编号（编号是 1001，ID 是 1）。
	// 这两者混用是接入期最容易犯的错，因此测试刻意用真实 ID。
	star := ts.createTeam(t, ev.ID, "1001", "星河队", "小学组")
	ts.createTeam(t, ev.ID, "1003", "晨曦队", "初中组")

	base := "/api/v1/teams/" + itoa(star.ID) + "/scores"

	// 第一轮（未签字）
	ts.do(t, http.MethodPut, base+"/1", map[string]any{
		"tasks": map[string]any{"focus": 80, "build": 82}, "time": 100, "signed": false,
	}, "裁判A").expect(t, http.StatusOK)

	var rec model.ScoreRecord
	ts.do(t, http.MethodGet, base+"/1", nil, "").expect(t, http.StatusOK).as(t, &rec)
	if rec.TaskValues["focus"] != 80.0 || rec.Operator != "裁判A" {
		t.Fatalf("录分结果异常：%+v", rec)
	}

	// 第二轮（已签字）
	ts.do(t, http.MethodPut, base+"/2", map[string]any{
		"tasks": map[string]any{"focus": 95, "build": 92}, "time": 90, "signed": true,
	}, "裁判A").expect(t, http.StatusOK)

	// 已签字成绩直接改 → 409
	locked := ts.do(t, http.MethodPut, base+"/2", map[string]any{
		"tasks": map[string]any{"focus": 100, "build": 100}, "time": 90, "signed": true,
	}, "裁判A")
	locked.expect(t, http.StatusConflict)
	if !strings.Contains(locked.Msg, "改分申请") {
		t.Errorf("409 应引导走改分申请：%q", locked.Msg)
	}

	// 轮次越界 → 400
	ts.do(t, http.MethodPut, base+"/3", map[string]any{"tasks": map[string]any{}}, "裁判A").
		expect(t, http.StatusBadRequest)

	// 改分申请（缺原因 → 400）
	ts.do(t, http.MethodPost, base+"/2/change-requests", map[string]any{"after": 99, "reason": ""}, "裁判A").
		expect(t, http.StatusBadRequest)

	// 改分申请 → 200，且不落数据
	var req model.ScoreChangeRequest
	ts.do(t, http.MethodPost, base+"/2/change-requests", map[string]any{
		"after": 99, "reason": "申诉复核：搭建任务漏计 1 个构件",
	}, "裁判A").expect(t, http.StatusOK).as(t, &req)
	if req.Approved {
		t.Error("改分申请不应自动获批")
	}

	// 授权改分（缺审批人 → 400）
	ts.do(t, http.MethodPost, base+"/2/apply-change", map[string]any{
		"tasks": map[string]any{"focus": 100, "build": 92}, "time": 90, "signed": true,
		"reason": "申诉成立", "approver": "",
	}, "裁判长C").expect(t, http.StatusBadRequest)

	// 授权改分 → 200
	ts.do(t, http.MethodPost, base+"/2/apply-change", map[string]any{
		"tasks": map[string]any{"focus": 100, "build": 92}, "time": 90, "signed": true,
		"reason": "申诉成立，授权修改", "approver": "裁判长C",
	}, "裁判长C").expect(t, http.StatusOK)

	// 审计可按审批人检索
	var logs struct {
		Logs []model.AuditLog `json:"logs"`
	}
	ts.do(t, http.MethodGet, "/api/v1/audit-logs?approver=裁判长C", nil, "").expect(t, http.StatusOK).as(t, &logs)
	if len(logs.Logs) != 1 || logs.Logs[0].Approver != "裁判长C" {
		t.Fatalf("按审批人检索失败：%+v", logs.Logs)
	}

	// 榜单：按组别分组
	var st service.StandingsResult
	ts.do(t, http.MethodGet, "/api/v1/events/"+ev.ID+"/standings", nil, "").expect(t, http.StatusOK).as(t, &st)
	if len(st.Groups) != 2 {
		t.Fatalf("应返回 2 个组别，实际 %d", len(st.Groups))
	}
	if st.Groups[0].Group != "小学组" || st.Groups[0].Rows[0].BestRound != 2 {
		t.Fatalf("小学组应取优到第 2 轮：%+v", st.Groups[0])
	}
	// 未完成/未签字队伍在严格模式下不参与评奖
	var strict service.StandingsResult
	ts.do(t, http.MethodGet, "/api/v1/events/"+ev.ID+"/standings?signed=1&awardComplete=1", nil, "").
		expect(t, http.StatusOK).as(t, &strict)
	if len(strict.Groups) == 0 {
		t.Fatal("严格模式下也应返回分组结构")
	}

	// 指定组别
	var one service.StandingsResult
	ts.do(t, http.MethodGet, "/api/v1/events/"+ev.ID+"/standings?group=初中组", nil, "").
		expect(t, http.StatusOK).as(t, &one)
	if len(one.Groups) != 1 || one.Groups[0].Group != "初中组" {
		t.Fatalf("指定组别应只返回该组：%+v", one.Groups)
	}

	// 打分明细列表
	var list struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, base, nil, "").expect(t, http.StatusOK).as(t, &list)
	if list.Total != 2 {
		t.Fatalf("应有 2 轮成绩，实际 %d", list.Total)
	}
}

func TestScheduleAndScreenOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	ts.createTeam(t, ev.ID, "1001", "星河队", "小学组")
	ts.createTeam(t, ev.ID, "1002", "追光队", "小学组")

	// 赛台
	var seat model.Seat
	ts.do(t, http.MethodPost, "/api/v1/seats", map[string]any{"name": "赛台 1", "sortOrder": 0}, "运营A").
		expect(t, http.StatusCreated).as(t, &seat)
	if seat.ID == 0 {
		t.Fatal("赛台未回填 ID")
	}
	ts.do(t, http.MethodPost, "/api/v1/seats", map[string]any{"name": "  "}, "运营A").
		expect(t, http.StatusBadRequest)

	// 场次
	var slot model.Slot
	ts.do(t, http.MethodPost, "/api/v1/slots", map[string]any{
		"seatId": seat.ID, "period": "上午", "time": "09:00–12:00",
		"eventId": ev.ID, "group": "小学组", "type": "normal",
	}, "运营A").expect(t, http.StatusCreated).as(t, &slot)

	// 就近自动分配 → 两支小学组队伍都进这个场次
	var assigned struct {
		Count   int     `json:"count"`
		TeamIDs []int64 `json:"teamIds"`
	}
	ts.do(t, http.MethodPost, "/api/v1/slots/"+itoa(slot.ID)+"/auto-assign", nil, "运营A").
		expect(t, http.StatusOK).as(t, &assigned)
	if assigned.Count != 2 {
		t.Fatalf("自动分配应铺入 2 支队伍，实际 %d", assigned.Count)
	}

	// 手动改派
	ts.do(t, http.MethodPost, "/api/v1/slots/"+itoa(slot.ID)+"/teams", map[string]any{
		"teamIds": []int64{assigned.TeamIDs[0]}, "reason": "现场手动调整",
	}, "运营A").expect(t, http.StatusOK)

	// 场次列表带出队伍
	var slots struct {
		Slots []model.Slot `json:"slots"`
		Total int          `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/slots?seat="+itoa(seat.ID)+"&event="+ev.ID, nil, "").
		expect(t, http.StatusOK).as(t, &slots)
	if slots.Total != 1 || len(slots.Slots[0].TeamIDs) != 1 {
		t.Fatalf("场次列表应带出队伍绑定：%+v", slots)
	}

	// 加时赛场次 + 场内快照
	var extra model.Slot
	ts.do(t, http.MethodPost, "/api/v1/slots", map[string]any{
		"seatId": seat.ID, "period": "下午", "time": "14:00–17:00",
		"eventId": ev.ID, "group": "小学组", "type": "extra",
	}, "运营A").expect(t, http.StatusCreated).as(t, &extra)

	ts.do(t, http.MethodPost, "/api/v1/slots/"+itoa(extra.ID)+"/snapshot", map[string]any{
		"snapshot": []map[string]any{
			{"no": "9001", "name": "新星队", "school": "成都教装展学校", "coach": "陈老师"},
			{"no": "9001", "name": "新星队（重复）"},
		},
		"reason": "加时赛队伍导入",
	}, "运营A").expect(t, http.StatusOK)

	var snaps struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/slots/"+itoa(extra.ID)+"/snapshot", nil, "").
		expect(t, http.StatusOK).as(t, &snaps)
	if snaps.Total != 1 {
		t.Fatalf("同编号快照应被去重，实际 %d 条", snaps.Total)
	}
	// 主库不受影响
	var teams struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/events/"+ev.ID+"/teams", nil, "").expect(t, http.StatusOK).as(t, &teams)
	if teams.Total != 2 {
		t.Fatalf("加时赛快照污染了主库：%d 支", teams.Total)
	}
	// 正式场次不接受快照
	ts.do(t, http.MethodPost, "/api/v1/slots/"+itoa(slot.ID)+"/snapshot", map[string]any{
		"snapshot": []map[string]any{{"no": "9002", "name": "不该成功"}},
	}, "运营A").expect(t, http.StatusBadRequest)

	// 大屏：默认配置
	var cfg model.ScreenConfig
	ts.do(t, http.MethodGet, "/api/v1/screen/"+ev.ID+"/config", nil, "").expect(t, http.StatusOK).as(t, &cfg)
	if cfg.PageSize != model.DefaultPageSize || cfg.IntervalSec != model.DefaultIntervalSec {
		t.Fatalf("大屏默认配置不对：%+v", cfg)
	}

	// 改配置：每屏 1 条 → 两屏
	ts.do(t, http.MethodPut, "/api/v1/screen/"+ev.ID+"/config", map[string]any{
		"pageSize": 1, "intervalSec": 10, "reason": "演示用",
	}, "运营A").expect(t, http.StatusOK)

	var page struct {
		Page        int             `json:"page"`
		TotalPage   int             `json:"totalPage"`
		TotalRows   int             `json:"totalRows"`
		PageSize    int             `json:"pageSize"`
		IntervalSec int             `json:"intervalSec"`
		Locked      bool            `json:"locked"`
		Rows        []screenRowJSON `json:"rows"`
	}
	ts.do(t, http.MethodGet, "/api/v1/screen/"+ev.ID+"?page=1", nil, "").expect(t, http.StatusOK).as(t, &page)
	if page.PageSize != 1 || page.TotalPage != 2 || page.TotalRows != 2 || len(page.Rows) != 1 {
		t.Fatalf("分页异常：%+v", page)
	}
	if page.IntervalSec != 10 {
		t.Errorf("停留秒数 = %d，期望 10", page.IntervalSec)
	}
	// 姓名必须已脱敏（队名保留）
	for _, r := range page.Rows {
		if r.Members == "" {
			t.Error("大屏选手字段不应为空")
		}
		if !strings.Contains(r.Members, "*") {
			t.Errorf("大屏选手姓名未脱敏：%q", r.Members)
		}
	}
	// 越界页夹到末页
	ts.do(t, http.MethodGet, "/api/v1/screen/"+ev.ID+"?page=99", nil, "").expect(t, http.StatusOK).as(t, &page)
	if page.Page != 2 || len(page.Rows) != 1 {
		t.Fatalf("越界页应夹到末页：%+v", page)
	}
	// 锁定本场
	ts.do(t, http.MethodPut, "/api/v1/screen/"+ev.ID+"/config", map[string]any{
		"pageSize": 1, "intervalSec": 10, "pinned": "1001", "reason": "现场异常，锁定本屏",
	}, "运营A").expect(t, http.StatusOK)
	ts.do(t, http.MethodGet, "/api/v1/screen/"+ev.ID, nil, "").expect(t, http.StatusOK).as(t, &page)
	if !page.Locked {
		t.Fatal("锁定状态未生效")
	}

	// 删赛台（有场次需原因）
	ts.do(t, http.MethodDelete, "/api/v1/seats/"+itoa(seat.ID), nil, "运营A").
		expect(t, http.StatusBadRequest)
	ts.do(t, http.MethodDelete, "/api/v1/seats/"+itoa(seat.ID)+"?reason=赛台调整", nil, "运营A").
		expect(t, http.StatusOK)
}

func TestAuditSummaryAndSyncOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "1001", "星河队", "小学组")
	ts.createTeam(t, ev.ID, "1002", "追光队", "小学组")

	// 走一遍六类操作里的几类，验证审计概览会报告缺失项
	ts.do(t, http.MethodPost, "/api/v1/teams/"+itoa(team.ID)+"/withdraw", map[string]any{"reason": "退赛"}, "运营A").
		expect(t, http.StatusOK)

	var summary struct {
		Counts         map[string]int `json:"counts"`
		Required       []string       `json:"required"`
		MissingActions []string       `json:"missingActions"`
	}
	ts.do(t, http.MethodGet, "/api/v1/audit-summary", nil, "").expect(t, http.StatusOK).as(t, &summary)
	if len(summary.Required) != 6 {
		t.Fatalf("必须留痕的动作应有 6 类，实际 %v", summary.Required)
	}
	if summary.Counts["弃赛"] != 1 {
		t.Fatalf("弃赛应有 1 条：%+v", summary.Counts)
	}
	// 尚未发生的操作应出现在 missingActions 里，供运营核验留痕覆盖度
	if len(summary.MissingActions) == 0 {
		t.Error("未发生过的操作应被列为缺失")
	}

	// —— 离线批量上行 ——
	// 1001 服务端已有弃赛前录入的成绩？没有，因此本轮是 insert
	var syncOut struct {
		Total     int       `json:"total"`
		Succeeded int       `json:"succeeded"`
		Failed    int       `json:"failed"`
		Results   []syncRes `json:"results"`
	}
	ts.do(t, http.MethodPost, "/api/v1/sync", map[string]any{
		"scores": []map[string]any{
			{"eventId": ev.ID, "no": "1001", "roundNo": 1, "clientId": "pad-01",
				"tasks": map[string]any{"focus": 88, "build": 90}, "time": 95, "signed": true},
			{"eventId": ev.ID, "no": "1002", "roundNo": 1, "clientId": "pad-01",
				"tasks": map[string]any{"focus": 70, "build": 72}, "time": 110, "signed": true},
			{"eventId": ev.ID, "no": "9999", "roundNo": 1, "clientId": "pad-02",
				"tasks": map[string]any{"focus": 1}, "time": 1},
			{"eventId": ev.ID, "no": "", "roundNo": 1, "clientId": "pad-02"},
		},
	}, "离线同步").expect(t, http.StatusOK).as(t, &syncOut)

	if syncOut.Total != 4 || syncOut.Succeeded != 2 || syncOut.Failed != 2 {
		t.Fatalf("上行结果应为 2 成功 2 失败：%+v", syncOut)
	}
	if syncOut.Results[0].Action != "insert" {
		t.Errorf("首次上行应为 insert：%+v", syncOut.Results[0])
	}
	if syncOut.Results[2].Error == "" || syncOut.Results[3].Error == "" {
		t.Errorf("失败条目应给出原因：%+v", syncOut.Results)
	}
	// 单条失败不影响其余：已成功的两条确实落库了
	var scores struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/teams/"+itoa(team.ID)+"/scores", nil, "").
		expect(t, http.StatusOK).as(t, &scores)
	if scores.Total != 1 {
		t.Fatalf("成功条目应已落库，实际 %d 条", scores.Total)
	}

	// 再上行一次同样的已签字成绩 → 被拒绝（离线同步不是改分后门）
	ts.do(t, http.MethodPost, "/api/v1/sync", map[string]any{
		"scores": []map[string]any{
			{"eventId": ev.ID, "no": "1001", "roundNo": 1, "clientId": "pad-01",
				"tasks": map[string]any{"focus": 99, "build": 99}, "time": 80, "signed": true},
		},
	}, "离线同步").expect(t, http.StatusOK).as(t, &syncOut)
	if syncOut.Failed != 1 || !strings.Contains(syncOut.Results[0].Error, "已签字") {
		t.Fatalf("已签字成绩应拒绝覆盖：%+v", syncOut)
	}
}

func TestBadRequests(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())

	// 未知字段 → 400（尽早暴露前后端字段拼写不一致）
	ts.do(t, http.MethodPost, "/api/v1/events", map[string]any{
		"id": "x", "name": "x", "groups": []string{"小学组"}, "unknownField": 1,
	}, "运营A").expect(t, http.StatusBadRequest)

	// 路径参数非数字 → 400
	ts.do(t, http.MethodGet, "/api/v1/teams/abc", nil, "").expect(t, http.StatusBadRequest)
	ts.do(t, http.MethodGet, "/api/v1/teams/0", nil, "").expect(t, http.StatusBadRequest)

	// 轮次非数字 → 400
	ts.do(t, http.MethodGet, "/api/v1/teams/1/scores/x", nil, "").expect(t, http.StatusBadRequest)

	// 时间格式错 → 400（必须是 RFC3339，跨时区赛事不能用裸时间）
	ts.do(t, http.MethodGet, "/api/v1/audit-logs?since=2026-09-17", nil, "").expect(t, http.StatusBadRequest)

	// 请求体不是 JSON → 400
	req := httptest.NewRequest(http.MethodPost, "/api/v1/events", strings.NewReader("{不是 json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应 400，实际 %d", rec.Code)
	}

	// 不存在的赛项 → 404
	ts.do(t, http.MethodGet, "/api/v1/events/nope", nil, "").expect(t, http.StatusNotFound)

	// 未声明的操作者 → 审计里落到兜底名字，而不是空串
	ts.do(t, http.MethodPost, "/api/v1/events/"+ev.ID+"/teams", map[string]any{
		"no": "1001", "name": "星河队", "group": "小学组",
	}, "").expect(t, http.StatusCreated)
	var logs struct {
		Logs []model.AuditLog `json:"logs"`
	}
	ts.do(t, http.MethodGet, "/api/v1/audit-logs", nil, "").expect(t, http.StatusOK).as(t, &logs)
	for _, l := range logs.Logs {
		if l.Operator == "" {
			t.Fatal("审计里出现了空操作人 —— 留痕等于没留")
		}
	}
}

// ---------------------------------------------------------------------------

// engineIssue 对应 engine.Issue 的最小子集（避免测试依赖 engine 包全量类型）。
type engineIssue struct {
	Level   string `json:"level"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type importDiffRow struct {
	Line       int      `json:"line"`
	Status     string   `json:"status"`
	Selectable bool     `json:"selectable"`
	Conflicts  []string `json:"conflicts"`
}

type screenRowJSON struct {
	Rank     int    `json:"rank"`
	TeamName string `json:"name"`
	School   string `json:"school"`
	Group    string `json:"group"`
	Members  string `json:"members"`
	Award    string `json:"award"`
}

type syncRes struct {
	ClientID string `json:"clientId"`
	No       string `json:"no"`
	RoundNo  int    `json:"roundNo"`
	OK       bool   `json:"ok"`
	Action   string `json:"action"`
	Error    string `json:"error"`
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
