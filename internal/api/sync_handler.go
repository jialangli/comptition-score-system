package api

import (
	"context"
	"errors"
	"fmt"
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
	// 同步冲突：服务端已存在同队同轮记录且来自另一来源。
	// 此时**不会覆盖**，而是自动生成一条争议工单等裁判长裁定，
	// 前端据此提示裁判「这份已转人工裁定」，而不是反复重传。
	Conflict    bool   `json:"conflict,omitempty"`
	DisputeID   int64  `json:"disputeId,omitempty"`
	DisputeCode string `json:"disputeCode,omitempty"`
}

// syncResp 批量上行响应。
type syncResp struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	// Conflicted 检测到同步冲突、已转人工裁定的条数。
	//
	// 单列一个计数而不是并入 failed：冲突不是「没传上去」，而是「传上去了但
	// 有分歧、已进 P8 队列」。两者的处置完全不同 —— 前者要重传，后者要等裁定。
	Conflicted int          `json:"conflicted"`
	Results    []syncResult `json:"results"`
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

		op := syncOperator(item)

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

		// -------------------------------------------------------------------
		// 同步冲突：服务端已存在同队同轮记录，且来自**另一来源**
		//
		// 判据是 operator 而不是成绩内容 —— 要防的从来不是「两边打得不一样」，
		// 而是「两份成绩静静躺着、取数默认取最新而错榜」（P12 note 7）。
		// 同一台设备的续传 / 网络重试（operator 相同）必须仍然放行，
		// 否则每次断线重传都会被误判成「两台平板各打一份」。
		//
		// 处置是**不覆盖 + 自动建单**，而不是静默取最新。
		// -------------------------------------------------------------------
		if existing != nil && !sameSource(existing.Operator, op) {
			res.Error = "服务端已存在同队同轮成绩（来源：" + label(existing.Operator) + "），本次不覆盖"
			d, derr := s.raiseSyncConflict(r.Context(), team.ID, item.RoundNo, op, existing.Operator)
			resp.markConflict(&res, d, derr)
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
			Operator:    op,
		})
		if err != nil {
			if errors.Is(err, service.ErrScoreSubmitted) {
				// 已签字的成绩被另一来源补传覆盖 —— 同样是两份成绩并存。
				// 只回一句「去走改分申请」会让后上传的那份无声消失，故一并建单。
				if existing != nil && !sameSource(existing.Operator, op) {
					res.Error = "服务端该轮成绩已签字，拒绝覆盖"
					d, derr := s.raiseSyncConflict(r.Context(), team.ID, item.RoundNo, op, existing.Operator)
					resp.markConflict(&res, d, derr)
					resp.Results = append(resp.Results, res)
					continue
				}
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

// markConflict 把「自动建单」的结果写进单条上行结果。
//
// 建单成功计入 conflicted，失败计入 failed —— 建单失败不能吞掉：
// 静默放弃等于把两份成绩的差异重新藏起来，正是本次改造要消灭的问题。
func (resp *syncResp) markConflict(res *syncResult, d *model.Dispute, err error) {
	if err != nil {
		res.Error += "；且生成争议工单失败：" + err.Error()
		resp.Failed++
		return
	}
	res.Conflict = true
	res.DisputeID = d.ID
	res.DisputeCode = d.Code
	res.Error += "；已自动生成同步冲突工单 " + d.Code + "，等待裁判长裁定"
	resp.Conflicted++
}

// raiseSyncConflict 生成一条同步冲突工单。
//
// 幂等：同一队同一轮重复撞车只建一条（补传会重试，不能每重试一次就多一单）。
func (s *Server) raiseSyncConflict(ctx context.Context, teamID int64, round int,
	incoming, serverSide string) (*model.Dispute, error) {
	detail := fmt.Sprintf(
		"离线补传（%s）发现服务端已有 %s 记录的同队同轮成绩，两份成绩待裁定",
		label(incoming), label(serverSide))
	return s.svc.ReportSyncConflict(ctx, teamID, round, detail)
}

// sameSource 判断服务端那条记录是否来自同一台设备。
//
// 两者都非空且相等才认作同源。服务端记录若没有 operator（例如后台人工录的），
// 宁可判成冲突也不静默覆盖 —— 后台录入的成绩被离线平板盖掉，正是要防的场景。
func sameSource(serverSide, incoming string) bool {
	return serverSide != "" && incoming != "" && serverSide == incoming
}

// label 把可能为空的 operator 变成可读文案，避免提示里出现空括号。
func label(op string) string {
	if op == "" {
		return "未知来源"
	}
	return op
}
