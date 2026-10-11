package api_test

// ============================================================================
// 执裁排班端点：PUT /api/v1/slots/{id}/judges（0021）
//
// service 层已验证口径，这里验证「接线」：
//  1. 路由挂上了（不是 404 / 410）
//  2. GET /slots 回读 judges（读写闭环走 HTTP）
//  3. 不存在的裁判 → 400 可读报错；未知字段 → 400（严格模式）
// ============================================================================

import (
	"net/http"
	"strconv"
	"testing"
)

// TestSlotJudgesEndpoint 主链路：建场次 → PUT 排班 → GET 读回 → 非法裁判 400。
func TestSlotJudgesEndpoint(t *testing.T) {
	ts := newTestServer(t)

	ev := ts.createEvent(t, brainPlanetBody())

	var seat modelSeat
	ts.do(t, http.MethodPost, "/api/v1/seats", map[string]any{"name": "1 号台", "sortOrder": 1}, "运营A").
		expect(t, http.StatusCreated).as(t, &seat)

	var slot modelSlot
	ts.do(t, http.MethodPost, "/api/v1/slots", map[string]any{
		"seatId": seat.ID, "period": "上午", "time": "09:00–12:00",
		"eventId": ev.ID, "group": "小学组", "type": "normal", "round": 1,
	}, "运营A").expect(t, http.StatusCreated).as(t, &slot)

	// 建档一个裁判（拿到档案 ID）；响应包了一层 {code:{...}}（码+执裁范围+状态）
	var refWrap struct {
		Code struct {
			ID   int64  `json:"id"`
			Code string `json:"code"`
		} `json:"code"`
	}
	ts.do(t, http.MethodPost, "/api/v1/referee-codes", map[string]any{
		"name": "裁判A", "role": "referee", "eventId": ev.ID, "groupCode": "小学组", "seatId": seat.ID,
	}, "运营A").expect(t, http.StatusCreated).as(t, &refWrap)
	refID := refWrap.Code.ID

	// 不存在的场次 → 404
	ts.do(t, http.MethodPut, "/api/v1/slots/999999/judges", map[string]any{
		"judges": []int64{refID},
	}, "运营A").expect(t, http.StatusNotFound)

	// PUT 排班：整体替换 + 去重
	var updated modelSlot
	slotPath := "/api/v1/slots/" + strconv.FormatInt(slot.ID, 10) + "/judges"
	ts.do(t, http.MethodPut, slotPath, map[string]any{
		"judges": []int64{refID, refID},
		"reason": "首场排班",
	}, "运营A").expect(t, http.StatusOK).as(t, &updated)
	if len(updated.Judges) != 1 || updated.Judges[0] != refID {
		t.Errorf("排班应去重落库: %+v", updated.Judges)
	}

	// GET /slots 读回（读写闭环）
	var list struct {
		Slots []modelSlot `json:"slots"`
		Total int         `json:"total"`
	}
	ts.do(t, http.MethodGet, "/api/v1/slots", nil, "运营A").expect(t, http.StatusOK).as(t, &list)
	if list.Total != 1 || len(list.Slots[0].Judges) != 1 || list.Slots[0].Judges[0] != refID {
		t.Errorf("GET /slots 应读回排班: total=%d %+v", list.Total, list.Slots)
	}

	// 不存在的裁判 → 400，报错指回裁判档案
	r := ts.do(t, http.MethodPut, slotPath, map[string]any{
		"judges": []int64{424242},
	}, "运营A")
	if r.Status != http.StatusBadRequest {
		t.Errorf("不存在的裁判应 400，实为 %d：%s", r.Status, r.Msg)
	}

	// 严格模式：未知字段整条 400
	ts.do(t, http.MethodPut, slotPath, map[string]any{
		"judges": []int64{refID}, "what": 1,
	}, "运营A").expect(t, http.StatusBadRequest)
}

// —— 解包用的最小结构（与 model 对齐字段名）——

type modelSeat struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type modelSlot struct {
	ID     int64   `json:"id"`
	Judges []int64 `json:"judges"`
}
