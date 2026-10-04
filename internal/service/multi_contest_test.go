package service_test

// ============================================================================
// 0004 多赛事维度：跨赛事隔离验证
//
// 这一组用例回答的核心问题：**两场赛事的数据会不会互相串**。
//
// 为什么必须专门测：store 层每条 SQL 都靠 ctx 里的 contest_id 做分区，
// 而 contest_id 有默认值 ct_default。也就是说「忘记带赛事标识」不会报错，
// 只会静默读到默认赛事的数据 —— 这类 bug 在单赛事下完全看不出来，
// 必须构造两场赛事才能真正暴露。
//
// 覆盖三条最容易串的路径：
//   1. 队伍：两场赛事可共用同一编号，且各自只能看到自己那场
//   2. 成绩：A 赛事的成绩不会出现在 B 赛事的榜单里
//   3. 赛项：两场赛事可共用同名赛项（brain_planet 等），互不覆盖
// ============================================================================

import (
	"context"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// contestCtx 模拟 api 层中间件：把当前赛事与操作人写进 ctx。
func contestCtx(contestID, operator string) context.Context {
	ctx := service.WithContest(context.Background(), contestID)
	return service.WithUser(ctx, operator)
}

func TestMultiContestIsolation(t *testing.T) {
	svc, db := newSvc(t)
	if err := db.TruncateAll(context.Background()); err != nil {
		t.Fatalf("清空失败: %v", err)
	}

	const (
		ctA = "ct_quzhou"
		ctB = "ct_hangzhou"
	)
	ctxA := contestCtx(ctA, "运营A")
	ctxB := contestCtx(ctB, "运营B")

	// ---- ① 先建两场赛事 ----
	// 外键要求：所有业务表都指向 contests(id)，建赛项之前必须先有赛事。
	// 这层 RESTRICT 是刻意护栏 —— 不允许出现「成绩挂在一条不存在的赛事上」。
	if err := db.Repos().Contests.EnsureDefault(context.Background()); err != nil {
		t.Fatalf("重建默认赛事失败: %v", err)
	}
	for _, c := range []*model.Contest{
		{ID: ctA, Name: "衢州选拔赛", Status: model.ContestLive},
		{ID: ctB, Name: "杭州总决赛", Status: model.ContestLive},
	} {
		if err := db.Repos().Contests.Create(context.Background(), c); err != nil {
			t.Fatalf("建赛事 %s 失败: %v", c.ID, err)
		}
	}

	// ---- ② 两场赛事各自建立同名赛项 ----
	evA := brainPlanetEvent()
	if _, err := svc.CreateEvent(ctxA, evA); err != nil {
		t.Fatalf("A 场建赛项失败: %v", err)
	}
	evB := brainPlanetEvent()
	if _, err := svc.CreateEvent(ctxB, evB); err != nil {
		t.Fatalf("B 场建赛项失败（同名赛项应可共存）: %v", err)
	}

	// 赛项各归各：A 场只应看到自己那份
	eventsA, err := svc.ListEvents(ctxA)
	if err != nil {
		t.Fatalf("A 场列赛项失败: %v", err)
	}
	if len(eventsA) != 1 {
		t.Fatalf("A 场应只见 1 个赛项，实为 %d", len(eventsA))
	}

	// ---- ③ 两场赛事建立同编号队伍（核心语义：编号只在赛事内唯一）----
	teamA, err := svc.CreateTeam(ctxA, model.TeamDraft{
		EventID: "brain_planet", TeamNo: "1001", Name: "追光队",
		School: "杭州实验小学", GroupCode: "小学组", Source: model.SourceManual,
	})
	if err != nil {
		t.Fatalf("A 场建队失败: %v", err)
	}
	teamB, err := svc.CreateTeam(ctxB, model.TeamDraft{
		EventID: "brain_planet", TeamNo: "1001", Name: "星河队",
		School: "青岛实验学校", GroupCode: "小学组", Source: model.SourceManual,
	})
	if err != nil {
		t.Fatalf("B 场建队失败（同编号应可共存）: %v", err)
	}

	if teamA.ContestID != ctA {
		t.Errorf("A 队 contest_id 应为 %s，实为 %s", ctA, teamA.ContestID)
	}
	if teamB.ContestID != ctB {
		t.Errorf("B 队 contest_id 应为 %s，实为 %s", ctB, teamB.ContestID)
	}

	// 各自只能看到自己那场，且名字不能串
	listA, err := svc.ListTeams(ctxA, "brain_planet", true)
	if err != nil {
		t.Fatalf("A 场列队失败: %v", err)
	}
	listB, err := svc.ListTeams(ctxB, "brain_planet", true)
	if err != nil {
		t.Fatalf("B 场列队失败: %v", err)
	}
	if len(listA) != 1 {
		t.Fatalf("A 场应只见 1 队，实为 %d", len(listA))
	}
	if len(listB) != 1 {
		t.Fatalf("B 场应只见 1 队，实为 %d", len(listB))
	}
	if listA[0].Name != "追光队" {
		t.Errorf("A 场队名串了：%s", listA[0].Name)
	}
	if listB[0].Name != "星河队" {
		t.Errorf("B 场队名串了：%s", listB[0].Name)
	}

	// ---- ④ 成绩不跨赛事：给 A 场录分，B 场不应看到 ----
	if _, err := svc.SaveScore(ctxA, &model.ScoreRecord{
		TeamID:      teamA.ID,
		RoundNo:     1,
		TaskValues:  map[string]any{"focus": 90.0, "build": 88.0},
		DurationSec: 95,
		Signed:      true,
		Operator:    "裁判A",
	}); err != nil {
		t.Fatalf("A 场录分失败: %v", err)
	}

	// 用同一个 teamID（两场赛事自增 ID 会撞）验证：B 场读不到 A 场那份成绩
	if _, err := svc.GetScore(ctxB, teamA.ID, 1); err == nil {
		t.Errorf("B 场不应能读到 A 场的成绩（跨赛事隔离失效）")
	}
	scoresA, err := svc.ListScores(ctxA, teamA.ID)
	if err != nil {
		t.Fatalf("A 场取成绩失败: %v", err)
	}
	if len(scoresA) == 0 {
		t.Errorf("A 场应能看到自己那条成绩")
	}

	// ---- ⑤ 默认赛事兜底：不带赛事标识时落到 ct_default，不与 A/B 混淆 ----
	ctxDefault := contestCtx(store.DefaultContestID, "运营默认")
	listD, err := svc.ListTeams(ctxDefault, "brain_planet", true)
	if err != nil {
		t.Fatalf("默认场列队失败: %v", err)
	}
	if len(listD) != 0 {
		t.Errorf("默认场不应看到 A/B 的队伍，实为 %d", len(listD))
	}

	// ---- ⑥ 跨赛事按 ID 读取应拿不到（隔离失效的最后一道检查）----
	if _, err := svc.GetTeam(ctxB, teamA.ID); err == nil {
		t.Errorf("B 场不应能通过 ID 读到 A 场的队伍（隔离失效）")
	}
}
