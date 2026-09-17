package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 离线批量上行（POST /api/v1/sync）
//
// 赛场网络不可靠，评分端一律「先写本地、联网后上行」。这个端点就是上行的落点。
//
// 三条设计原则：
//
//  1. **逐条独立事务**。一条失败不影响其余 —— 现场最怕的是「100 条里有 1 条
//     有问题，结果整批都没上去」，然后还得人工挑出来重传。
//  2. **用队伍编号定位**，不用队伍 ID。离线录分时前端手里只有编号。
//  3. **不覆盖已签字成绩**。若服务端该队该轮已签字，上行会被拒绝并给出
//     明确原因 —— 改分必须走裁判长授权，离线同步不是后门。
//
// 响应里逐条给出结果与原因，前端据此把失败的条目留在本地待处理。
// ============================================================================

// syncResult 单条上行的处理结果。
type syncResult struct {
	ClientID string `json:"clientId,omitempty"`
	No       string `json:"no"`
	RoundNo  int    `json:"roundNo"`
	OK       bool   `json:"ok"`
	// Action 本次实际动作：insert（服务端原本没有）/ update（覆盖草稿）
	Action string `json:"action,omitempty"`
	Error  string `json:"error,omitempty"`
}

// syncResp 批量上行响应。
type syncResp struct {
	Total     int          `json:"total"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	Results   []syncResult `json:"results"`
	// ServerTime 让前端据此更新本地 lastSyncAt。
	ServerTime string `json:"serverTime"`
}

// handleSync POST /api/v1/sync
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	var body syncReq
	if err := decodeJSON(w, r, &body); err != nil {
		Fail(w, r, err)
		return
	}

	resp := syncResp{
		Total:   len(body.Scores),
		Results: make([]syncResult, 0, len(body.Scores)),
	}

	for _, item := range body.Scores {
		res := syncResult{ClientID: item.ClientID, No: item.TeamNo, RoundNo: item.RoundNo}

		if item.EventID == "" || item.TeamNo == "" {
			res.Error = "缺少 eventId 或队伍编号"
			resp.Failed++
			resp.Results = append(resp.Results, res)
			continue
		}

		team, err := s.svc.GetTeamByNo(r.Context(), item.EventID, item.TeamNo)
		if err != nil {
			res.Error = "该编号在本赛项下不存在"
			resp.Failed++
			resp.Results = append(resp.Results, res)
			continue
		}

		// 先看服务端现状：既用于判定 insert/update，也用于「已签字」的提前拦截。
		// 注意这里不吞错：只有「确实不存在」才算 insert，其它读失败必须暴露。
		existing, getErr := s.svc.GetScore(r.Context(), team.ID, item.RoundNo)
		switch {
		case getErr == nil:
			res.Action = "update"
		case errors.Is(getErr, store.ErrNotFound):
			res.Action = "insert"
		default:
			res.Error = "读取服务端成绩失败"
			resp.Failed++
			resp.Results = append(resp.Results, res)
			continue
		}

		_, err = s.svc.SaveScore(r.Context(), &model.ScoreRecord{
			TeamID:      team.ID,
			RoundNo:     item.RoundNo,
			TaskValues:  item.TaskValues,
			DurationSec: item.Duration,
			Yellow:      item.Yellow,
			Red:         item.Red,
			Signed:      item.Signed,
			Operator:    syncOperator(item),
		})
		if err != nil {
			if errors.Is(err, service.ErrScoreSubmitted) {
				res.Error = "服务端该轮成绩已签字，拒绝覆盖；如需修改请走改分申请"
			} else {
				res.Error = "上行失败：" + err.Error()
			}
			resp.Failed++
			resp.Results = append(resp.Results, res)
			continue
		}

		if existing == nil {
			res.Action = "insert"
		}
		res.OK = true
		resp.Succeeded++
		resp.Results = append(resp.Results, res)
	}

	resp.ServerTime = time.Now().Format(time.RFC3339)
	OK(w, resp)
}

// syncOperator 取这条上行记录的记分员。
//
// 优先用客户端标识（能追到具体是哪台平板/哪份本地记录），
// 没有时退回当前请求的操作者。留痕宁可粗一点，也不能空着。
func syncOperator(item syncScoreJSON) string {
	if item.ClientID != "" {
		return "离线录分:" + item.ClientID
	}
	return ""
}
