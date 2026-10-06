package service_test

import (
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 申述书照片（appeal，0010）集成测试
//
// 覆盖：登记（EvAppeal + SrcRefereeAppeal）→ 按争议单取回 → 按 ID 取回 →
// 解析关联改分单所需的证据 ID → 继承到改分单（两处都挂）。
//
// 打到真实 PG，连不上时跳过（见 integration_test.go 的 openTestDB）。
// ============================================================================

func intPtr(v int) *int { return &v }

// TestAppealEvidenceRecordAndQuery 主链路：登记 → 多视角取回 → ID 解析。
func TestAppealEvidenceRecordAndQuery(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9101", "申述队")
	ctx := operatorCtx("裁判A")

	d, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeOther, "对判罚有异议，提交手写申述书")
	if err != nil {
		t.Fatalf("上报争议失败: %v", err)
	}

	e, err := svc.RecordEvidence(ctx, &model.Evidence{
		TeamID:     teamID,
		RoundNo:    intPtr(1),
		Kind:       model.EvAppeal,
		Source:     model.SrcRefereeAppeal,
		FileName:   "appeal_T9101_R1.jpg",
		Status:     model.EvLocal,
		Operator:   "裁判A",
		DisputeID:  &d.ID,
		StorageURL: "appeal_1_1700000000000000000.jpg",
	})
	if err != nil {
		t.Fatalf("登记申述书证据失败: %v", err)
	}
	if e.ID == 0 {
		t.Fatal("证据 ID 应被回填")
	}
	if e.KindLabel != "申述书照片" || e.SourceLabel != "裁判拍照上传(申述书)" {
		t.Fatalf("标签未填充：kind=%q source=%q", e.KindLabel, e.SourceLabel)
	}

	// 按争议单取回
	list, err := svc.AppealForDispute(ctx, d.ID)
	if err != nil {
		t.Fatalf("按争议单取申述书失败: %v", err)
	}
	if len(list) != 1 || list[0].ID != e.ID {
		t.Fatalf("该争议单应有 1 张申述书且 ID 匹配，实际 %+v", list)
	}

	// 按 ID 取回
	got, err := svc.AppealByID(ctx, e.ID)
	if err != nil {
		t.Fatalf("按 ID 取申述书失败: %v", err)
	}
	if got.StorageURL != "appeal_1_1700000000000000000.jpg" {
		t.Errorf("StorageURL 未正确往返：%q", got.StorageURL)
	}

	// 解析出关联改分单要用的证据 ID
	id, err := svc.AppealEvidenceIDOfDispute(ctx, d.ID)
	if err != nil {
		t.Fatalf("解析申述书证据 ID 失败: %v", err)
	}
	if id == nil || *id != e.ID {
		t.Fatalf("AppealEvidenceIDOfDispute 应返回该证据 ID，实际 %v", id)
	}

	// 无申述书的争议单返回 (nil, nil)
	none, err := svc.AppealEvidenceIDOfDispute(ctx, d.ID+9999)
	if err != nil {
		t.Fatalf("查无申述书应不报错: %v", err)
	}
	if none != nil {
		t.Errorf("无申述书的争议单应返回 nil，实际 %v", none)
	}
}

// TestAppealInheritToChangeRequest 两处都挂：申述书同时挂争议单与改分单。
//
// 模拟 P8b「授权改分」生成改分单后把同一张申述书继承过去：
// 争议单的 dispute_id 不搬走，改分单新增 appeal_evidence_id 指向同一行。
func TestAppealInheritToChangeRequest(t *testing.T) {
	svc, _ := newSvc(t)
	teamID := newDisputeFixture(t, svc, "9102", "申述改分队")
	ctx := operatorCtx("裁判A")

	d, err := svc.ReportDispute(ctx, teamID, 1, model.DisputeOther, "申诉复核，要求重新核算第 1 轮成绩")
	if err != nil {
		t.Fatalf("上报争议失败: %v", err)
	}

	e, err := svc.RecordEvidence(ctx, &model.Evidence{
		TeamID:     teamID,
		Kind:       model.EvAppeal,
		Source:     model.SrcRefereeAppeal,
		FileName:   "appeal_9102.jpg",
		Status:     model.EvLocal,
		Operator:   "裁判A",
		DisputeID:  &d.ID,
		StorageURL: "appeal_2_1700000000000000000.jpg",
	})
	if err != nil {
		t.Fatalf("登记申述书失败: %v", err)
	}

	// 改分单要求先有成绩
	if _, err := svc.SaveScore(ctx, &model.ScoreRecord{
		TeamID: teamID, RoundNo: 1, DurationSec: 100, Signed: true,
		TaskValues: map[string]any{"focus": 80.0, "build": 82.0},
	}); err != nil {
		t.Fatalf("录成绩失败: %v", err)
	}

	// 生成改分申请（P8b 真正落地处会调用；此处验证「两处都挂」接线）
	cr, err := svc.ScoreChangeRequest(ctx, teamID, 1, 95.0, "申诉复核：要求重新核算第 1 轮成绩")
	if err != nil {
		t.Fatalf("生成改分申请失败: %v", err)
	}
	if cr.ID == 0 {
		t.Fatal("改分单 ID 应被回填")
	}

	// 继承：同一张申述书挂到改分单
	if err := svc.InheritAppealToChangeRequest(ctx, cr.ID, e.ID); err != nil {
		t.Fatalf("继承申述书到改分单失败: %v", err)
	}

	// 读回改分单，确认 appeal_evidence_id 已写入
	got, err := svc.PendingChangeOf(ctx, teamID, 1)
	if err != nil {
		t.Fatalf("读回改分单失败: %v", err)
	}
	if got.AppealEvidenceID == nil {
		t.Fatal("改分单应已关联申述书证据 ID")
	}
	if *got.AppealEvidenceID != e.ID {
		t.Errorf("改分单关联的证据 ID 应为 %d，实际 %d", e.ID, *got.AppealEvidenceID)
	}

	// 争议单侧仍挂着同一张（两处都挂，不搬走）
	list, err := svc.AppealForDispute(ctx, d.ID)
	if err != nil {
		t.Fatalf("查争议单申述书失败: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("争议单侧申述书应仍为 1 张，实际 %d", len(list))
	}
}
