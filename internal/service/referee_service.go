package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 裁判码（0007）
//
// 前端 P1 家族的登录链路：姓名 + 6 位裁判码双因子，
// 校验通过后按后台预绑的「赛项 × 组别 × 赛台」自动绑定执裁范围。
//
// 本期**不签发会话**：鉴权仍是插槽（api.CurrentUser），
// 这里只负责回答「这个人是谁、执裁范围是什么」。
// ============================================================================

// refereeCharset 无歧义字符集。
//
// 剔除 0/O、1/I/L 等易混字符 —— 裁判码是**口头传达 + 手输**的凭证
// （工作人员赛前建档后告诉裁判），字符集里留易混字符等于凭空制造登录失败。
const refereeCharset = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// refereeCodeLen 裁判码长度（前端 P1 明确为 6 位）。
const refereeCodeLen = 6

// generateRefereeCode 用 crypto/rand 生成 6 位无歧义码。
//
// 用 crypto/rand 而不是 math/rand：裁判码是凭据，可预测就等于没有。
func generateRefereeCode() (string, error) {
	max := big.NewInt(int64(len(refereeCharset)))
	out := make([]byte, refereeCodeLen)
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("生成裁判码失败: %w", err)
		}
		out[i] = refereeCharset[v.Int64()]
	}
	return string(out), nil
}

// IssueRefereeCode 赛前建档：生成裁判码并预绑执裁范围。
//
// 执裁范围与身份（裁判长 / 裁判）在建档时一次写入，登录时只读返回 ——
// 现场可以互换平板，但执裁范围固定，错评由实际签字裁判负责。
//
// 码撞车时重试：ux_referee_code 是数据库兜底，这里重试几次是为了不让
// 「极小概率的撞车」变成用户可见的报错。
func (s *Service) IssueRefereeCode(ctx context.Context, name string, role model.RefereeRole,
	eventID, groupCode string, seatID *int64) (*model.RefereeCode, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrReasonRequired // 复用「必填」语义：建档必须给姓名
	}
	if role != model.RoleReferee && role != model.RoleChief {
		role = model.RoleReferee
	}

	c := &model.RefereeCode{
		Name: name, Role: role,
		EventID: eventID, GroupCode: groupCode, SeatID: seatID,
		Status: model.RefereeUnused,
	}

	const attempts = 5
	var err error
	for i := 0; i < attempts; i++ {
		if c.Code, err = generateRefereeCode(); err != nil {
			return nil, err
		}
		err = s.tx(ctx, func(r store.Repos) error {
			if e := r.Referees.Create(ctx, c); e != nil {
				return e
			}
			return log(ctx, r, model.ActRefereeIssue,
				"裁判码 "+c.Code+" · "+c.Name, "", model.RefereeUnused.Label(),
				fmt.Sprintf("建档并预绑 %s", c.ScopeText(eventID, seatNameOf(seatID))))
		})
		if err == nil {
			return c, nil
		}
		if !errors.Is(err, store.ErrDuplicate) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("连续 %d 次生成的裁判码都撞车，请重试", attempts)
}

// ActivateReferee 首登联网激活：校验姓名 + 裁判码，返回预绑的执裁范围。
//
// 失败必须能**区分三种原因**，前端按此分流到不同页面：
//
//	码查不到      → ErrRefereeCodeInvalid  （P1.5b 裁判码无效页）
//	码存在但姓名不符 → ErrRefereeNameMismatch（P1.5c 姓名不匹配页）
//	码已作废      → ErrRefereeRevoked
//
// 统一成「登录失败」就没法分流了 —— 这两种失败给裁判的补救动作完全不同
// （前者要核对码，后者要确认自己是不是拿错了别人的码）。
//
// 已激活的码再次激活是**幂等成功**：换平板、重装 App 都要能重新激活，
// 否则裁判换台设备就再也登不进去。
func (s *Service) ActivateReferee(ctx context.Context, code, name string) (*model.RefereeCode, error) {
	code = strings.TrimSpace(code)
	name = strings.TrimSpace(name)
	if code == "" || name == "" {
		return nil, ErrRefereeCodeInvalid
	}

	c, err := s.ro().Referees.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrRefereeCodeInvalid
		}
		return nil, err
	}
	if c.Status == model.RefereeRevoked {
		return nil, ErrRefereeRevoked
	}
	if !strings.EqualFold(c.Name, name) {
		return nil, ErrRefereeNameMismatch
	}

	// 已激活过（换平板 / 重装）：直接返回绑定信息，不重复留痕
	if c.Status == model.RefereeActivated {
		return c, nil
	}

	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Referees.MarkActivated(ctx, c.ID); err != nil {
			return err
		}
		return log(ctx, r, model.ActRefereeActivate, "裁判码 "+c.Code+" · "+c.Name,
			c.Status.Label(), model.RefereeActivated.Label(), "首登联网激活")
	}); err != nil {
		return nil, err
	}
	// 回填内存里的状态：激活时间由数据库 now() 写入，这里补一个本地时间
	// 让调用方（与平板端缓存）拿到完整的档案，不必再回查一次。
	now := time.Now()
	c.Status, c.ActivatedAt = model.RefereeActivated, &now
	return c, nil
}

// RefereeCodes 本赛事全部裁判码（含激活状态）。
func (s *Service) RefereeCodes(ctx context.Context) ([]model.RefereeCode, error) {
	return s.ro().Referees.ListByContest(ctx)
}

// seatNameOf 建档日志里的赛台文案（没有名字就退回 ID）。
func seatNameOf(seatID *int64) string {
	if seatID == nil {
		return ""
	}
	return fmt.Sprintf("赛台 #%d", *seatID)
}
