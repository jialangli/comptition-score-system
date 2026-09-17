package model

// ============================================================================
// 现场大屏
//
// 已确认规格（需求确认单 v1.3 §6.1）：
//   - 展示范围：全部参赛队伍排名（不是只显示前几名）
//   - 分页轮播：每屏 10 条，停留 60 秒后切换下一屏
//   - 隐私：姓名脱敏（张*），队名与学校保留
//   - 运营可「置顶 / 锁定本场」干预
//   - 刷新延迟 ≤ 5 秒
// ============================================================================

// 已确认的默认值。
const (
	DefaultPageSize    = 10 // 每屏条数
	DefaultIntervalSec = 60 // 每屏停留秒数
)

// ScreenConfig 大屏配置。
type ScreenConfig struct {
	EventID     string `json:"eventId"`
	PageSize    int    `json:"pageSize"`    // 每屏条数
	IntervalSec int    `json:"intervalSec"` // 每屏停留秒数
	Pinned      string `json:"pinned,omitempty"`
}

// Normalize 补齐缺省值并做边界钳制，避免前端传入非法值导致轮播异常。
func (s *ScreenConfig) Normalize() {
	if s.PageSize <= 0 {
		s.PageSize = DefaultPageSize
	}
	if s.PageSize > 50 {
		s.PageSize = 50
	}
	if s.IntervalSec < 5 {
		s.IntervalSec = DefaultIntervalSec
	}
	if s.IntervalSec > 600 {
		s.IntervalSec = 600
	}
}

// ScreenRow 大屏展示行。
//
// Members 为**已脱敏**的选手姓名（如「程** / 崔**」）；
// 后端直接返回脱敏结果，不依赖前端处理，避免某处漏脱敏导致未成年人信息泄露。
type ScreenRow struct {
	Rank     int    `json:"rank"`
	TeamName string `json:"name"`
	School   string `json:"school"`
	Group    string `json:"group"`
	Members  string `json:"members"`
	Award    string `json:"award"`
}

// ScreenPage 一屏数据 + 分页元信息。
type ScreenPage struct {
	EventID   string      `json:"eventId"`
	EventName string      `json:"eventName"`
	Page      int         `json:"page"`      // 从 1 开始
	TotalPage int         `json:"totalPage"` // 总屏数
	TotalRows int         `json:"totalRows"` // 总队伍数
	PageSize  int         `json:"pageSize"`
	Locked    bool        `json:"locked"` // 运营已锁定本屏
	Rows      []ScreenRow `json:"rows"`
}
