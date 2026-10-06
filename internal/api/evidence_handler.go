package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 留底证据库（0009）
//
// 只登记**元数据**，不收二进制本体 —— 图片本体属于对象存储的职责。
// 本组接口回答的是合规追溯真正的问题：这一轮证据齐不齐、谁产生的、上云了没有。
// ============================================================================

// handleRecordEvidence POST /api/v1/evidence
//
// 请求体：{ "teamId": 12, "roundNo": 1, "kind": "signature", "source": "referee_submit",
//
//	"fileName": "T-001_R1_1240.jpg", "operator": "张老师", "seatId": 1 }
//
// kind: score_sheet 成绩表 / signature 签名图 / submit_snapshot 提交留底截图 /
//
//	decision 裁定单 / release 发布产物
//
// source: referee_submit 裁判提交 / chief_decide 裁判长裁定 / staff_publish 工作人员发布
func (s *Server) handleRecordEvidence(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TeamID    int64  `json:"teamId"`
		RoundNo   *int   `json:"roundNo"`
		Kind      string `json:"kind"`
		Source    string `json:"source"`
		FileName  string `json:"fileName"`
		Operator  string `json:"operator"`
		SeatID    *int64 `json:"seatId"`
		DisputeID *int64 `json:"disputeId"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		Fail(w, r, err)
		return
	}
	if req.TeamID <= 0 {
		Fail(w, r, NewBadRequest("teamId 必须为正整数"))
		return
	}
	kind := model.EvidenceKind(strings.TrimSpace(req.Kind))
	switch kind {
	case model.EvScoreSheet, model.EvSignature, model.EvSubmitSnap,
		model.EvDecision, model.EvRelease:
	default:
		Fail(w, r, NewBadRequest(
			"kind 必须是 score_sheet / signature / submit_snapshot / decision / release，收到 "+req.Kind))
		return
	}
	source := model.EvidenceSource(strings.TrimSpace(req.Source))
	switch source {
	case model.SrcRefereeSubmit, model.SrcChiefDecide, model.SrcStaffPublish:
	default:
		Fail(w, r, NewBadRequest(
			"source 必须是 referee_submit / chief_decide / staff_publish，收到 "+req.Source))
		return
	}
	if req.RoundNo != nil && (*req.RoundNo != 1 && *req.RoundNo != 2) {
		Fail(w, r, NewBadRequest("roundNo 只能是 1 或 2，或省略"))
		return
	}

	e := &model.Evidence{
		TeamID: req.TeamID, RoundNo: req.RoundNo, Kind: kind, Source: source,
		FileName: strings.TrimSpace(req.FileName),
		Status:   model.EvLocal, // 一律先落本地，联网后补传再置 synced
		Operator: req.Operator, SeatID: req.SeatID, DisputeID: req.DisputeID,
	}
	out, err := s.svc.RecordEvidence(r.Context(), e)
	if err != nil {
		Fail(w, r, err)
		return
	}
	Created(w, out)
}

// handleMarkEvidenceSynced POST /api/v1/evidence/{id}/synced
//
// 请求体：{ "storageUrl": "..." }（可省略，省略表示沿用已有地址）
func (s *Server) handleMarkEvidenceSynced(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt64(r, "id")
	if err != nil {
		Fail(w, r, err)
		return
	}
	var req struct {
		StorageURL string `json:"storageUrl"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		Fail(w, r, err)
		return
	}
	if err := s.svc.MarkEvidenceSynced(r.Context(), id, req.StorageURL); err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"id": id, "status": model.EvSynced})
}

// handleListEvidence GET /api/v1/evidence?teamId=12&round=1
//
// round 省略（或 0）表示不按轮次过滤 —— 发布 / 裁定类证据不挂在具体轮次上。
func (s *Server) handleListEvidence(w http.ResponseWriter, r *http.Request) {
	teamID, err := queryInt64(r, "teamId")
	if err != nil {
		Fail(w, r, err)
		return
	}
	round := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("round")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 2 {
			Fail(w, r, NewBadRequest("round 只能是 1 或 2，收到 "+raw))
			return
		}
		round = v
	}
	list, err := s.svc.TeamEvidence(r.Context(), teamID, round)
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"evidence": list, "total": len(list)})
}

// handleListPendingEvidence GET /api/v1/evidence/pending
//
// 待上云队列：断网时先落本地，联网后据此补传。
func (s *Server) handleListPendingEvidence(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.PendingEvidence(r.Context())
	if err != nil {
		Fail(w, r, err)
		return
	}
	OK(w, map[string]any{"evidence": list, "total": len(list)})
}

// handleEvidenceComplete GET /api/v1/evidence/complete?teamId=12&round=1
//
// 「证据三件」是否齐全（成绩表 + 签名图 + 提交留底截图）。
// 缺任何一件都不算完成留底 —— 发布前 gate 与合规检查要问的就是这个。
func (s *Server) handleEvidenceComplete(w http.ResponseWriter, r *http.Request) {
	teamID, err := queryInt64(r, "teamId")
	if err != nil {
		Fail(w, r, err)
		return
	}
	round, err := queryInt(r, "round", 0)
	if err != nil {
		Fail(w, r, err)
		return
	}
	ok, missing, err := s.svc.EvidenceComplete(r.Context(), teamID, round)
	if err != nil {
		Fail(w, r, err)
		return
	}
	labels := make([]string, 0, len(missing))
	for _, k := range missing {
		labels = append(labels, k.Label())
	}
	OK(w, map[string]any{"complete": ok, "missing": labels})
}
