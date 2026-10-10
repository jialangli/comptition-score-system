package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 赛事级规则（0019）
//
// 作用域是**赛事**而不是赛项：一条递补规则对所有赛项一视同仁。
// 与「赛项规则」（tasks / scoreRule / penaltyRule…）不是一个层级，别混。
// ============================================================================

// ContestRules 读取当前赛事的规则。
//
// 未设置过时返回**默认口径**（不递补），不是 ErrNotFound：
// 这条规则是"可选覆盖"，绝大多数赛事根本不会去改它，而榜单必须永远能出。
func (s *Service) ContestRules(ctx context.Context) (*model.ContestRules, error) {
	return s.ro().Rules.Get(ctx)
}

// SetSubstituteMode 切换名次编排（= 前端的「资格与递补」页）。
//
// 入参只有 mode + note，而不是收一个 ContestRules 整体：本接口能改的只有递补规则，
// 让调用方传 contestId / updatedAt 只会造成「传了但被静默忽略」的误解。
// 将来再加赛事级规则，给它自己的方法。
func (s *Service) SetSubstituteMode(ctx context.Context, mode model.SubstituteMode,
	note string) (*model.ContestRules, error) {

	if !mode.Valid() {
		return nil, &model.FieldError{
			Field: "substituteMode",
			Msg:   "名次编排只能取 none（不递补）/ rank（按名次顺延）",
		}
	}
	old, err := s.ro().Rules.Get(ctx)
	if err != nil {
		return nil, err
	}

	note = strings.TrimSpace(note)
	if note == "" {
		note = fmt.Sprintf("名次编排改为「%s」", mode.Label())
	}

	// 幂等：同值直接返回，**不写库、不留痕**。
	// 现场"点一下没反应就再点"很常见；不做短路的话台账会被同样的记录刷满，
	// 真出问题时反而找不到那一次真改动。
	if old.SubstituteMode == mode && old.SubstituteNote == note {
		return old, nil
	}

	next := &model.ContestRules{SubstituteMode: mode, SubstituteNote: note}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Rules.Upsert(ctx, next); err != nil {
			return err
		}
		// 留痕写「旧口径 → 新口径」而不是 mode 原始值：
		// 三个月后来查"名次为什么长这样"，要看到的是「不递补」而不是 `none`。
		return log(ctx, r, model.ActConfig, "赛事级规则 · 名次编排",
			old.SubstituteMode.OrDefault().Label(), mode.Label(), note)
	}); err != nil {
		return nil, err
	}
	return next, nil
}
