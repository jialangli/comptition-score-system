package service

import (
	"context"
	"fmt"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 发布单元与移交 / 发布状态机（0006）
//
// 对应前端 P11「确认并移交」与 P13「发布状态回流看板」：
//
//	未移交 →(裁判长移交)→ 已移交·处理中 →(工作人员接收)→ ⏳ 待发布 →(运营发布)→ ✓ 运营已发布
//	                                                        ↑                          │
//	                                                        └──(改分/裁定生效→标记重发)┘
//
// 状态机由数据库把关（每条 UPDATE 都带允许的前置状态），命中 0 行即视为状态不符，
// 因此并发下两个运营同时点发布只有一个会成功 —— 不会重复发布，也不会静默覆盖。
// ============================================================================

// EnsureReleaseUnit 取（或建）一个发布单元，幂等。
func (s *Service) EnsureReleaseUnit(ctx context.Context, eventID, groupCode string,
	seatID *int64) (*model.ReleaseUnit, error) {
	return s.ro().Releases.Ensure(ctx, eventID, groupCode, seatID)
}

// HandOverRelease 裁判长确认并移交。
//
// 入口按（赛项, 组别, 赛台）定位而不是按 ID：裁判长在 P11 面对的就是这个三元组，
// 让他先去查单元 ID 是平白增加一次往返。
func (s *Service) HandOverRelease(ctx context.Context, eventID, groupCode string,
	seatID *int64) (*model.ReleaseUnit, error) {
	u, err := s.ro().Releases.Ensure(ctx, eventID, groupCode, seatID)
	if err != nil {
		return nil, err
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Releases.HandOver(ctx, u.ID, CurrentUser(ctx)); err != nil {
			return err
		}
		return log(ctx, r, model.ActHandOver, releaseTarget(u),
			u.Status.Label(), model.ReleaseHanded.Label(), "裁判长确认数据完整并移交")
	}); err != nil {
		return nil, err
	}
	return s.ro().Releases.Get(ctx, u.ID)
}

// ReceiveRelease 工作人员接收，进入待发布。
func (s *Service) ReceiveRelease(ctx context.Context, id int64) (*model.ReleaseUnit, error) {
	u, err := s.ro().Releases.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Releases.Receive(ctx, id, CurrentUser(ctx)); err != nil {
			return err
		}
		return log(ctx, r, model.ActReceive, releaseTarget(u),
			u.Status.Label(), model.ReleasePending.Label(), "工作人员接收")
	}); err != nil {
		return nil, err
	}
	return s.ro().Releases.Get(ctx, id)
}

// PublishRelease 运营按下发布键。
//
// 未移交的单元不允许发布（须先移交）—— 裁判长还没确认数据完整就发出去，
// 正是 P11 / P13 这套流程要防的事。状态前置条件在 SQL 里，命中 0 行转 ErrNotFound。
func (s *Service) PublishRelease(ctx context.Context, id int64) (*model.ReleaseUnit, error) {
	u, err := s.ro().Releases.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !u.Status.CanPublish() {
		return nil, ErrReleaseNotPublishable
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Releases.Publish(ctx, id, CurrentUser(ctx)); err != nil {
			return err
		}
		return log(ctx, r, model.ActPublish, releaseTarget(u),
			u.Status.Label(), model.ReleasePublished.Label(), "运营发布")
	}); err != nil {
		return nil, err
	}
	return s.ro().Releases.Get(ctx, id)
}

// MarkRepublish 标记重发：已发布后又发生改分 / 裁定生效。
//
// 状态回退到「待发布」，但**保留上一次的发布人与时间** ——
// 重发期间前端既要提示「待重发」，也要能看到上一版是谁发的（P13 note 5）。
func (s *Service) MarkRepublish(ctx context.Context, id int64, reason string) (*model.ReleaseUnit, error) {
	u, err := s.ro().Releases.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Releases.MarkRepublish(ctx, id, reason); err != nil {
			return err
		}
		return log(ctx, r, model.ActRepublish, releaseTarget(u),
			u.Status.Label(), model.ReleasePending.Label(), reason)
	}); err != nil {
		return nil, err
	}
	return s.ro().Releases.Get(ctx, id)
}

// ReleaseUnits 本赛事全部发布单元（P13 看板数据源）。
func (s *Service) ReleaseUnits(ctx context.Context) ([]model.ReleaseUnit, error) {
	return s.ro().Releases.ListByContest(ctx)
}

// ReleaseUnit 单查一个发布单元。
func (s *Service) ReleaseUnit(ctx context.Context, id int64) (*model.ReleaseUnit, error) {
	return s.ro().Releases.Get(ctx, id)
}

// releaseTarget 审计的「操作对象」文案。
func releaseTarget(u *model.ReleaseUnit) string {
	seat := "未指定赛台"
	if u.SeatID != nil {
		seat = fmt.Sprintf("赛台 #%d", *u.SeatID)
	}
	return fmt.Sprintf("发布单元 %s · %s · %s", u.EventID, u.GroupCode, seat)
}
