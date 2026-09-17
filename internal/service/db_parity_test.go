package service_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/service"
)

// ============================================================================
// 端到端一致性：把前端基准种子经「service → 数据库 → 再取回来 → engine」
// 跑一遍，结果必须与前端原型的榜单逐位一致。
//
// 这条测试同时压住四层：
//
//	PostgreSQL 的 NUMERIC / JSONB 往返精度
//	store 层的 SQL 与扫描映射
//	service 层的取数（含「一次查询取全量成绩」的聚合）
//	engine 的计算
//
// 任何一层把数值弄坏了（JSONB 里的 88.0 回读成 88.0000001、成绩聚合漏队、
// 排序不稳定），这里都会立刻失败。
// ============================================================================

type goldenTeamRow struct {
	No      string            `json:"no"`
	Name    string            `json:"name"`
	Group   string            `json:"group"`
	School  string            `json:"school"`
	Coach   string            `json:"coach"`
	Members string            `json:"members"`
	Status  string            `json:"status"`
	Record  model.ScoreRecord `json:"record"`
}

type goldenStandingRow struct {
	Rank  int     `json:"rank"`
	No    string  `json:"no"`
	Name  string  `json:"name"`
	Group string  `json:"group"`
	Total float64 `json:"total"`
	Time  float64 `json:"time"`
	Award string  `json:"award"`
}

type goldenEvent struct {
	ID        string              `json:"id"`
	Event     model.Event         `json:"event"`
	Teams     []goldenTeamRow     `json:"teams"`
	Standings []goldenStandingRow `json:"standings"`
}

type goldenFile struct {
	Source  string        `json:"source"`
	RefTime float64       `json:"refTime"`
	Events  []goldenEvent `json:"events"`
}

func loadGoldenFixture(t *testing.T) goldenFile {
	t.Helper()
	p := filepath.Join("..", "..", "testdata", "frontend_golden.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("跳过：找不到前端基准 %s（先跑 node scripts/gen_frontend_golden.js）: %v", p, err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("解析前端基准失败: %v", err)
	}
	if len(g.Events) == 0 {
		t.Fatal("前端基准为空")
	}
	return g
}

func findGoldenEvent(g goldenFile, id string) (goldenEvent, bool) {
	for _, e := range g.Events {
		if e.ID == id {
			return e, true
		}
	}
	return goldenEvent{}, false
}

// goldenGroupRows 取基准中某个组别的榜单，按基准名次升序。
//
// 前端榜单是全赛项混合排名的，而公示表要求组内独立名次。
// 全局排序限制到某个子集时相对顺序不变，因此这两种排名是自洽的。
func goldenGroupRows(rows []goldenStandingRow, group string) []goldenStandingRow {
	out := make([]goldenStandingRow, 0, len(rows))
	for _, r := range rows {
		if r.Group == group {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

// ---------------------------------------------------------------------------

// TestSeedParityThroughDatabase 用基准种子把三个赛项经真实数据库跑一遍，
// 与前端原型的榜单逐行比对。
func TestSeedParityThroughDatabase(t *testing.T) {
	g := loadGoldenFixture(t)
	svc, _ := newSvc(t)
	ctx := operatorCtx("种子导入")

	for _, ge := range g.Events {
		t.Run(ge.ID, func(t *testing.T) {
			ev := ge.Event
			if _, err := svc.CreateEvent(ctx, &ev); err != nil {
				t.Fatalf("建赛项失败: %v", err)
			}

			for _, gt := range ge.Teams {
				created, err := svc.CreateTeam(ctx, model.TeamDraft{
					EventID: ge.ID, TeamNo: gt.No, Name: gt.Name, School: gt.School,
					Coach: gt.Coach, GroupCode: gt.Group, Members: gt.Members,
				})
				if err != nil {
					t.Fatalf("建队伍 %s 失败: %v", gt.No, err)
				}
				rec := gt.Record
				rec.TeamID = created.ID
				if rec.RoundNo == 0 {
					rec.RoundNo = 1
				}
				if _, err := svc.SaveScore(ctx, &rec); err != nil {
					t.Fatalf("录分 %s 失败: %v", gt.No, err)
				}
			}

			res, err := svc.Standings(ctx, ge.ID, service.StandingsOptions{})
			if err != nil {
				t.Fatalf("出榜单失败: %v", err)
			}
			if res.RefTime != g.RefTime {
				t.Errorf("基准时长 = %v，期望 %v（前端 REF_TIME）", res.RefTime, g.RefTime)
			}
			if len(res.Groups) != len(ge.Event.Groups) {
				t.Fatalf("组别数 = %d，期望 %d", len(res.Groups), len(ge.Event.Groups))
			}

			for _, grp := range res.Groups {
				want := goldenGroupRows(ge.Standings, grp.Group)
				if len(grp.Rows) != len(want) {
					t.Fatalf("组别「%s」队伍数 = %d，基准 %d", grp.Group, len(grp.Rows), len(want))
				}
				for i := range want {
					got := grp.Rows[i]
					if got.Team.TeamNo != want[i].No {
						t.Errorf("组别「%s」第 %d 名：Go 是 %s（%s），基准是 %s（%s）",
							grp.Group, i+1, got.Team.TeamNo, got.Team.Name, want[i].No, want[i].Name)
						continue
					}
					// 严格相等：数值经 PG 的 JSONB / NUMERIC 往返后必须原样回来
					if got.Result.Total != want[i].Total {
						t.Errorf("%s 总分：Go %v / 前端 %v（数据库往返改变了数值？）",
							got.Team.Name, got.Result.Total, want[i].Total)
					}
					if got.Duration != want[i].Time {
						t.Errorf("%s 用时：Go %v / 前端 %v", got.Team.Name, got.Duration, want[i].Time)
					}
					if got.Rank != i+1 {
						t.Errorf("%s 组内名次 = %d，期望 %d", got.Team.Name, got.Rank, i+1)
					}
				}
				// 组内独立授奖：第 1 名必须拿到奖项（占比 >0 时）
				if len(want) > 0 && grp.Rows[0].Award == "" {
					t.Errorf("组别「%s」第 1 名没有奖项", grp.Group)
				}
			}

			// 大屏：姓名必须已脱敏，分页元信息自洽
			page, err := svc.ScreenPage(ctx, ge.ID, 1)
			if err != nil {
				t.Fatalf("取大屏失败: %v", err)
			}
			if page.TotalRows != len(ge.Teams) {
				t.Errorf("大屏总行数 = %d，期望 %d", page.TotalRows, len(ge.Teams))
			}
			if page.PageSize != model.DefaultPageSize {
				t.Errorf("每屏条数 = %d，期望默认 %d", page.PageSize, model.DefaultPageSize)
			}
			for _, r := range page.Rows {
				for _, raw := range []string{"张一", "李二", "王三", "孙五", "冯九", "韩五"} {
					if strings.Contains(r.Members, raw) {
						t.Errorf("大屏泄露了未脱敏姓名：%q 出现在 %q", raw, r.Members)
					}
				}
				if r.TeamName == "" {
					t.Errorf("大屏队名不应为空：%+v", r)
				}
				if !strings.Contains(r.Members, "*") && r.Members != "—" {
					t.Errorf("大屏选手字段应已脱敏：%q", r.Members)
				}
			}
		})
	}
}

// TestScreenLockAndPagination 大屏的分页与「置顶 / 锁定本场」。
func TestScreenLockAndPagination(t *testing.T) {
	g := loadGoldenFixture(t)
	ge, ok := findGoldenEvent(g, "brain_planet")
	if !ok {
		t.Skip("基准里没有 brain_planet，跳过")
	}
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev := ge.Event
	if _, err := svc.CreateEvent(ctx, &ev); err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	for _, gt := range ge.Teams {
		if _, err := svc.CreateTeam(ctx, model.TeamDraft{
			EventID: ge.ID, TeamNo: gt.No, Name: gt.Name, School: gt.School,
			Coach: gt.Coach, GroupCode: gt.Group, Members: gt.Members,
		}); err != nil {
			t.Fatalf("建队伍失败: %v", err)
		}
	}

	cfg, err := svc.GetScreenConfig(ctx, ge.ID)
	if err != nil {
		t.Fatalf("读大屏配置失败: %v", err)
	}
	cfg.PageSize = 2
	cfg.IntervalSec = 10
	if _, err := svc.UpdateScreenConfig(ctx, cfg, "演示用：每屏 2 条"); err != nil {
		t.Fatalf("改大屏配置失败: %v", err)
	}

	page, err := svc.ScreenPage(ctx, ge.ID, 1)
	if err != nil {
		t.Fatalf("取大屏失败: %v", err)
	}
	if page.PageSize != 2 || page.TotalPage != 2 || len(page.Rows) != 2 {
		t.Fatalf("分页异常：pageSize=%d totalPage=%d rows=%d", page.PageSize, page.TotalPage, len(page.Rows))
	}
	if page.IntervalSec != 10 {
		t.Errorf("停留秒数 = %d，期望 10", page.IntervalSec)
	}

	// 越界页夹到末页，而不是返回空屏
	last, err := svc.ScreenPage(ctx, ge.ID, 99)
	if err != nil {
		t.Fatalf("取大屏失败: %v", err)
	}
	if last.Page != last.TotalPage || len(last.Rows) != 2 {
		t.Errorf("越界页应夹到末页：page=%d totalPage=%d rows=%d", last.Page, last.TotalPage, len(last.Rows))
	}

	// 锁定本场：locked=true，前端据此停播
	cfg.Pinned = "1001"
	if _, err := svc.UpdateScreenConfig(ctx, cfg, "现场异常，锁定本屏"); err != nil {
		t.Fatalf("锁定失败: %v", err)
	}
	page, err = svc.ScreenPage(ctx, ge.ID, 1)
	if err != nil {
		t.Fatalf("取大屏失败: %v", err)
	}
	if !page.Locked || page.Pinned != "1001" {
		t.Errorf("锁定状态未生效：locked=%v pinned=%q", page.Locked, page.Pinned)
	}
}

// TestExtraSlotSnapshotDoesNotPolluteMainTable 加时赛场内快照绝不污染队伍主库。
func TestExtraSlotSnapshotDoesNotPolluteMainTable(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev, err := svc.CreateEvent(ctx, brainPlanetEvent())
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	before, err := svc.ListTeams(ctx, ev.ID, true)
	if err != nil {
		t.Fatalf("查队伍失败: %v", err)
	}

	seat, err := svc.CreateSeat(ctx, "赛台 1", 0)
	if err != nil {
		t.Fatalf("建赛台失败: %v", err)
	}
	extra, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "下午", TimeRange: "14:00–17:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotExtra,
	})
	if err != nil {
		t.Fatalf("建加时赛场次失败: %v", err)
	}

	snaps := []model.Snapshot{
		{TeamNo: "9001", Name: "新星队", School: "成都教装展学校", Coach: "陈老师"},
		{TeamNo: "9002", Name: "闪电队", School: "青岛实验学校", Coach: "吴老师"},
		{TeamNo: "9002", Name: "闪电队（重复）", School: "同编号应被去重"},
	}
	saved, err := svc.SaveSnapshot(ctx, extra.ID, snaps, "加时赛队伍导入")
	if err != nil {
		t.Fatalf("写快照失败: %v", err)
	}
	if len(saved) != 2 {
		t.Errorf("同编号快照应被去重，实际保留 %d 条", len(saved))
	}

	after, err := svc.ListTeams(ctx, ev.ID, true)
	if err != nil {
		t.Fatalf("查队伍失败: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("加时赛快照污染了队伍主库：%d → %d 支", len(before), len(after))
	}
	for _, tm := range after {
		if tm.TeamNo == "9001" || tm.TeamNo == "9002" {
			t.Fatalf("主库中不应出现加时赛队伍 %s", tm.TeamNo)
		}
	}

	// 两条路互斥：正式场次不接受快照，加时赛场次不接受主库改派
	normal, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "上午", TimeRange: "09:00–12:00",
		EventID: ev.ID, GroupCode: "小学组", Type: model.SlotNormal,
	})
	if err != nil {
		t.Fatalf("建正式场次失败: %v", err)
	}
	if _, err := svc.SaveSnapshot(ctx, normal.ID, snaps, "不该成功"); err == nil {
		t.Error("正式场次不应接受场内快照")
	}
	if err := svc.AssignSlotTeams(ctx, extra.ID, []int64{1}, "不该成功"); err == nil {
		t.Error("加时赛场次不应接受主库改派")
	}

	// 同一赛台同一时段只能排一个场次
	if _, err := svc.CreateSlot(ctx, model.SlotDraft{
		SeatID: seat.ID, Period: "下午", TimeRange: "14:00–17:00",
		EventID: ev.ID, GroupCode: "初中组", Type: model.SlotNormal,
	}); err == nil {
		t.Error("同一赛台同一时段应只允许一个场次")
	}
}

// TestConfigSnapshotRestore 配置改错了能回滚，且回滚本身也可再回滚。
func TestConfigSnapshotRestore(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := operatorCtx("运营A")

	ev, err := svc.CreateEvent(ctx, brainPlanetEvent())
	if err != nil {
		t.Fatalf("建赛项失败: %v", err)
	}
	// 记下基线：两份权重各 0.5
	baseline, err := svc.ListConfigSnapshots(ctx, 10)
	if err != nil {
		t.Fatalf("查快照失败: %v", err)
	}
	if len(baseline) != 1 {
		t.Fatalf("应只有 1 份基线快照，实际 %d 份", len(baseline))
	}

	// 改坏：权重改成 0.9 / 0.9
	broken := *ev
	broken.Tasks = []model.Task{
		{ID: "focus", Name: "专注力任务", Type: model.TaskNumeric, MaxScore: fptr(100), Weight: 0.9, Control: model.CtrlSlider},
		{ID: "build", Name: "搭建任务", Type: model.TaskNumeric, MaxScore: fptr(100), Weight: 0.9, Control: model.CtrlSlider},
	}
	if _, err := svc.UpdateEvent(ctx, &broken, "误改权重"); err != nil {
		t.Fatalf("改配置失败: %v", err)
	}

	// 回滚到基线快照
	if err := svc.RestoreConfigSnapshot(ctx, baseline[0].ID, "权重改错，回滚"); err != nil {
		t.Fatalf("回滚失败: %v", err)
	}
	restored, err := svc.GetEvent(ctx, ev.ID)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if len(restored.Tasks) != 2 || restored.Tasks[0].Weight != 0.5 {
		t.Errorf("回滚后配置不符：%+v", restored.Tasks)
	}
	// 回滚前后的状态都留了档
	snaps, err := svc.ListConfigSnapshots(ctx, 10)
	if err != nil {
		t.Fatalf("查快照失败: %v", err)
	}
	if len(snaps) < 3 {
		t.Errorf("回滚应额外留档（基线 + 改配置前 + 回滚前 = 3 份），实际 %d 份", len(snaps))
	}
}
