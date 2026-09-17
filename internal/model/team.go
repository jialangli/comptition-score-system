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

// Team 队伍。
//
// TeamNo 在本赛项内唯一（数据库有唯一索引 ux_teams_event_no），落实「一号一队」：
// 出现「一队多号」或「一号多队」时导入会被拦截，交运营裁决。
type Team struct {
	ID        int64      `json:"id"`
	EventID   string     `json:"eventId"`
	TeamNo    string     `json:"no"` // 队伍编号（赛项内唯一）
	Name      string     `json:"name"`
	School    string     `json:"school"`
	Coach     string     `json:"coach"`
	GroupCode string     `json:"group"`
	Members   string     `json:"members"` // 选手名单，源数据用 / 或 | 分隔
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
