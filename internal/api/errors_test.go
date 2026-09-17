package api_test

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/api"
	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 参数与错误路径
//
// 主流程测试证明「对的路走得通」，这里把每条岔路走一遍。
// handler 里未被主流程覆盖的语句基本都是同一种形状：
//
//	if err != nil { Fail(w, r, err); return }
//
// 它们不写就会长期无人验证 —— 而「参数写错返回 500」正是最容易被现场
// 抱怨、又最难复现的一类问题。
// ============================================================================

func TestHandlerErrorPaths(t *testing.T) {
	ts := newTestServer(t)
	ev := ts.createEvent(t, brainPlanetBody())
	team := ts.createTeam(t, ev.ID, "1001", "星河队", "小学组")

	seat := ts.do(t, http.MethodPost, "/api/v1/seats", map[string]any{"name": "赛台 1"}, "运营A")
	var seatObj model.Seat
	seat.expect(t, http.StatusCreated).as(t, &seatObj)

	slot := ts.do(t, http.MethodPost, "/api/v1/slots", map[string]any{
		"seatId": seatObj.ID, "period": "上午", "eventId": ev.ID,
		"group": "小学组", "type": "normal",
	}, "运营A")
	var slotObj model.Slot
	slot.expect(t, http.StatusCreated).as(t, &slotObj)

	extra := ts.do(t, http.MethodPost, "/api/v1/slots", map[string]any{
		"seatId": seatObj.ID, "period": "下午", "eventId": ev.ID,
		"group": "小学组", "type": "extra",
	}, "运营A")
	var extraObj model.Slot
	extra.expect(t, http.StatusCreated).as(t, &extraObj)

	cases := []struct {
		name   string
		method string
		path   string
		body   any
		want   int
	}{
		// —— 查询参数非法 ——
		{"审计 limit 非数字", http.MethodGet, "/api/v1/audit-logs?limit=abc", nil, http.StatusBadRequest},
		{"审计 offset 非数字", http.MethodGet, "/api/v1/audit-logs?offset=x", nil, http.StatusBadRequest},
		{"审计 until 格式错", http.MethodGet, "/api/v1/audit-logs?until=昨天", nil, http.StatusBadRequest},
		{"导入日志 limit 非数字", http.MethodGet, "/api/v1/import-logs?limit=abc", nil, http.StatusBadRequest},
		{"快照 limit 非数字", http.MethodGet, "/api/v1/config-snapshots?limit=abc", nil, http.StatusBadRequest},
		{"大屏 page 非数字", http.MethodGet, "/api/v1/screen/" + ev.ID + "?page=abc", nil, http.StatusBadRequest},
		{"场次筛选 seat 非数字", http.MethodGet, "/api/v1/slots?seat=abc", nil, http.StatusBadRequest},

		// —— 路径参数非法 ——
		{"快照回滚 sid 非数字", http.MethodPost, "/api/v1/config-snapshots/abc/restore", nil, http.StatusBadRequest},
		{"赛台 id 非数字", http.MethodPut, "/api/v1/seats/abc", map[string]any{"name": "x"}, http.StatusBadRequest},

		// —— 资源不存在 ——
		{"回滚不存在的快照", http.MethodPost, "/api/v1/config-snapshots/999999/restore", nil, http.StatusNotFound},
		{"改不存在的赛台", http.MethodPut, "/api/v1/seats/999999", map[string]any{"name": "x"}, http.StatusNotFound},
		{"删不存在的场次", http.MethodDelete, "/api/v1/slots/999999", nil, http.StatusNotFound},
		{"自动分配不存在的场次", http.MethodPost, "/api/v1/slots/999999/auto-assign", nil, http.StatusNotFound},
		{"对不存在场次改派（写操作必须报错）", http.MethodPost, "/api/v1/slots/999999/teams", map[string]any{"teamIds": []int64{1}}, http.StatusNotFound},
		{"读不存在队伍的某轮成绩", http.MethodGet, "/api/v1/teams/999999/scores/1", nil, http.StatusNotFound},

		// —— 请求体问题 ——
		{"建赛台未知字段", http.MethodPost, "/api/v1/seats", map[string]any{"name": "x", "oops": 1}, http.StatusBadRequest},
		{"建场次未知字段", http.MethodPost, "/api/v1/slots", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"改赛台未知字段", http.MethodPut, "/api/v1/seats/" + itoa(seatObj.ID), map[string]any{"oops": 1}, http.StatusBadRequest},
		{"录分未知字段", http.MethodPut, "/api/v1/teams/" + itoa(team.ID) + "/scores/1", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"改分申请未知字段", http.MethodPost, "/api/v1/teams/" + itoa(team.ID) + "/scores/1/change-requests", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"授权改分未知字段", http.MethodPost, "/api/v1/teams/" + itoa(team.ID) + "/scores/1/apply-change", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"导入预览未知字段", http.MethodPost, "/api/v1/imports/preview", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"导入提交未知字段", http.MethodPost, "/api/v1/imports/commit", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"批量上行未知字段", http.MethodPost, "/api/v1/sync", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"大屏配置未知字段", http.MethodPut, "/api/v1/screen/" + ev.ID + "/config", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"校验接口未知字段", http.MethodPost, "/api/v1/events/" + ev.ID + "/validate", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"建队伍未知字段", http.MethodPost, "/api/v1/events/" + ev.ID + "/teams", map[string]any{"oops": 1}, http.StatusBadRequest},
		{"改队伍未知字段", http.MethodPut, "/api/v1/teams/" + itoa(team.ID), map[string]any{"oops": 1}, http.StatusBadRequest},
		{"删队伍未知字段", http.MethodDelete, "/api/v1/teams/" + itoa(team.ID), map[string]any{"oops": 1}, http.StatusBadRequest},

		// —— 业务约束 ——
		{"加时赛场次不接受主库改派", http.MethodPost, "/api/v1/slots/" + itoa(extraObj.ID) + "/teams",
			map[string]any{"teamIds": []int64{team.ID}}, http.StatusBadRequest},
		{"自动分配不能用于加时赛", http.MethodPost, "/api/v1/slots/" + itoa(extraObj.ID) + "/auto-assign", nil, http.StatusBadRequest},
		{"加时赛快照编号非法", http.MethodPost, "/api/v1/slots/" + itoa(extraObj.ID) + "/snapshot",
			map[string]any{"snapshot": []map[string]any{{"no": "abc", "name": "x"}}}, http.StatusBadRequest},
		{"普通场次写快照", http.MethodPost, "/api/v1/slots/" + itoa(slotObj.ID) + "/snapshot",
			map[string]any{"snapshot": []map[string]any{{"no": "9001", "name": "x"}}}, http.StatusBadRequest},

		// —— 缺原因的操作 ——
		{"删除有场次的赛台缺原因", http.MethodDelete, "/api/v1/seats/" + itoa(seatObj.ID), nil, http.StatusBadRequest},
		{"弃赛缺原因", http.MethodPost, "/api/v1/teams/" + itoa(team.ID) + "/withdraw", map[string]any{"reason": ""}, http.StatusBadRequest},
		{"改组缺原因", http.MethodPut, "/api/v1/teams/" + itoa(team.ID),
			map[string]any{"name": "星河队", "group": "初中组"}, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts.do(t, tc.method, tc.path, tc.body, "运营A").expect(t, tc.want)
		})
	}
}

// TestRouteNotFound 未知的接口路径必须返回**JSON** 404。
//
// 若落到静态资源兜底，会返回 Go 标准的纯文本 "404 page not found"，
// 前端 JSON.parse 直接抛异常，现场会误判成网络问题。
func TestRouteNotFound(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{
		"/api/v1/不存在的端点",
		"/api/v1/events/x/不存在",
		"/api/v1/teams/1/不存在",
	} {
		// do 内部会强制解析 JSON —— 非 JSON 响应会直接 Fatal，正是我们要防的
		r := ts.do(t, http.MethodGet, path, nil, "")
		if r.Status != http.StatusNotFound {
			t.Errorf("%s 应返回 404，实际 %d（message=%q）", path, r.Status, r.Msg)
		}
		if r.Code != api.CodeNotFound {
			t.Errorf("%s 业务码应为 %d，实际 %d", path, api.CodeNotFound, r.Code)
		}
		if r.Msg == "" {
			t.Errorf("%s 应给出可读的错误说明", path)
		}
	}
}

// TestSubCollectionOfMissingParent 记录一个刻意的契约选择：
// 「读子集合」类接口对不存在的父资源返回 200 + 空集合，而不是 404。
//
// 理由：这类接口的语义是「该父资源下的集合」，父资源不存在时集合就是空的，
// 与「集合为空」无法也不需要区分。写操作（改派、写入）则必须报 404 ——
// 那里区分得清，也必须区分。
func TestSubCollectionOfMissingParent(t *testing.T) {
	ts := newTestServer(t)

	var scores struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/teams/999999/scores", nil, "").
		expect(t, http.StatusOK).as(t, &scores)
	if scores.Total != 0 {
		t.Errorf("不存在的队伍应返回空成绩列表，实际 %d 条", scores.Total)
	}

	var snaps struct {
		Total int `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/slots/999999/snapshot", nil, "").
		expect(t, http.StatusOK).as(t, &snaps)
	if snaps.Total != 0 {
		t.Errorf("不存在的场次应返回空快照列表，实际 %d 条", snaps.Total)
	}
}
