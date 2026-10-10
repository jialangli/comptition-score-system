package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ============================================================================
// 参数解析
//
// 全部集中在这里，是为了让「非法参数一律 400」这件事只有一种写法。
// 散落各处手写 strconv 的后果是：有的地方返回 400、有的地方静默当成 0，
// 后者最坑 —— 请求 /teams/abc 删除队伍会变成「删除 ID 为 0 的队伍」。
// ============================================================================

// pathStr 取路径参数（Go 1.22 ServeMux 的 {name} 语法）。
func pathStr(r *http.Request, name string) string {
	return strings.TrimSpace(r.PathValue(name))
}

// pathInt64 取路径参数并转成 int64。缺失或非法都返回错误，由调用方转 400。
func pathInt64(r *http.Request, name string) (int64, error) {
	raw := pathStr(r, name)
	if raw == "" {
		return 0, NewBadRequest("缺少路径参数 " + name)
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, NewBadRequest(fmt.Sprintf("路径参数 %s 必须是整数，收到 %q", name, raw))
	}
	if v <= 0 {
		return 0, NewBadRequest(fmt.Sprintf("路径参数 %s 必须为正整数，收到 %d", name, v))
	}
	return v, nil
}

// pathID 是 pathInt64 的便捷包装，路径参数名固定为 id。
func pathID(r *http.Request) (int64, error) { return pathInt64(r, "id") }

// queryInt 取查询参数整数；未提供时返回 def。
func queryInt(r *http.Request, name string, def int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, NewBadRequest(fmt.Sprintf("查询参数 %s 必须是整数，收到 %q", name, raw))
	}
	return v, nil
}

// queryBool 取查询参数布尔值。
//
// 接受 1/true/yes/on（不分大小写）为真，其余非空值一律为假；
// 空值返回 def。刻意不做「非法值报错」：布尔开关的语义就是
// 「传了即为真」，报错反而让 ?withdrawn= 这种写法变成 400。
func queryBool(r *http.Request, name string, def bool) bool {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// 请求体
// ---------------------------------------------------------------------------

// reasonBody 需要一个「原因」的请求体（弃赛 / 改组 / 删队 / 锁定等）。
//
// 统一成一个结构体，避免每个端点各写一个只有字段名不同的匿名结构。
type reasonBody struct {
	Reason string `json:"reason"`
}

// createTeamBody 新增队伍的请求体。
type createTeamBody struct {
	No      string `json:"no"`
	Name    string `json:"name"`
	School  string `json:"school"`
	Coach   string `json:"coach"`
	Group   string `json:"group"`
	Members string `json:"members"`
}

// updateTeamBody 修改队伍信息的请求体（整体覆盖语义）。
type updateTeamBody struct {
	Name    string `json:"name"`
	School  string `json:"school"`
	Coach   string `json:"coach"`
	Group   string `json:"group"`
	Members string `json:"members"`
	Reason  string `json:"reason"`
}

// assignSeatBody 队伍归台的请求体（PUT /teams/{id}/seat）。
//
// seatId 省略或 0 = **取消归台**（回到「未排台」，顺位一并归零）：
// 用 0 而不是 null，是为了让前端「清掉选择框就提交 0」这种最自然的写法直接可用。
type assignSeatBody struct {
	SeatID    int64  `json:"seatId"`
	SeatOrder int    `json:"seatOrder"`
	Reason    string `json:"reason"`
}

// setSessionBody 设置队伍参赛轮次的请求体（PUT /teams/{id}/session）。
//
// session 取 '1' / '2' / 'both'（省略按 both）；非法值由服务层转成 400。
type setSessionBody struct {
	Session string `json:"session"`
	Reason  string `json:"reason"`
}

// importPreviewReq 报名导入预览 / 提交的请求体。
//
// Rows 由解析层（前端或 P6 的 xlsx 解析）按列映射归一后提供；
// 本层只负责把它转交给 service。
type importPreviewReq struct {
	EventID       string               `json:"eventId"`
	Rows          []importRowJSON      `json:"rows"`
	SelectedLines []int                `json:"selectedLines,omitempty"` // 仅提交时使用
	Overrides     []importOverrideJSON `json:"overrides,omitempty"`     // 仅提交时使用：冲突行的人工裁决
	Note          string               `json:"note,omitempty"`
	Source        string               `json:"source,omitempty"`
}

// importOverrideJSON 冲突行的人工裁决。
//
// mode 取 file（以文件为准，强制覆盖）/ db（以库内为准，丢弃）/ skip（跳过）。
// 服务端不会"猜"该怎么裁 —— 必须逐行点名，未点名的冲突行一律让整批入库失败。
type importOverrideJSON struct {
	Line int    `json:"line"`
	Mode string `json:"mode"`
}

// importRowJSON 一行报名数据（字段名与前端列映射结果一致）。
type importRowJSON struct {
	No      string `json:"no"`
	Name    string `json:"name"`
	School  string `json:"school"`
	Coach   string `json:"coach"`
	Group   string `json:"group"`
	Members string `json:"members"`
	LineNo  int    `json:"lineNo,omitempty"`
}

// saveScoreReq 录入 / 修改成绩的请求体。
type saveScoreReq struct {
	RoundNo     int            `json:"roundNo"`
	TaskValues  map[string]any `json:"tasks"`
	Duration    float64        `json:"time"`
	Yellow      int            `json:"yellow"`
	UpgradedRed int            `json:"upgradedRed"` // 已由黄牌升级出的红牌数（计入红牌总数）
	Red         int            `json:"red"`
	Signed      bool           `json:"signed"`
}

// scoreChangeReq 改分申请 / 授权改分的请求体。
type scoreChangeReq struct {
	Round      int            `json:"roundNo"`
	TaskValues map[string]any `json:"tasks,omitempty"`
	After      float64        `json:"after,omitempty"`
	Reason     string         `json:"reason"`
	Approver   string         `json:"approver,omitempty"` // 仅授权改分时提供
}

// seatBody 赛台请求体。
type seatBody struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sortOrder"`
	Reason    string `json:"reason,omitempty"`
}

// slotBody 场次请求体。
type slotBody struct {
	SeatID    int64      `json:"seatId"`
	Period    string     `json:"period"`
	TimeRange string     `json:"time"`
	EventID   string     `json:"eventId"`
	Group     string     `json:"group"`
	Type      string     `json:"type"`
	Round     int        `json:"round"`              // 轮次 1/2；省略按时段取默认（上午=1、下午=2）
	TeamIDs   []int64    `json:"teamIds,omitempty"`  // **已废弃**：场次队伍改为派生，字段仅保留以兼容老请求的解析
	Snapshot  []snapJSON `json:"snapshot,omitempty"` // 独立场次快照
	Reason    string     `json:"reason,omitempty"`
}

// snapJSON 加时赛场内快照的一行。
type snapJSON struct {
	No     string `json:"no"`
	Name   string `json:"name"`
	School string `json:"school"`
	Coach  string `json:"coach"`
}

// screenConfigBody 大屏配置请求体。
type screenConfigBody struct {
	PageSize    int    `json:"pageSize"`
	IntervalSec int    `json:"intervalSec"`
	Pinned      string `json:"pinned"`
	Reason      string `json:"reason,omitempty"`
}

// contestRulesBody 赛事级规则的更新请求体。
//
// 只收「改成什么 + 为什么」，不收 contestId / updatedAt：
// 赛事由请求上下文决定，时间由库决定 —— 让客户端传这两样只会造成
// 「传了但被静默忽略」的误解。
type contestRulesBody struct {
	// SubstituteMode 名次编排：none = 不递补（默认）/ rank = 按名次顺延。
	SubstituteMode string `json:"substituteMode"`
	// Reason 为什么改。留空时由服务端按新口径生成一条说明（改公示名次必须留痕）。
	Reason string `json:"reason,omitempty"`
}

// applyChangeReq 授权改分请求体（成绩字段 + 审批信息）。
type applyChangeReq struct {
	Round       int            `json:"roundNo"`
	TaskValues  map[string]any `json:"tasks"`
	Duration    float64        `json:"time"`
	Yellow      int            `json:"yellow"`
	UpgradedRed int            `json:"upgradedRed"` // 已由黄牌升级出的红牌数（计入红牌总数）
	Red         int            `json:"red"`
	Signed      bool           `json:"signed"`
	Approver    string         `json:"approver"`
	Reason      string         `json:"reason"`
}

// syncReq 离线批量上行的请求体（P5 前端断网兜底用）。
type syncReq struct {
	// LastSyncAt 客户端上次同步时间（RFC3339）。仅作参考记录，本期不做增量裁剪。
	LastSyncAt string `json:"lastSyncAt,omitempty"`
	// Scores 待上行的成绩记录。逐条处理，单条失败不影响其余。
	Scores []syncScoreJSON `json:"scores"`
}

// syncScoreJSON 一条待上行的成绩。
//
// 用 TeamNo 而不是 TeamID：现场离线录分时前端只知道队伍编号，
// 队伍 ID 是后端分配的，前端不一定拿到过。用编号更贴近现场实际。
type syncScoreJSON struct {
	EventID     string         `json:"eventId"`
	TeamNo      string         `json:"no"`
	RoundNo     int            `json:"roundNo"`
	TaskValues  map[string]any `json:"tasks"`
	Duration    float64        `json:"time"`
	Yellow      int            `json:"yellow"`
	UpgradedRed int            `json:"upgradedRed"` // 已由黄牌升级出的红牌数（计入红牌总数）
	Red         int            `json:"red"`
	Signed      bool           `json:"signed"`
	// ClientID 客户端本地记录标识，用于回执对账（可选）。
	ClientID string `json:"clientId,omitempty"`
}
