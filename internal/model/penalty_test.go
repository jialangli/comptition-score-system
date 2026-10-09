package model

import (
	"encoding/json"
	"testing"
)

// ============================================================================
// 判罚规则与牌面计数规则的 JSON 往返
//
// 背景：早期前端把 cardRules 挂在 penaltyRule 的**顶层**，而 PenaltyRule 只有
// {template, params} 两个字段 —— encoding/json 遇到未知顶层字段会**静默丢弃**，
// 于是「配好的黄牌开关、阈值、红牌事由」一存回库就永久少一块，全程零报错。
//
// 这组用例锁住修复后的行为：cardRules 必须是具名字段，能完整往返，
// 且**显式关闭（false）不能被 omitempty 吃掉**（那会变成"未配置"=默认开启，
// 用户明明关了却还开着）。
// ============================================================================

func TestPenaltyRuleCardRulesRoundTrip(t *testing.T) {
	off := false
	allow := true
	orig := PenaltyRule{
		Template: PenaltyRecordOnly,
		CardRules: &CardRules{
			Enabled:        &off,
			RedThreshold:   5,
			AllowDirectRed: &allow,
			Reasons: []CardReason{
				{Code: "R01", Card: "red", Label: "顶撞裁判"},
				{Code: "R02", Card: "red", Label: "恶意撞击能源轨道"},
			},
		},
	}

	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}

	// 显式 false 必须真的写出去，不能被 omitempty 省略
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("解析探针失败：%v", err)
	}
	cr, ok := probe["cardRules"].(map[string]any)
	if !ok {
		t.Fatalf("序列化结果里没有 cardRules：%s", raw)
	}
	if v, exists := cr["enabled"]; !exists || v != false {
		t.Errorf("enabled=false 必须被写出（否则会被读成「未配置」而默认开启），实际：%v（存在=%v）", v, exists)
	}

	// 往返后必须逐字段一致
	var back PenaltyRule
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	if back.Template != orig.Template {
		t.Errorf("template 往返后变了：%q → %q", orig.Template, back.Template)
	}
	if back.CardRules == nil {
		t.Fatal("cardRules 往返后丢失")
	}
	if back.CardRules.EnabledOrDefault() {
		t.Error("往返后 enabled 应以显式 false 为准，不该回落成默认开启")
	}
	if back.CardRules.ThresholdOrDefault() != 5 {
		t.Errorf("阈值往返后应仍为 5，得到 %d", back.CardRules.ThresholdOrDefault())
	}
	if !back.CardRules.AllowDirectRedOrDefault() {
		t.Error("allowDirectRed 往返后应仍为 true")
	}
	if got := back.CardRules.ReasonLabels("red"); len(got) != 2 {
		t.Errorf("红牌事由往返后应仍为 2 条，得到 %v", got)
	}
}

func TestPenaltyRuleWithoutCardRules(t *testing.T) {
	// 未配置时不写 cardRules（保持历史 JSON 形状，减少无谓 diff）
	raw, err := json.Marshal(PenaltyRule{Template: PenaltyNone})
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("解析探针失败：%v", err)
	}
	if _, exists := probe["cardRules"]; exists {
		t.Errorf("未配置时不应输出 cardRules：%s", raw)
	}

	// 读旧数据（完全没有 cardRules 字段）→ nil，一律按默认值处理
	var back PenaltyRule
	if err := json.Unmarshal([]byte(`{"template":"none"}`), &back); err != nil {
		t.Fatalf("解析旧数据失败：%v", err)
	}
	if back.CardRules != nil {
		t.Error("旧数据里没有 cardRules，应解析为 nil")
	}
	var nilRules *CardRules = back.CardRules
	if !nilRules.EnabledOrDefault() || !nilRules.AllowDirectRedOrDefault() ||
		nilRules.ThresholdOrDefault() != DefaultRedThreshold {
		t.Error("nil 规则应全部取默认值（开 / 允许 / 3 张）")
	}
}

func TestPenaltyTemplateValues(t *testing.T) {
	// 当前口径：只有「仅记录不扣分」是可选档；两个历史值仍须合法（读旧数据）
	if !PenaltyRecordOnly.Valid() {
		t.Error("record_only 应合法")
	}
	if !PenaltyNone.Valid() || !PenaltyPerCard.Valid() || !PenaltyTemplate("").Valid() {
		t.Error("历史值 none / per_card 与空值都应合法")
	}
	if PenaltyTemplate("weird").Valid() {
		t.Error("未知模板不应合法")
	}

	if got := PenaltyRecordOnly.Display(); got != "仅记录不扣分" {
		t.Errorf("record_only 的显示名应为「仅记录不扣分」，得到 %q", got)
	}
	if !PenaltyPerCard.Deprecated() {
		t.Error("per_card 应标记为已下线")
	}
	if PenaltyRecordOnly.Deprecated() || PenaltyNone.Deprecated() {
		t.Error("record_only / none 不应标记为已下线")
	}
}
