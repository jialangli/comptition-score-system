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
	SlotNormal  SlotType = "normal"  // 正式场次：队伍来自队伍主库，参与分台派生
	SlotExtra   SlotType = "extra"   // 加时赛：独立场次，仅在影响冠亚季军 / 晋级时启用
	SlotRematch SlotType = "rematch" // 重赛：独立场次，某队因器材故障 / 受干扰等原因重打一次
)

// Extra 是否「非正式场次」（加时赛 / 重赛）。
//
// 两者机制完全相同 —— 队伍以场内快照导入、不写入队伍主库、不参与分台派生、归档时独立标记；
// 区别只在「为什么打」（见各常量的注释）。判断一律走本方法，
// 不要再写 t == SlotExtra：新增类型时会漏点，而漏点**不报错**，
// 会把独立场次当成正式场次走进派生路径。
func (t SlotType) Extra() bool { return t == SlotExtra || t == SlotRematch }

// Valid 校验场次类型。
func (t SlotType) Valid() bool { return t == SlotNormal || t.Extra() }

// Display 返回中文名称（界面与留痕文案用）。
func (t SlotType) Display() string {
	switch t {
	case SlotNormal:
		return "正式场次"
	case SlotExtra:
		return "加时赛（独立场次）"
	case SlotRematch:
		return "重赛（独立场次）"
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
	RoundNo   int        `json:"round"`              // 轮次 1 / 2（见 RoundOfPeriod）
	TeamIDs   []int64    `json:"teamIds,omitempty"`  // 正式场次：**派生**出的队伍 ID（不再存储）
	Snapshot  []Snapshot `json:"snapshot,omitempty"` // 独立场次：场内快照
	CreatedAt time.Time  `json:"createdAt"`
}

// RoundOfPeriod 时段 → 轮次的**默认**绑定（上午 = 第 1 轮、下午 = 第 2 轮）。
//
// 只是默认值，不是恒等式：赛项只有 1 轮时，那一场完全可能被排在下午，
// 此时它仍是第 1 轮（属赛程安排，不是赛制）。
// 调用方要显式给 round 覆盖默认值 —— 所以轮次落列，而不是每次由时段现推。
func RoundOfPeriod(period string) int {
	if period == "下午" {
		return 2
	}
	return 1
}

// Snapshot 加时赛场内快照。
//
// 加时赛的队伍编号由运营手动导入、以导入为准；数据以快照形式保存，
// 绝不写入队伍主库，归档时独立标记为「独立场次」。
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
	RoundNo   int      `json:"round"` // 省略时按 Period 取默认（见 RoundOfPeriod）
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
		return &FieldError{Field: "type", Msg: "场次类型只能是 normal / extra / rematch"}
	}
	// 轮次省略时按时段取默认绑定；显式给了就必须是 1 / 2。
	// 显式覆盖是必要的：1 轮赛项的下午场次仍是第 1 轮（见 RoundOfPeriod）。
	if d.RoundNo == 0 {
		d.RoundNo = RoundOfPeriod(d.Period)
	}
	if d.RoundNo != 1 && d.RoundNo != 2 {
		return &FieldError{Field: "round", Msg: "场次轮次只能是 1 或 2"}
	}
	return nil
}
