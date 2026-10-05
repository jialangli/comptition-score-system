package api_test

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 发布单元与移交 / 发布状态机 HTTP 集成测试
// ============================================================================

func TestReleasesOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())

	// ---------- 取（或建）发布单元 ----------
	var u model.ReleaseUnit
	ts.do(t, http.MethodPost, "/api/v1/releases/ensure", map[string]any{
		"eventId": ev.ID, "groupCode": "小学组",
	}, "运营A").expect(t, http.StatusOK).as(t, &u)
	if u.Status != model.ReleaseNotHanded {
		t.Fatalf("新建单元应为未移交，实际 %q", u.Status)
	}

	// ---------- 未移交不得发布（409）----------
	ts.do(t, http.MethodPost, "/api/v1/releases/"+itoa(u.ID)+"/publish", nil, "运营B").
		expect(t, http.StatusConflict)

	// ---------- 移交 ----------
	ts.do(t, http.MethodPost, "/api/v1/releases/hand-over", map[string]any{
		"eventId": ev.ID, "groupCode": "小学组",
	}, "裁判长C").expect(t, http.StatusOK).as(t, &u)
	if u.Status != model.ReleaseHanded {
		t.Fatalf("移交后应为 handed，实际 %q", u.Status)
	}
	if u.HandedBy != "裁判长C" {
		t.Fatalf("移交人应为裁判长C，实际 %q", u.HandedBy)
	}

	// ---------- 接收 ----------
	ts.do(t, http.MethodPost, "/api/v1/releases/"+itoa(u.ID)+"/receive", nil, "工作人员A").
		expect(t, http.StatusOK).as(t, &u)
	if u.Status != model.ReleasePending {
		t.Fatalf("接收后应为 pending，实际 %q", u.Status)
	}
	if u.PublishLabel != "⏳ 待发布" {
		t.Fatalf("待发布文案不对：%q", u.PublishLabel)
	}

	// ---------- 发布 ----------
	ts.do(t, http.MethodPost, "/api/v1/releases/"+itoa(u.ID)+"/publish", nil, "运营B").
		expect(t, http.StatusOK).as(t, &u)
	if u.Status != model.ReleasePublished {
		t.Fatalf("发布后应为 published，实际 %q", u.Status)
	}
	if u.PublishedBy != "运营B" || u.PublishedAt == nil {
		t.Fatalf("发布人 / 时间应落库：%q / %v", u.PublishedBy, u.PublishedAt)
	}
	if u.PublishLabel != "✓ 运营已发布" {
		t.Fatalf("已发布文案不对：%q", u.PublishLabel)
	}

	// ---------- 标记重发 ----------
	ts.do(t, http.MethodPost, "/api/v1/releases/"+itoa(u.ID)+"/republish",
		map[string]any{"reason": "改分申请已生效"}, "系统").
		expect(t, http.StatusOK).as(t, &u)
	if !u.RepublishRequired {
		t.Fatal("重发标记应为 true")
	}
	if u.PublishLabel != "⏳ 待发布（待重发）" {
		t.Fatalf("重发文案应带「（待重发）」，实际 %q", u.PublishLabel)
	}

	// ---------- P13 看板 ----------
	var board struct {
		Units     []model.ReleaseUnit `json:"units"`
		Total     int                 `json:"total"`
		Counts    map[string]int      `json:"counts"`
		Republish int                 `json:"republish"`
	}
	ts.do(t, http.MethodGet, "/api/v1/releases", nil, "").expect(t, http.StatusOK).as(t, &board)
	if board.Total != 1 {
		t.Fatalf("看板应有 1 个发布单元，实际 %d", board.Total)
	}
	if board.Republish != 1 {
		t.Fatalf("应统计到 1 个待重发，实际 %d", board.Republish)
	}
	if board.Counts["pending"] != 1 || board.Counts["published"] != 0 {
		t.Fatalf("四态计数不对：%+v", board.Counts)
	}

	// ---------- 参数校验 ----------
	ts.do(t, http.MethodPost, "/api/v1/releases/ensure", map[string]any{
		"eventId": ev.ID,
	}, "运营A").expect(t, http.StatusBadRequest) // 缺 groupCode

	ts.do(t, http.MethodPost, "/api/v1/releases/"+itoa(u.ID)+"/republish",
		map[string]any{"reason": "短"}, "系统").expect(t, http.StatusBadRequest)

	// ---------- 不存在的单元 ----------
	ts.do(t, http.MethodGet, "/api/v1/releases/999999", nil, "").expect(t, http.StatusNotFound)
}
