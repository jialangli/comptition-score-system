package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
)

// ============================================================================
// 与前端实现的逐位比对（P2 验收标准）
//
// 基准文件 testdata/frontend_golden.json 由 scripts/gen_frontend_golden.js
// 从 demo 原型里**原样抽取**前端实现（自动展开依赖闭包，不再手写函数名清单）
// 后跑出来的期望值。
//
// 这里断言的是**浮点精确相等**，不是近似相等 —— 因为 Go 侧刻意保持了与 JS
// 完全相同的运算顺序，任何一处顺序差异都会立刻暴露成断言失败。
//
// 2026-10-10 重写：上一版基准停在 2026-09-17，此后前端换了整套赛项配置
// （future_city 的任务都变了）、standings 长出 rankAll / bestRoundOf 等依赖，
// 而"两边都是旧快照"所以测试一直绿 —— 这条自检链路曾**静默断掉**。
// 现在的脚本自带依赖闭包展开与覆盖度自检，不会再因为清单过时而悄悄失效。
// ============================================================================

// goldenResult 一条记录的逐轮计分结果（= 前端 computeTotal 的返回）。
type goldenResult struct {
	RoundNo int     `json:"roundNo"`
	Time    float64 `json:"time"`
	model.ScoreResult
}

type goldenTeam struct {
	No      string `json:"no"`
	Name    string `json:"name"`
	Group   string `json:"group"`
	School  string `json:"school"`
	Coach   string `json:"coach"`
	Members string `json:"members"`
	Status  string `json:"status"`
	Note    string `json:"note"`
	// Voided 被裁定「取消资格」→ 成绩作废、整行不进榜单（与红牌是两条路径）。
	Voided bool `json:"voided"`
	// Records 该队全部轮次的原始记录（字段名与 model.ScoreRecord 一致，可直接反序列化）。
	// 两轮制下长度可为 2；未开赛的队伍为空。
	Records []model.ScoreRecord `json:"records"`
	// PerRound 逐轮计分结果：**每一轮都单独比对**，
	// 于是"某一轮算错"不会被取优掩盖掉。
	PerRound []goldenResult `json:"expectPerRound"`
	// Expect 取优那一轮的结果（榜单所用口径）。
	Expect model.ScoreResult `json:"expect"`
}

type goldenStanding struct {
	Rank  int     `json:"rank"`
	No    string  `json:"no"`
	Name  string  `json:"name"`
	Group string  `json:"group"`
	Base  float64 `json:"base"`
	Bonus float64 `json:"bonus"`
	Penal float64 `json:"penalty"`
	Total float64 `json:"total"`
	Time  float64 `json:"time"`
	// Complete 全部任务均已录入。
	Complete bool `json:"complete"`
	// DQ 红牌取消比赛资格：留在榜内、名次置 0、不参评奖项。
	DQ bool `json:"dq"`
	// Rounds 有记录的轮次；BestRound 取优采用的那一轮（0 = 尚无记录）。
	Rounds    []int  `json:"rounds"`
	BestRound int    `json:"bestRound"`
	Award     string `json:"award"`
	Tie       bool   `json:"tie"`
}

type goldenPair struct {
	In  string `json:"in"`
	Out string `json:"out"`
}

type goldenExcluded struct {
	// Affects 这条排除影响哪一组断言（substitute / cards / mask）；
	// 空串表示纯说明，不影响任何断言。
	Affects string `json:"affects"`
	What    string `json:"what"`
	Why     string `json:"why"`
}

// goldenSource 生成基准时的源文件指纹。
//
// 为什么必须有：这份基准曾**静默脱离前端近一个月** —— 生成脚本的清单过时后直接崩掉，
// 而两边读的都是同一份旧快照，测试于是照样全绿。只写 generatedAt 挡不住这件事
// （没人会因为时间戳旧就去重跑）。有了指纹，基准"过期"就是一个能被自动检查的事实。
type goldenSource struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	MTime  string `json:"mtime"`
}

type goldenFile struct {
	Source     string       `json:"source"`
	SourceInfo goldenSource `json:"sourceInfo"`
	Note       string       `json:"note"`
	RefTime    float64      `json:"refTime"`
	// Excluded 明确"这份基准没有对照什么"，以及为什么 ——
	// 否则读基准的人会以为它覆盖了全部口径。
	Excluded []goldenExcluded `json:"excluded"`
	Events   []struct {
		ID   string `json:"id"`
		Note string `json:"note"`
		// RefTime 本赛项的**时间奖励基准时长**（前端 eventTotalSec 的结论）。
		// 多阶段赛项 = 各阶段之和；Go 侧必须用 RefTimeFor 自行推导出同一个数。
		RefTime float64 `json:"refTime"`
		// SubstituteMode 本场景生效的**名次编排口径**（前端 S.substituteRule.mode）。
		// 主场景是产品默认 'none'（不递补：作废队位置留空 → 名次出现 4 → 6 这种空洞），
		// 另有 'rank'（按名次顺延）场景。Go 侧据此决定 RankOptions.KeepGap。
		SubstituteMode string           `json:"substituteMode"`
		Event          model.Event      `json:"event"`
		Teams          []goldenTeam     `json:"teams"`
		Ranking        []goldenStanding `json:"standings"`
		FEValid        struct {
			Errs  []string `json:"errs"`
			Warns []string `json:"warns"`
		} `json:"frontendValidation"`
	} `json:"events"`
	MaskPersonCases []goldenPair `json:"maskPersonCases"`
	MaskCases       []goldenPair `json:"maskCases"`
}

// excludedLogged 保证"未纳入对照"清单每个测试进程只打印一次。
var excludedLogged bool

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "frontend_golden.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取前端基准失败（先跑 node scripts/gen_frontend_golden.js）：%v", err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("解析前端基准失败：%v", err)
	}
	if len(g.Events) == 0 {
		t.Fatal("前端基准为空")
	}
	// 把"未纳入对照"的口径打到测试输出里：它必须被看见，
	// 否则这份基准会被当成"全覆盖"，而实际有四处刻意排除。
	// 每个进程只打一次 —— 每个用例都打一遍会把真正的失败淹掉。
	if !excludedLogged {
		excludedLogged = true
		for _, x := range g.Excluded {
			t.Logf("⚠ 未纳入对照：%s\n    原因：%s", x.What, x.Why)
		}
	}
	return g
}

// TestParityWithFrontendScoring 逐轮计分逐位一致。
//
// 对每支队伍的**每一条记录**都算一遍并与前端比对（不是只比取优那一轮）：
// 两轮赛项里"第 2 轮算错、但因为取优仍然选第 1 轮"这种错法必须能被抓到。
func TestParityWithFrontendScoring(t *testing.T) {
	g := loadGolden(t)

	cases, mismatches := 0, 0
	for _, ge := range g.Events {
		ev := ge.Event
		// 基准时长由赛项自身推导（多阶段 = 阶段之和）；
		// 用全局 refTime 顶替会把「后端按 120s 算时间奖励」这个 bug 盖掉。
		refTime := RefTimeFor(&ev, 0)
		for _, gt := range ge.Teams {
			for _, want := range gt.PerRound {
				cases++
				rec, ok := recordOfRound(gt.Records, want.RoundNo)
				if !ok {
					mismatches++
					t.Errorf("%s / %s：基准里有第 %d 轮的期望值，却找不到对应记录",
						ge.ID, gt.Name, want.RoundNo)
					continue
				}
				got := Score(&ev, rec, refTime)
				if diff := compareResult(got, want.ScoreResult); diff != "" {
					mismatches++
					t.Errorf("%s / %s（%s）第 %d 轮：与前端不一致（%s）\n  Go:   base=%v bonus=%v penalty=%v total=%v complete=%v\n  前端: base=%v bonus=%v penalty=%v total=%v complete=%v",
						ge.ID, gt.Name, gt.No, want.RoundNo, diff,
						got.Base, got.Bonus, got.Penalty, got.Total, got.Complete,
						want.Base, want.Bonus, want.Penalty, want.Total, want.Complete)
				}
			}
		}
	}
	t.Logf("计分逐位比对：%d 例（逐轮），%d 例不一致", cases, mismatches)
}

// TestParityWithFrontendBestRound 取优口径一致（总分 ↓ → 用时 ↑ → 轮次小者）。
//
// 单列一个用例而不是并进榜单断言：榜单里名次、奖项、并列混在一起，
// 取优错了只会表现为"名次不对"，看不出是取优的问题。
func TestParityWithFrontendBestRound(t *testing.T) {
	g := loadGolden(t)

	cases := 0
	for _, ge := range g.Events {
		ev := ge.Event
		// 基准时长由赛项自身推导（多阶段 = 阶段之和）；
		// 用全局 refTime 顶替会把「后端按 120s 算时间奖励」这个 bug 盖掉。
		refTime := RefTimeFor(&ev, 0)
		for _, gt := range ge.Teams {
			cases++
			best, found := BestOf(&ev, gt.Records, refTime)
			got := best.Result
			if !found {
				// 无记录：两端都应是零值结果
				got = model.ScoreResult{}
			} else if best.RoundNo != wantBestRound(gt) {
				t.Errorf("%s / %s：取优轮次 Go=%d / 前端=%d",
					ge.ID, gt.Name, best.RoundNo, wantBestRound(gt))
			}
			if diff := compareResult(got, gt.Expect); diff != "" {
				t.Errorf("%s / %s：取优结果与前端不一致（%s）\n  Go:   %+v\n  前端: %+v",
					ge.ID, gt.Name, diff, got, gt.Expect)
			}
		}
	}
	t.Logf("取优比对：%d 例", cases)
}

// wantBestRound 从逐轮期望值里按取优规则反推前端选了哪一轮。
func wantBestRound(gt goldenTeam) int {
	if len(gt.PerRound) == 0 {
		return 0
	}
	best := gt.PerRound[0]
	for _, cur := range gt.PerRound[1:] {
		if cur.Total > best.Total ||
			(cur.Total == best.Total && cur.Time < best.Time) ||
			(cur.Total == best.Total && cur.Time == best.Time && cur.RoundNo < best.RoundNo) {
			best = cur
		}
	}
	return best.RoundNo
}

// TestParityWithFrontendStandings 排名结果逐位一致
// （名次、总分、分项、用时、完成度、取消资格、轮次、取优轮、奖项、并列标记）。
//
// 名次口径（含被取消资格队伍留下的空缺）由基准逐场景给出：主场景是产品默认的
// 「不递补」，因此这里比对的名次带空洞；`*_substitute_rank` 场景则是连续编号。
// 两档都在对照里 —— 2026-10-10 之前「不递补」后端没有，只能排除在外。
func TestParityWithFrontendStandings(t *testing.T) {
	g := loadGolden(t)

	for _, ge := range g.Events {
		t.Run(ge.ID, func(t *testing.T) {
			ev := ge.Event
			refTime := RefTimeFor(&ev, 0)
			in, voided := goldenRankInput(ev, ge.Teams)
			// AwardOnlyComplete 与生产默认口径一致（2026-10-04 起默认开启）：
			// 未完成录入的队伍保留名次但不占获奖名额。
			got := Rank(in, RankOptions{
				RefTime:           refTime,
				AwardOnlyComplete: true,
				VoidedTeams:       voided,
				// 口径由基准给出，判据走 model —— 测试里不自己写 `== "none"`。
				KeepGap: model.SubstituteMode(ge.SubstituteMode).KeepGap(),
			})

			if len(got) != len(ge.Ranking) {
				t.Fatalf("榜单长度不一致：Go %d 行 / 前端 %d 行", len(got), len(ge.Ranking))
			}
			for i, want := range ge.Ranking {
				row := got[i]
				if row.Rank != want.Rank {
					t.Errorf("第 %d 行名次：Go %d / 前端 %d", i+1, row.Rank, want.Rank)
				}
				if row.Team.TeamNo != want.No {
					t.Errorf("第 %d 名队伍：Go %s（%s）/ 前端 %s（%s）",
						i+1, row.Team.TeamNo, row.Team.Name, want.No, want.Name)
				}
				if row.Result.Total != want.Total {
					t.Errorf("%s 总分：Go %v / 前端 %v", want.Name, row.Result.Total, want.Total)
				}
				if row.Result.Base != want.Base || row.Result.Bonus != want.Bonus || row.Result.Penalty != want.Penal {
					t.Errorf("%s 分项：Go base=%v bonus=%v penalty=%v / 前端 base=%v bonus=%v penalty=%v",
						want.Name, row.Result.Base, row.Result.Bonus, row.Result.Penalty,
						want.Base, want.Bonus, want.Penal)
				}
				if row.Duration != want.Time {
					t.Errorf("%s 用时：Go %v / 前端 %v", want.Name, row.Duration, want.Time)
				}
				if row.Result.Complete != want.Complete {
					t.Errorf("%s 完成度：Go %v / 前端 %v", want.Name, row.Result.Complete, want.Complete)
				}
				if row.Disqualified != want.DQ {
					t.Errorf("%s 取消资格标记：Go %v / 前端 %v", want.Name, row.Disqualified, want.DQ)
				}
				if fmt.Sprint(row.Rounds) != fmt.Sprint(want.Rounds) {
					t.Errorf("%s 有记录的轮次：Go %v / 前端 %v", want.Name, row.Rounds, want.Rounds)
				}
				if row.BestRound != want.BestRound {
					t.Errorf("%s 取优轮次：Go %d / 前端 %d", want.Name, row.BestRound, want.BestRound)
				}
				if row.Award != want.Award {
					t.Errorf("%s 奖项：Go %q / 前端 %q", want.Name, row.Award, want.Award)
				}
				if row.Tie != want.Tie {
					t.Errorf("%s 并列标记：Go %v / 前端 %v", want.Name, row.Tie, want.Tie)
				}
			}
		})
	}
}

// TestParityWithFrontendMask 脱敏结果完全一致。
//
// ⚠️ 目前被基准**刻意排除**（见 golden 的 excluded / affects=mask）：
// 三处实现互不相同（demo 用 x 且保留末字；web 与 Go 用 * 且不保留末字；
// 裁判端设计册写的是「卫*九」），属产品口径未统一，不是引擎分歧。
// 排除时不静默通过 —— 会打印原因，提醒这不是"已对齐"。
func TestParityWithFrontendMask(t *testing.T) {
	g := loadGolden(t)

	if x, ok := g.excludes("mask"); ok {
		t.Logf("⚠ 脱敏对照已按基准声明跳过：%s\n    原因：%s", x.What, x.Why)
		return
	}

	for _, c := range g.MaskPersonCases {
		if got := MaskPerson(c.In); got != c.Out {
			t.Errorf("MaskPerson(%q)：Go %q / 前端 %q", c.In, got, c.Out)
		}
	}
	for _, c := range g.MaskCases {
		if got := MaskMembers(c.In); got != c.Out {
			t.Errorf("MaskMembers(%q)：Go %q / 前端 %q", c.In, got, c.Out)
		}
	}
}

// excludes 查某组断言是否被基准声明为「未纳入对照」。
func (g goldenFile) excludes(affects string) (goldenExcluded, bool) {
	for _, x := range g.Excluded {
		if x.Affects == affects {
			return x, true
		}
	}
	return goldenExcluded{}, false
}

// TestParityWithFrontendRefTime 时间奖励的**基准时长**两端一致。
//
// 单列一个用例，因为它最容易被忽略、后果又最直接：基准时长决定 time_bonus 的加分上限，
// 算错了成绩整体偏，而页面上看不出是它。2026-10-10 就是靠这条对出来的：
// 分阶段赛项（未来之城 120+105s）前端按 225s、后端只认 params.refTime / 默认值（120s），
// 同一份成绩差出十几分。
func TestParityWithFrontendRefTime(t *testing.T) {
	g := loadGolden(t)

	for _, ge := range g.Events {
		ev := ge.Event
		if got := RefTimeFor(&ev, 0); got != ge.RefTime {
			t.Errorf("%s 基准时长：Go %v / 前端 %v（阶段数 %d）—— 两端 time_bonus 会按不同基准加分",
				ge.ID, got, ge.RefTime, len(ev.Phases))
		}
	}
}

// TestGoldenFixtureFreshness 基准必须与 demo 的当前内容对得上（防"悄悄过期"）。
//
// 这一条是补上真实事故的：基准脚本失效 + 两边都读旧快照 = 测试全绿但对照早已失效。
// 现在只要 demo 变了而基准没重新生成，这里就会红，并给出重跑命令。
//
// 比对的是**仓库内**的那份 demo 副本（与桌面工作副本同步）。若仓库里没有副本，
// 说明这份检出用不到前端基准，跳过而不是误报。
func TestGoldenFixtureFreshness(t *testing.T) {
	g := loadGolden(t)
	if g.SourceInfo.SHA256 == "" {
		t.Skip("基准里没有 sourceInfo 指纹（旧格式），跳过；重跑一次生成脚本即可带上")
	}
	p := filepath.Join("..", "..", g.SourceInfo.Name)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("跳过：仓库内没有前端源 %s（%v）", p, err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != g.SourceInfo.SHA256 {
		t.Errorf("前端基准已过期：demo 内容与生成基准时不一致。\n"+
			"  demo: %s（%d 字节，现在 %d 字节）\n"+
			"  基准: sha256 %s…\n"+
			"  现在: sha256 %s…\n"+
			"  修法：node scripts/gen_frontend_golden.js 重新生成，\n"+
			"        然后 git diff testdata/frontend_golden.json ——\n"+
			"        有 diff = 前端算法真的变了（两端需要同步）；无 diff = 只是改了与计分无关的内容。",
			p, g.SourceInfo.Bytes, len(raw),
			g.SourceInfo.SHA256[:16], got[:16])
	}
}

// TestGoldenSeedEventsAreValid 基准里的赛项配置必须无阻断性问题
// （前端校验同样为 0 error，两侧一致）。
func TestGoldenSeedEventsAreValid(t *testing.T) {
	g := loadGolden(t)

	for _, ge := range g.Events {
		ev := ge.Event
		res := ValidateEvent(&ev)
		if !res.OK() {
			t.Errorf("%s 配置被判定为不合法：%v", ge.ID, res.ErrorMessages())
		}
		if len(ge.FEValid.Errs) != 0 {
			t.Errorf("%s 前端基准里仍有 error（基准本身有问题）：%v", ge.ID, ge.FEValid.Errs)
		}
		if len(res.Warnings) != len(ge.FEValid.Warns) {
			t.Logf("%s 提醒项数量：Go %d / 前端 %d（Go 侧刻意补齐了更多检查，数量不同属预期）",
				ge.ID, len(res.Warnings), len(ge.FEValid.Warns))
		}
	}
}

// TestGoldenFixtureSanity 基准文件自检：确保脚本真的抽到了内容，
// 而不是抽出一个空壳让比对「假通过」。
func TestGoldenFixtureSanity(t *testing.T) {
	g := loadGolden(t)

	if g.Source == "" {
		t.Error("基准文件缺少 source 字段")
	}
	if g.RefTime != DefaultRefTime {
		t.Errorf("基准 refTime = %v，与 DefaultRefTime（%v）不一致，请确认赛制基准时长", g.RefTime, DefaultRefTime)
	}

	total, twoRounds, dq, voided, awards := 0, 0, 0, 0, 0
	for _, ge := range g.Events {
		total += len(ge.Teams)
		if len(ge.Teams) == 0 || len(ge.Ranking) == 0 {
			t.Errorf("%s 的基准为空", ge.ID)
		}
		for _, gt := range ge.Teams {
			if len(gt.Records) >= 2 {
				twoRounds++
			}
			if gt.Voided {
				voided++
			}
			if len(gt.Records) != len(gt.PerRound) {
				t.Errorf("%s/%s：记录 %d 条但逐轮期望 %d 条，基准自相矛盾",
					ge.ID, gt.Name, len(gt.Records), len(gt.PerRound))
			}
			if gt.Expect.Complete && gt.Expect.Total == 0 && gt.Expect.Base > 0 {
				t.Errorf("%s/%s：已完成录入却算出总分 0，基准可疑", ge.ID, gt.Name)
			}
		}
		for _, st := range ge.Ranking {
			if st.DQ {
				dq++
			}
			if st.Award != "" {
				awards++
			}
		}
		// 记录「加分项对名次的实际影响」：若加分的队间极差 ≥ 基础分的队间极差，
		// 说明加分规则和主任务评分一样能决定名次。这是赛制参数问题而非引擎问题，
		// 但需要业务确认，因此在测试里留一条显式日志，避免被长期忽略。
		if bSpread, baseSpread := spread(ge.Teams, true), spread(ge.Teams, false); bSpread >= baseSpread && baseSpread > 0 {
			t.Logf("⚠ %s：加分项队间极差 %.1f ≥ 基础分队间极差 %.1f —— "+
				"加分规则对名次的影响力不低于主任务评分，建议业务复核奖励参数", ge.ID, bSpread, baseSpread)
		}
	}
	if total < 10 {
		t.Errorf("基准仅 %d 支队伍，样本过少", total)
	}
	if twoRounds == 0 {
		t.Error("基准里没有一支队伍有两轮记录 —— 「两轮取优」这条口径实际未被对照")
	}
	if voided == 0 {
		t.Error("基准里没有「裁定取消资格」的队伍 —— 成绩作废不进榜单这条口径未被对照")
	}
	if dq == 0 {
		t.Error("基准里没有红牌取消资格的行 —— dq 标记与名次置 0 未被对照")
	}
	if awards == 0 {
		t.Log("⚠ 基准里没有一行奖项非空 —— 奖项分配的对照目前是**空断言**（两端都恒为空串）。" +
			"原因：demo 已移除奖项档位配置（rankRule.awardTiers），后端也只在配置了它时才分配。" +
			"奖项分配只能靠 engine 自身单测守护，不要指望这份基准。")
	}
	t.Logf("基准规模：%d 支队伍 / %d 支两轮队 / %d 支作废队 / %d 行取消资格 / %d 行有奖项",
		total, twoRounds, voided, dq, awards)
}

// spread 取某赛项队伍中 bonus（或 base）的极差。
func spread(teams []goldenTeam, useBonus bool) float64 {
	var min, max float64
	first := true
	for _, t := range teams {
		v := t.Expect.Base
		if useBonus {
			v = t.Expect.Bonus
		}
		if first {
			min, max, first = v, v, false
			continue
		}
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return max - min
}

// ---------------------------------------------------------------------------

// recordOfRound 取某队某一轮的记录。
func recordOfRound(recs []model.ScoreRecord, round int) (model.ScoreRecord, bool) {
	for _, r := range recs {
		if r.RoundNo == round {
			return r, true
		}
	}
	return model.ScoreRecord{}, false
}

func goldenRankInput(ev model.Event, teams []goldenTeam) (RankInput, map[int64]bool) {
	in := RankInput{
		Event:  &ev,
		Teams:  make([]model.Team, 0, len(teams)),
		Scores: make(map[int64][]model.ScoreRecord, len(teams)),
	}
	voided := make(map[int64]bool)
	for i, gt := range teams {
		id := int64(i + 1)
		in.Teams = append(in.Teams, model.Team{
			ID:        id,
			EventID:   ev.ID,
			TeamNo:    gt.No,
			Name:      gt.Name,
			School:    gt.School,
			Coach:     gt.Coach,
			GroupCode: gt.Group,
			Members:   gt.Members,
			Status:    model.TeamStatus(orDefault(gt.Status, string(model.TeamActive))),
		})
		// 未开赛的队伍：给空记录（而不是塞一条零值记录）——
		// 塞零值会让"有记录"与"无记录"在榜单上表现一致，把这条口径的差异盖掉。
		if len(gt.Records) > 0 {
			in.Scores[id] = gt.Records
		}
		if gt.Voided {
			voided[id] = true
		}
	}
	return in, voided
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// compareResult 返回结果差异描述；完全一致时返回空串。
// 这里用严格 == 而非 epsilon：Go 与 JS 的运算顺序一致，应当逐位相同。
func compareResult(got, want model.ScoreResult) string {
	if got == want {
		return ""
	}
	// 兜底诊断：若只差浮点末位，明确指出来，便于判断是「算法不同」还是「算术顺序不同」
	if math.Abs(got.Total-want.Total) < 1e-9 && math.Abs(got.Base-want.Base) < 1e-9 &&
		math.Abs(got.Bonus-want.Bonus) < 1e-9 && math.Abs(got.Penalty-want.Penalty) < 1e-9 &&
		got.Complete == want.Complete {
		return fmt.Sprintf("仅浮点末位差异：total %.17g vs %.17g", got.Total, want.Total)
	}
	return "存在实质差异"
}
