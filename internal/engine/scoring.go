// Package engine 承载赛事评分系统的全部业务规则，并刻意保持为**纯函数**：
// 不访问数据库、不发 HTTP 请求、不读文件、不依赖全局可变状态。
//
// 为什么这样设计：计分与排名是整个系统里「最容易算错、算错了最难发现、发现时
// 往往成绩已经公示」的部分。把它做成纯函数后，同一份 fixture 既能喂给本包做
// 表驱动单测，也能喂给前端 JS 实现，两边结果可以逐位比对（见
// testdata/frontend_golden.json 与 scoring_test.go）。
//
// 依赖方向：engine → model（单向）。engine 不得 import store / service / api。
package engine

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// DefaultRefTime 时间奖励的基准时长（秒）。
//
// 含义：完赛用时恰好等于该值时时间奖励为 0，每快 1 秒加 perSecond 分，
// 慢于该值不计负分（截断到 0）。该值属于赛制参数而非赛项级配置，
// 可由 ev.scoreRule.params.refTime 覆盖，未配置时回退到本默认值
// （与前端原型的 REF_TIME 常量对齐）。
const DefaultRefTime = 120.0

// RefTimeFor 取某赛项的时间奖励基准时长。
//
// 优先级：ev.scoreRule.params.refTime → fallback → DefaultRefTime。
// fallback <= 0 视为未提供。
func RefTimeFor(ev *model.Event, fallback float64) float64 {
	if fallback <= 0 {
		fallback = DefaultRefTime
	}
	if ev != nil && ev.ScoreRule.Params != nil {
		if v, ok := ev.ScoreRule.Params["refTime"]; ok {
			if f := numOr0(v); f != 0 {
				return f
			}
		}
	}
	return fallback
}

// ============================================================================
// 取值语义：复刻 JS 的隐式数值转换
//
// 裁判端录入的值经 JSON 反序列化后可能是 float64（JSONB 数字）、
// string（表单原文）或 nil（未录入）。JS 的 Number(x) 对这几类输入的收敛规则
// 必须逐条对齐，否则「同一份数据前端算一套、后端算另一套」，
// 而排名对不上时几乎无法排查。因此本包不直接用 strconv，
// 而是统一走 numOr0。
// ============================================================================

// numOr0 复刻 JS 的 `Number(x) || 0`。
//
// 覆盖的语义：
//
//	nil          → 0
//	"12.5"       → 12.5    "12" → 12    " 12 " → 12（JS 前后空白可解析）
//	""           → 0       "abc" → 0（NaN 经 || 收敛为 0）
//	true/false   → 1 / 0
//	[]/[a]       → 0 / a    （JS 数组强转规则，多元素数组给 NaN → 0）
//	NaN/±Inf     → 0
func numOr0(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case float64:
		return finiteOr0(x)
	case float32:
		return finiteOr0(float64(x))
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint:
		return float64(x)
	case uint64:
		return float64(x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0
		}
		return finiteOr0(f)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0 // Number("") === 0
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0 // Number("abc") === NaN，NaN||0 === 0
		}
		return finiteOr0(f)
	case []any:
		switch len(x) {
		case 0:
			return 0
		case 1:
			return numOr0(x[0])
		default:
			return 0
		}
	}
	return 0
}

// finiteOr0 把 NaN / ±Inf 收敛为 0，避免脏数据把整张榜单污染成 NaN。
func finiteOr0(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// jsKey 复刻 JS「用变量取对象键」时的字符串化规则：
// 数字 12 → "12"、布尔 true → "true"、字符串原样。
// 用于等级任务（enum）的映射表查表。
func jsKey(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.FormatInt(int64(x), 10), true
		}
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case bool:
		return strconv.FormatBool(x), true
	case json.Number:
		return x.String(), true
	}
	return "", false
}

// f64ptr 取浮点指针，用于表达「已录入 / 未录入」三态。
func f64ptr(f float64) *float64 { return &f }

// ============================================================================
// 单任务取值
// ============================================================================

// taskRawScore 计算单个任务项的原始得分；返回 nil 表示**该项尚未录入**。
//
// 三态语义很关键：nil（未录入）与 0（录入了、得 0 分）必须区分开，
// 因为 complete 判定依赖「已录入项数 == 任务总数」。
//
// 与前端 taskRawScore 逐位一致：
//
//	nil / 空字符串 → nil（未录入）
//	enum           → 命中等级映射则取映射值，未命中记 0
//	count          → 数量 × 每单位分（任务 weight 即单价）
//	其余（numeric） → 原样作为数值，0 是合法得分
func taskRawScore(t *model.Task, val any) *float64 {
	if t == nil {
		return nil
	}
	if val == nil {
		return nil
	}
	if s, ok := val.(string); ok && s == "" {
		return nil
	}
	switch t.Type {
	case model.TaskEnum:
		if key, ok := jsKey(val); ok {
			if mapped, hit := t.EnumMap[key]; hit {
				return f64ptr(numOr0(mapped))
			}
		}
		return f64ptr(0)
	case model.TaskCount:
		return f64ptr(numOr0(val) * numOr0(t.Weight))
	default:
		return f64ptr(numOr0(val))
	}
}

// ============================================================================
// 单轮计分
// ============================================================================

// Score 计算**单轮**成绩。
//
// 计算链路（与前端 computeTotal 逐位一致，运算顺序刻意保持相同，
// 以保证浮点结果完全一致）：
//
//	基础分 base    按计分模板聚合各任务得分
//	  weighted_sum  Σ(归一化分 × 权重)，数值项按满分归一化到 100 分制
//	  sum           Σ(原始分)
//	  average       已录入项的算术平均
//	加分   bonus   time_bonus（越快加分，可封顶）
//	              + count_bonus（计数项按量加分，可封顶）
//	扣分   penalty per_card 模板下：黄牌数×黄牌分 + 红牌数×红牌分
//	总分   total   max(0, base + bonus − penalty)   ← 下限截断，不倒欠
//
// 注意 base 只统计 ev.Tasks 中声明过的任务项；count_bonus 引用的
// 「派生计数项」（如能源块、桥梁块）不参与基础分，只通过加分进入总分。
func Score(ev *model.Event, rec model.ScoreRecord, refTime float64) model.ScoreResult {
	var res model.ScoreResult
	if ev == nil {
		return res
	}
	if refTime <= 0 {
		refTime = DefaultRefTime
	}

	tasks := ev.Tasks
	vals := rec.TaskValues
	if vals == nil {
		vals = map[string]any{}
	}

	tpl := ev.ScoreRule.Template
	if tpl == "" {
		tpl = model.TplWeightedSum
	}

	var scored int
	switch tpl {
	case model.TplWeightedSum:
		for i := range tasks {
			t := &tasks[i]
			raw := taskRawScore(t, vals[t.ID])
			if raw == nil {
				continue
			}
			scored++
			norm := *raw
			if t.Type == model.TaskNumeric && t.MaxScore != nil && *t.MaxScore != 0 {
				norm = *raw / *t.MaxScore * 100
			}
			res.Base += norm * numOr0(t.Weight)
		}
	case model.TplSum:
		for i := range tasks {
			raw := taskRawScore(&tasks[i], vals[tasks[i].ID])
			if raw == nil {
				continue
			}
			scored++
			res.Base += *raw
		}
	case model.TplAverage:
		var sum float64
		for i := range tasks {
			raw := taskRawScore(&tasks[i], vals[tasks[i].ID])
			if raw == nil {
				continue
			}
			scored++
			sum += raw0(raw)
		}
		if scored > 0 {
			res.Base = sum / float64(scored)
		}
	default:
		// 未知模板不静默降级：base 保持 0，问题由 ValidateEvent 报错阻断保存。
		// （前端此处会落入 average 分支 —— 打成哪一档都算不出正确成绩，
		// 因此 Go 侧选择「不猜」并把配置错误暴露出来。）
	}

	// —— 加分 ——
	for i := range ev.BonusRules {
		b := &ev.BonusRules[i]
		p := b.Params
		switch b.Template {
		case model.BonusTime:
			dur := finiteOr0(rec.DurationSec)
			if dur > 0 {
				v := (refTime - dur) * numOr0(p["perSecond"])
				if v < 0 {
					v = 0
				}
				if capped, ok := p["cap"]; ok && capped != nil {
					if c := numOr0(capped); v > c {
						v = c
					}
				}
				res.Bonus += v
			}
		case model.BonusCount:
			taskID, _ := p["taskId"].(string)
			if taskID != "" {
				w := numOr0(vals[taskID]) * numOr0(p["perUnit"])
				if capped, ok := p["cap"]; ok && capped != nil {
					if c := numOr0(capped); w > c {
						w = c
					}
				}
				res.Bonus += w
			}
		}
	}

	// —— 扣分 ——
	if ev.PenaltyRule.Template == model.PenaltyPerCard {
		q := ev.PenaltyRule.Params
		res.Penalty = numOr0(rec.Yellow)*numOr0(q["yellow"]) + numOr0(rec.Red)*numOr0(q["red"])
	}

	// —— 总分 ——
	res.Total = res.Base + res.Bonus - res.Penalty
	if res.Total < 0 {
		res.Total = 0
	}
	res.Complete = len(tasks) > 0 && scored == len(tasks)
	return res
}

// raw0 解引用任务得分指针（调用点已确保非 nil）。
func raw0(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// ============================================================================
// 两轮取优
// ============================================================================

// RoundScore 一轮的计分结果 + 溯源信息。
type RoundScore struct {
	RoundNo int                // 轮次：1 / 2
	Record  *model.ScoreRecord // 该轮原始记录；nil 表示该轮无记录
	Result  model.ScoreResult  // 该轮计分结果
}

// BestOf 两轮取优，返回较好的那一轮。
//
// 赛制为两轮制：每支队伍在两轮各产生一条记录，最终成绩取较好的一轮。
// 择优顺序全部确定，保证同一份数据反复计算结果稳定：
//
//  1. 总分高者优先
//  2. 总分相同 → 用时少者优先
//  3. 仍相同   → 轮次小者优先（倾向于保留第一轮）
//
// 第二个返回值表示是否存在至少一条记录；为 false 时返回零值 RoundScore
// （未开赛的队伍，榜单上仍应出现，只是成绩为 0 且 complete=false）。
func BestOf(ev *model.Event, recs []model.ScoreRecord, refTime float64) (RoundScore, bool) {
	var best RoundScore
	found := false
	for i := range recs {
		rec := &recs[i]
		cur := RoundScore{
			RoundNo: rec.RoundNo,
			Record:  rec,
			Result:  Score(ev, *rec, refTime),
		}
		if !found || betterRound(cur, best) {
			best = cur
			found = true
		}
	}
	return best, found
}

func betterRound(a, b RoundScore) bool {
	if a.Result.Total != b.Result.Total {
		return a.Result.Total > b.Result.Total
	}
	var at, bt float64
	if a.Record != nil {
		at = finiteOr0(a.Record.DurationSec)
	}
	if b.Record != nil {
		bt = finiteOr0(b.Record.DurationSec)
	}
	if at != bt {
		return at < bt
	}
	return a.RoundNo < b.RoundNo
}

// RoundNumbers 返回一组记录中出现过的轮次（升序、去重）。
// 榜单与成绩表需要展示「该队打了哪几轮」。
func RoundNumbers(recs []model.ScoreRecord) []int {
	if len(recs) == 0 {
		return nil
	}
	seen := make(map[int]bool, len(recs))
	out := make([]int, 0, len(recs))
	for i := range recs {
		r := recs[i].RoundNo
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	// 轮次数量极少（≤2），插入排序足够且无额外依赖
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
