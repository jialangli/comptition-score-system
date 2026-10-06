package service

import (
	"context"
	"fmt"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 留底证据库（0009）
//
// 三条来自 wireframe 的口径（P2e note 7）：
//
//  1. **产生端各不相同**：裁判提交成绩 / 裁判长提交裁定 / 工作人员发布 ——
//     「规范统一、触发点各异」，所以 source 必须能区分。
//  2. **状态一律以「是否已上云」为准**：本地先落、异步同步。
//  3. **作废不抹除证据**：成绩作废只改「是否计入排名」，证据仍完整留存。
//
// ⚠️ 只登记**元数据**，不承载二进制本体 —— 图片本体属于对象存储的职责。
// 后端要回答的是合规追溯真正的问题：这一轮证据齐不齐、谁产生的、上云了没有。
// ============================================================================

// RecordEvidence 登记一条证据。
//
// 重复登记同一条（同队伍同轮次同类型同文件名）返回 ErrDuplicate：
// 补传会重试，不去重会让「证据三件」变成「七件」，反而没法判断齐不齐。
func (s *Service) RecordEvidence(ctx context.Context, e *model.Evidence) (*model.Evidence, error) {
	if e.FileName == "" {
		return nil, ErrReasonRequired // 复用「必填」语义：没有文件名的证据无法定位
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Evidence.Create(ctx, e); err != nil {
			return err
		}
		return log(ctx, r, model.ActEvidence,
			evidenceTarget(e), "", e.Status.Label(),
			e.Source.Label()+"生成："+e.Kind.Label())
	}); err != nil {
		return nil, err
	}
	e.DeriveLabels()
	return e, nil
}

// MarkEvidenceSynced 标记为已上云（断网时先落本地，联网后补传）。
func (s *Service) MarkEvidenceSynced(ctx context.Context, id int64, storageURL string) error {
	return s.tx(ctx, func(r store.Repos) error {
		if err := r.Evidence.MarkSynced(ctx, id, storageURL); err != nil {
			return err
		}
		return log(ctx, r, model.ActEvidence, fmt.Sprintf("证据 #%d", id),
			model.EvLocal.Label(), model.EvSynced.Label(), "补传上云")
	})
}

// TeamEvidence 某队全部证据；round=0 表示不按轮次过滤。
func (s *Service) TeamEvidence(ctx context.Context, teamID int64, round int) ([]model.Evidence, error) {
	return s.ro().Evidence.ListByTeam(ctx, teamID, round)
}

// PendingEvidence 待上云队列：断网时先落本地，联网后据此补传。
func (s *Service) PendingEvidence(ctx context.Context) ([]model.Evidence, error) {
	return s.ro().Evidence.ListPending(ctx)
}

// AppealForDispute 取某争议工单关联的申述书照片（kind=appeal）。
//
// 裁判长裁定台（P8 家族）据此在裁定详情里展示选手手写申述书的原件。
func (s *Service) AppealForDispute(ctx context.Context, disputeID int64) ([]model.Evidence, error) {
	return s.ro().Evidence.GetByDispute(ctx, disputeID)
}

// AppealByID 按证据 ID 读取申述书照片元数据（出图时定位文件用）。
func (s *Service) AppealByID(ctx context.Context, id int64) (*model.Evidence, error) {
	return s.ro().Evidence.Get(ctx, id)
}

// AppealEvidenceIDOfDispute 取某争议工单第一张申述书照片的证据 ID。
//
// 供 P8b「授权改分」生成改分单时调用，把同一张申诉书照片继承到改分单
// （score_change_requests.appeal_evidence_id），实现「两处都挂」。
// 该争议单无申述书时返回 (nil, nil)。
func (s *Service) AppealEvidenceIDOfDispute(ctx context.Context, disputeID int64) (*int64, error) {
	list, err := s.ro().Evidence.GetByDispute(ctx, disputeID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0].ID, nil
}

// InheritAppealToChangeRequest 把争议单的申述书照片挂到改分申请单（两处都挂）。
//
// 在 P8b 生成改分单后调用；appealEvidenceID 来自 AppealEvidenceIDOfDispute。
func (s *Service) InheritAppealToChangeRequest(ctx context.Context, changeRequestID, appealEvidenceID int64) error {
	return s.tx(ctx, func(r store.Repos) error {
		return r.Changes.SetAppeal(ctx, changeRequestID, appealEvidenceID)
	})
}

// EvidenceComplete 判断某队某轮的「证据三件」是否齐全。
//
// 三件 = 成绩表 + 签名图 + 提交留底截图（P2e note）。
// 缺任何一件都不算完成留底 —— 这是发布前 gate 与合规检查要问的问题。
func (s *Service) EvidenceComplete(ctx context.Context, teamID int64,
	round int) (bool, []model.EvidenceKind, error) {
	list, err := s.ro().Evidence.ListByTeam(ctx, teamID, round)
	if err != nil {
		return false, nil, err
	}
	have := make(map[model.EvidenceKind]bool, len(list))
	for _, e := range list {
		have[e.Kind] = true
	}
	var missing []model.EvidenceKind
	for _, k := range model.EvidenceKinds() {
		if !have[k] {
			missing = append(missing, k)
		}
	}
	return len(missing) == 0, missing, nil
}

// evidenceTarget 审计的「操作对象」文案。
func evidenceTarget(e *model.Evidence) string {
	round := "无轮次"
	if e.RoundNo != nil {
		round = fmt.Sprintf("第 %d 轮", *e.RoundNo)
	}
	return fmt.Sprintf("队伍 #%d · %s · %s（%s）", e.TeamID, round, e.Kind.Label(), e.FileName)
}
