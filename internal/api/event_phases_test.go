package api_test

// 阶段配置（events.phases）的 HTTP 往返用例。
//
// 单独一个文件而不是并进 api_test.go：它与「迁移 0018 + 基准重建」是同一批改动，
// 放在一起便于回溯那批改了什么；api_test.go 则是队伍 / 场次 / 成绩这些基础生命周期的用例集。

import (
	"net/http"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// TestEventPhasesOverHTTP 阶段配置经 HTTP 往返不丢，且基准时长按阶段之和推导。
//
// 守的是 2026-10-10 修掉的那类「静默丢配置」：events 表原先没有 phases 列，
// 配置存一次阶段就整个消失（零报错），后端随即把时间奖励按默认 120s 而不是
// 阶段之和（225s）来算 —— 成绩偏了却看不出是它。
//
// 服务层已有同口径的存储往返用例，这里补的是**HTTP 编解码这一段**：
// 请求体字段名写错、或 handler 换成了别的 DTO，一样会丢，而那样只有集成测试能发现。
func TestEventPhasesOverHTTP(t *testing.T) {
	ts := newTestServer(t)

	body := brainPlanetBody()
	body["phases"] = []map[string]any{
		{"id": "auto", "name": "自动阶段", "durationSec": 120,
			"timerMode": "countdown", "source": "colorcard", "desc": "刷色卡驱动小车"},
		{"id": "manual", "name": "手动阶段", "durationSec": 105,
			"timerMode": "countdown", "source": "gamepad"},
	}
	ev := ts.createEvent(t, body)
	if len(ev.Phases) != 2 {
		t.Fatalf("建赛项返回的阶段数 = %d，期望 2", len(ev.Phases))
	}

	var got model.Event
	ts.do(t, http.MethodGet, "/api/v1/events/brain_planet", nil, "").expect(t, http.StatusOK).as(t, &got)
	if len(got.Phases) != 2 {
		t.Fatalf("读回的阶段数 = %d，期望 2（请求体里的 phases 没被解析 / 没落库）", len(got.Phases))
	}
	if got.Phases[0].ID != "auto" || got.Phases[0].Source != "colorcard" || got.Phases[0].TimerMode != "countdown" {
		t.Errorf("阶段元信息未完整往返：%+v", got.Phases[0])
	}
	if got.Phases[1].DurationSec != 105 {
		t.Errorf("阶段时长未完整往返：%v", got.Phases[1].DurationSec)
	}
	if s := got.PhaseTotalSec(); s != 225 {
		t.Errorf("阶段总时长 = %v，期望 225（时间奖励的基准时长）", s)
	}
}
