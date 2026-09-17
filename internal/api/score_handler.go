package api

import (
	"net/http"
	"strconv"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 打分与改分
//
// 两条路径刻意分开，不做成一个「改分」接口：
//
//	PUT  /teams/{id}/scores/{round}                   裁判录入 / 编辑草稿
//	POST /teams/{id}/scores/{round}/change-requests   裁判发起改分申请（不落数据）
//	POST /teams/{id}/scores/{round}/apply-change      裁判长授权后落库
//
// 如果只有前一个接口，裁判就能自己改自己的分，「需裁判长授权」这句话
// 就只存在于文档里了。
// ============================================================================

// roundParam 取路径里的轮次。
func roundParam(r *http.Request) (int, error) {
	raw := pathStr(r, "round")
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, NewBadRequest("轮次必须是整数，收到 " + raw)
	}
	if v < model.MinRound || v > model.MaxRound {
		return 0, NewBadRequest("轮次只能是 1 或 2")
	}
	return v, nil
}

// handleListScores GET /api/v1/teams/{id}/scores
func (s *Server) handleListScores(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	recs, err := s.svc.ListScores(r.Context(), id)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"scores": recs, "total": len(recs)})
}

// handleGetScore GET /api/v1/teams/{id}/scores/{round}
func (s *Server) handleGetScore(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	round, err := roundParam(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	rec, err := s.svc.GetScore(r.Context(), id, round)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, rec)
}

// handleSaveScore PUT /api/v1/teams/{id}/scores/{round}
//
// 已签字（signed=true）的成绩会被拒绝并返回 409 —— 必须走改分申请。
// 未签字的草稿可以反复编辑，每一次编辑都会留痕。
func (s *Server) handleSaveScore(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	round, err := roundParam(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	var body saveScoreReq
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	rec, err := s.svc.SaveScore(r.Context(), &model.ScoreRecord{
		TeamID:      id,
		RoundNo:     round,
		TaskValues:  body.TaskValues,
		DurationSec: body.Duration,
		Yellow:      body.Yellow,
		Red:         body.Red,
		Signed:      body.Signed,
	})
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, rec)
}

// handleScoreChangeRequest POST /api/v1/teams/{id}/scores/{round}/change-requests
//
// 只写审计、不写成绩。返回体里的 approved 恒为 false，供界面显示「待裁判长授权」。
func (s *Server) handleScoreChangeRequest(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	round, err := roundParam(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	var body scoreChangeReq
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	req, err := s.svc.ScoreChangeRequest(r.Context(), id, round, body.After, body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, req)
}

// handleApplyScoreChange POST /api/v1/teams/{id}/scores/{round}/apply-change
//
// 裁判长授权后落库：必须提供 approver（审批人）与 reason（理由），
// 两者都会写进审计的独立字段。
func (s *Server) handleApplyScoreChange(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	round, err := roundParam(r)
	if err != nil {
		Fail(w, r, err)
		return
	}
	var body applyChangeReq
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}
	rec, err := s.svc.ApplyScoreChange(r.Context(), &model.ScoreRecord{
		TeamID:      id,
		RoundNo:     round,
		TaskValues:  body.TaskValues,
		DurationSec: body.Duration,
		Yellow:      body.Yellow,
		Red:         body.Red,
		Signed:      body.Signed,
	}, body.Approver, body.Reason)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, rec)
}
