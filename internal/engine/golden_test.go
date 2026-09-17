package engine

import (
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
// 从 demo 原型里**原样抽取**前端 computeTotal / standings / maskPerson /
// maskMembers / validateEvent 后跑出来的期望值。
//
// 这里断言的是**浮点精确相等**，不是近似相等 —— 因为 Go 侧刻意保持了与 JS
// 完全相同的运算顺序，任何一处顺序差异都会立刻暴露成断言失败。
// ============================================================================

type goldenTeam struct {
	No      string            `json:"no"`
	Name    string            `json:"name"`
	Group   string            `json:"group"`
	School  string            `json:"school"`
	Coach   string            `json:"coach"`
	Members string            `json:"members"`
	Status  string            `json:"status"`
	Record  model.ScoreRecord `json:"record"` // 字段名与 golden 一致，可直接反序列化
	Expect  model.ScoreResult `json:"expect"`
}

type goldenStanding struct {
	Rank     int     `json:"rank"`
	No       string  `json:"no"`
	Name     string  `json:"name"`
	Group    string  `json:"group"`
	Base     float64 `json:"base"`
	Bonus    float64 `json:"bonus"`
	Penalty  float64 `json:"penalty"`
	Total    float64 `json:"total"`
	Time     float64 `json:"time"`
	Complete bool    `json:"complete"`
	Award    string  `json:"award"`
	Tie      bool    `json:"tie"`
}

type goldenPair struct {
	In  string `json:"in"`
	Out string `json:"out"`
}

type goldenFile struct {
	Source  string  `json:"source"`
	Note    string  `json:"note"`
	RefTime float64 `json:"refTime"`
	Events  []struct {
		ID      string           `json:"id"`
		Event   model.Event      `json:"event"`
		Teams   []goldenTeam     `json:"teams"`
		Ranking []goldenStanding `json:"standings"`
		FEValid struct {
			Errs  []string `json:"errs"`
			Warns []string `json:"warns"`
		} `json:"frontendValidation"`
	} `json:"events"`
	MaskPersonCases []goldenPair `json:"maskPersonCases"`
	MaskCases       []goldenPair `json:"maskCases"`
}

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
	return g
}

// TestParityWithFrontendScoring 单轮计分逐位一致。
func TestParityWithFrontendScoring(t *testing.T) {
	g := loadGolden(t)

	cases, mismatches := 0, 0
	for _, ge := range g.Events {
		ev := ge.Event
		for _, gt := range ge.Teams {
			cases++
			got := Score(&ev, gt.Record, g.RefTime)
			want := gt.Expect

			diff := compareResult(got, want)
			if diff != "" {
				mismatches++
				t.Errorf("%s / %s（%s）：与前端不一致\n  Go:   base=%v bonus=%v penalty=%v total=%v complete=%v\n  前端: base=%v bonus=%v penalty=%v total=%v complete=%v",
					ge.ID, gt.Name, gt.No,
					got.Base, got.Bonus, got.Penalty, got.Total, got.Complete,
					want.Base, want.Bonus, want.Penalty, want.Total, want.Complete)
			}
		}
	}
	t.Logf("计分逐位比对：%d 例，%d 例不一致", cases, mismatches)
}

// TestParityWithFrontendStandings 排名结果逐位一致（含名次、总分、奖项、并列标记）。
func TestParityWithFrontendStandings(t *testing.T) {
	g := loadGolden(t)

	for _, ge := range g.Events {
		t.Run(ge.ID, func(t *testing.T) {
			in := goldenRankInput(ge.Event, ge.Teams)
			got := Rank(in, RankOptions{RefTime: g.RefTime})

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
				if row.Result.Base != want.Base || row.Result.Bonus != want.Bonus || row.Result.Penalty != want.Penalty {
					t.Errorf("%s 分项：Go base=%v bonus=%v penalty=%v / 前端 base=%v bonus=%v penalty=%v",
						want.Name, row.Result.Base, row.Result.Bonus, row.Result.Penalty,
						want.Base, want.Bonus, want.Penalty)
				}
				if row.Duration != want.Time {
					t.Errorf("%s 用时：Go %v / 前端 %v", want.Name, row.Duration, want.Time)
				}
				if row.Result.Complete != want.Complete {
					t.Errorf("%s 完成度：Go %v / 前端 %v", want.Name, row.Result.Complete, want.Complete)
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
func TestParityWithFrontendMask(t *testing.T) {
	g := loadGolden(t)

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

// TestGoldenSeedEventsAreValid 种子配置必须无阻断性问题
// （前端校验同样为 0 error，两侧一致）。
func TestGoldenSeedEventsAreValid(t *testing.T) {
	g := loadGolden(t)

	for _, ge := range g.Events {
		ev := ge.Event
		res := ValidateEvent(&ev)
		if !res.OK() {
			t.Errorf("%s 种子配置被判定为不合法：%v", ge.ID, res.ErrorMessages())
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
	total := 0
	for _, ge := range g.Events {
		total += len(ge.Teams)
		if len(ge.Teams) == 0 || len(ge.Ranking) == 0 {
			t.Errorf("%s 的基准为空", ge.ID)
		}
		for _, gt := range ge.Teams {
			if gt.Expect.Complete && gt.Expect.Total == 0 && gt.Expect.Base > 0 {
				t.Errorf("%s/%s：已完成录入却算出总分 0，基准可疑", ge.ID, gt.Name)
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

func goldenRankInput(ev model.Event, teams []goldenTeam) RankInput {
	in := RankInput{
		Event:  &ev,
		Teams:  make([]model.Team, 0, len(teams)),
		Scores: make(map[int64][]model.ScoreRecord, len(teams)),
	}
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
		in.Scores[id] = []model.ScoreRecord{gt.Record}
	}
	return in
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
