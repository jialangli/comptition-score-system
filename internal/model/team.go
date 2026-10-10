package model

import "time"

// ============================================================================
// 队伍
//
// 队伍数据的唯一源头是 WRC 官网报名导出的 Excel（外部系统），本系统只负责
// 接入、核对与锁定，下游（打分 / 排名 / 公示）全部只读消费。
// ============================================================================

// TeamStatus 队伍状态。退赛只做软删除，绝不物理删除 —— 历史成绩永不悬空。
type TeamStatus string

const (
	TeamActive    TeamStatus = "active"    // 在册
	TeamWithdrawn TeamStatus = "withdrawn" // 弃赛（软删除）
)

// TeamSource 队伍数据来源。
type TeamSource string

const (
	SourceExcel  TeamSource = "excel"  // WRC 报名表导入
	SourceManual TeamSource = "manual" // 手动录入
	SourceAPI    TeamSource = "api"    // 外部接口
)

// TeamSession 队伍参赛轮次。
//
// 为什么要落**队伍级**：赛项轮次（1 轮 / 2 轮）是**赛项级**配置，而一队两轮制下
// 常出现「某队下午不来」—— 那是**队伍级**的事实。靠改赛项配置表达不了
// （改赛项会连累同赛项的所有队），靠弃赛又太重（整队退赛、成绩不进榜单）。
//
// 取值与前端原型完全一致（字符串 '1' / '2' / 'both'）。
type TeamSession string

const (
	SessionRound1 TeamSession = "1"    // 只打第 1 轮（上午）
	SessionRound2 TeamSession = "2"    // 只打第 2 轮（下午）
	SessionBoth   TeamSession = "both" // 两轮都打（默认）
)

// Valid 校验取值。
func (s TeamSession) Valid() bool {
	return s == SessionRound1 || s == SessionRound2 || s == SessionBoth
}

// Label 中文名称（界面与留痕文案用）。
func (s TeamSession) Label() string {
	switch s {
	case SessionRound1:
		return "仅第 1 轮"
	case SessionRound2:
		return "仅第 2 轮"
	case SessionBoth:
		return "两轮"
	}
	return string(s)
}

// Rounds 该队真正要打的轮次 = 赛项轮次 ∩ 参赛轮次（保持入参顺序）。
//
// 用于**赛项级**判断：发布门 / 完整性 / 进度列都必须走它。
// 只按赛项轮次判，会把「只打第 1 轮的队」永远算作「第 2 轮未录」——
// 一队下午缺席就永久卡住发布门，而现场并没有任何错。
//
// ⚠️ 交集为空时**回落到赛项轮次**（与前端 `teamRounds` 逐字同口径）：
// 赛项只排 1 轮、队伍却标「只打第 2 轮」是配置矛盾。回落口径下该队仍留在
// 「待完成名单」里，运营会看到它并去修配置；若算成空，该队就**静默消失**，
// 没人会发现配置错了。
//
// eventRounds 为赛项轮次（如 [1,2]）；本方法不校验它是否合法。
func (s TeamSession) Rounds(eventRounds []int) []int {
	if !s.Valid() {
		s = SessionBoth // 漏传不该变成「一轮都不打」
	}
	out := make([]int, 0, len(eventRounds))
	for _, r := range eventRounds {
		if s.Includes(r) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return append([]int(nil), eventRounds...)
	}
	return out
}

// Includes 该队是否参加第 round 轮 —— **场次队伍派生**的匹配判据。
//
// 与 Rounds 的分工：Rounds 管「赛项计划 ∩ 参赛轮次」（赛项级），
// Includes 管「这个场次的轮次我该不该上」（场次级）。
// 后者不需要知道赛项计划：赛项只有 1 轮时，压根不存在 round=2 的场次。
func (s TeamSession) Includes(round int) bool {
	if !s.Valid() {
		s = SessionBoth
	}
	switch s {
	case SessionRound1:
		return round == 1
	case SessionRound2:
		return round == 2
	}
	return true // both：两轮都打
}

// Team 队伍。
//
// TeamNo 在「赛事 + 赛项」内唯一（0004 后的复合唯一索引
// uq_teams_contest_event_no），落实「一号一队」。
// ⚠️ 是**赛事内**唯一，不是全局唯一：同一编号在不同赛事里可以重复，
//
//	跨赛事汇总时靠「队名 + 学校」近似归并（严格身份另需 team_uid）。
type Team struct {
	ID        int64  `json:"id"`
	ContestID string `json:"contestId"` // 所属赛事（0004 多赛事维度）
	EventID   string `json:"eventId"`
	TeamNo    string `json:"no"` // 队伍编号（赛事 + 赛项内唯一）
	Name      string `json:"name"`
	School    string `json:"school"`
	Coach     string `json:"coach"`
	GroupCode string `json:"group"`
	Members   string `json:"members"` // 选手名单，源数据用 / 或 | 分隔

	// —— 现场编排（分台与顺位）——
	//
	// 归台是**队伍级事实**，赛台是赛事级资源（可跨时段、跨赛项复用）：
	// 队伍 ∈ 一个赛台，场次队伍由「赛台 × 赛项 × 组别 × 轮次」**派生**，
	// 不再单独存储（slot_teams 退化为历史值）。
	//
	// SeatID 为 nil 表示**未排台** —— 这是「还没分台」的正常中间态，不是错误。
	SeatID *int64 `json:"seatId,omitempty"`
	// SeatOrder 台内顺位（从 1 起）；未排台时为 0。
	SeatOrder int `json:"seatOrder,omitempty"`
	// Session 参赛轮次；空值按 both 解释（见 TeamSession.Rounds）。
	Session TeamSession `json:"session,omitempty"`

	Status    TeamStatus `json:"status"`
	Source    TeamSource `json:"source"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// IsActive 是否在册（可用于参赛）。
func (t *Team) IsActive() bool { return t.Status == TeamActive }

// TeamDraft 新增/导入队伍时的入参（不含自增 ID 与时间戳）。
type TeamDraft struct {
	EventID   string     `json:"eventId"`
	TeamNo    string     `json:"no"`
	Name      string     `json:"name"`
	School    string     `json:"school"`
	Coach     string     `json:"coach"`
	GroupCode string     `json:"group"`
	Members   string     `json:"members"`
	Source    TeamSource `json:"source"`
}

// Validate 基础校验：编号必填且为纯数字、名称必填、组别必填。
func (d *TeamDraft) Validate() error {
	if d.TeamNo == "" {
		return &FieldError{Field: "no", Msg: "队伍编号不能为空"}
	}
	if !isDigits(d.TeamNo) {
		return &FieldError{Field: "no", Msg: "队伍编号必须为纯数字：" + d.TeamNo}
	}
	if d.Name == "" {
		return &FieldError{Field: "name", Msg: "队伍名称不能为空"}
	}
	if d.GroupCode == "" {
		return &FieldError{Field: "group", Msg: "组别不能为空"}
	}
	if d.Source == "" {
		d.Source = SourceManual
	}
	return nil
}

// FieldError 字段级校验错误，API 层据此返回 400 与字段名。
type FieldError struct {
	Field string `json:"field"`
	Msg   string `json:"message"`
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Msg }

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
