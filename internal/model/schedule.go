package model

import "time"

// ============================================================================
// 赛台与场次
//
// 赛台是「赛事级资源」：数量由运营按参赛人数自行配置，可跨时段、跨赛项复用
// （例如今天比火星救援、明天比未来之城）。
// 场次 = 赛台 × 时段，绑定赛项与组别。
// ============================================================================

// Seat 赛台。
type Seat struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sortOrder"`
	CreatedAt time.Time `json:"createdAt"`
}

// SlotType 场次类型。
type SlotType string

const (
	SlotNormal SlotType = "normal" // 正式场次：队伍来自队伍主库
	SlotExtra  SlotType = "extra"  // 加时赛：独立场次，队伍来自场内快照
)

// Valid 校验场次类型。
func (t SlotType) Valid() bool { return t == SlotNormal || t == SlotExtra }

// Display 返回中文名称（界面与留痕文案用）。
func (t SlotType) Display() string {
	switch t {
	case SlotNormal:
		return "正式场次"
	case SlotExtra:
		return "加时赛（独立场次）"
	}
	return string(t)
}

// Slot 场次 = 赛台 × 时段。
// 一张赛台在一个时段只能有一个场次（数据库唯一约束 ux_slot_seat_period）。
type Slot struct {
	ID        int64      `json:"id"`
	SeatID    int64      `json:"seatId"`
	Period    string     `json:"period"` // 上午 / 下午
	TimeRange string     `json:"time"`   // 09:00–12:00
	EventID   string     `json:"eventId"`
	GroupCode string     `json:"group"`
	Type      SlotType   `json:"type"`
	TeamIDs   []int64    `json:"teamIds,omitempty"`  // 正式场次：队伍 ID（引用主库）
	Snapshot  []Snapshot `json:"snapshot,omitempty"` // 加时赛：场内快照
	CreatedAt time.Time  `json:"createdAt"`
}

// Snapshot 加时赛场内快照。
//
// 加时赛的队伍编号由运营手动导入、以导入为准；数据以快照形式保存，
// 绝不写入队伍主库，归档时独立标记为「加时赛场次」。
type Snapshot struct {
	ID     int64  `json:"id"`
	SlotID int64  `json:"slotId"`
	TeamNo string `json:"no"`
	Name   string `json:"name"`
	School string `json:"school"`
	Coach  string `json:"coach"`
}

// SlotDraft 新增 / 编辑场次的入参。
type SlotDraft struct {
	SeatID    int64    `json:"seatId"`
	Period    string   `json:"period"`
	TimeRange string   `json:"time"`
	EventID   string   `json:"eventId"`
	GroupCode string   `json:"group"`
	Type      SlotType `json:"type"`
}

// Validate 场次入参校验。
func (d *SlotDraft) Validate() error {
	if d.SeatID <= 0 {
		return &FieldError{Field: "seatId", Msg: "赛台不能为空"}
	}
	if d.Period == "" {
		return &FieldError{Field: "period", Msg: "时段不能为空"}
	}
	if d.EventID == "" {
		return &FieldError{Field: "eventId", Msg: "赛项不能为空"}
	}
	if d.GroupCode == "" {
		return &FieldError{Field: "group", Msg: "组别不能为空"}
	}
	if d.Type == "" {
		d.Type = SlotNormal
	}
	if !d.Type.Valid() {
		return &FieldError{Field: "type", Msg: "场次类型只能是 normal 或 extra"}
	}
	return nil
}
