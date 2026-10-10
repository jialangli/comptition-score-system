package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/jialangli/comptition-score-server/internal/model"
	"github.com/jialangli/comptition-score-server/internal/store"
)

// ============================================================================
// 队伍用例
//
// 两条业务红线：
//
//  1. **退赛用软删除**（status=withdrawn），绝不物理删队 ——
//     历史成绩、公示记录、申诉取证都要靠这条记录。
//  2. **物理删队会被外键拦住**：带成绩的队伍删不掉，服务层把 ErrInUse
//     翻译成可读提示，而不是让 DBA 级的错误码直接冒到界面上。
//
// 五类改动全部留痕：弃赛 / 恢复 / 改组 / 改信息 / 删队。
// ============================================================================

// ListTeams 列出赛项下的队伍。
//
// includeWithdrawn=true 用于队伍管理页（要看得到弃赛记录），
// false 用于打分 / 排名场景。
func (s *Service) ListTeams(ctx context.Context, eventID string, includeWithdrawn bool) ([]model.Team, error) {
	return s.ro().Teams.ListByEvent(ctx, eventID, includeWithdrawn)
}

// GetTeam 读取队伍。
func (s *Service) GetTeam(ctx context.Context, id int64) (*model.Team, error) {
	return s.ro().Teams.Get(ctx, id)
}

// GetTeamByNo 按「赛项 + 队伍编号」读取队伍。
//
// 用途是离线批量上行：现场录分时前端只知道队伍编号，
// 队伍 ID 由后端分配、前端不一定拿到过。用编号定位更贴近现场实际。
func (s *Service) GetTeamByNo(ctx context.Context, eventID, teamNo string) (*model.Team, error) {
	return s.ro().Teams.GetByNo(ctx, eventID, teamNo)
}

// CreateTeam 手工新增一支队伍。
//
// 「一号一队」由数据库唯一索引兜底：编号重复时返回 store.ErrDuplicate，
// 由 api 层转成 409 并提示「该编号已存在，请核对是否重复报名」。
func (s *Service) CreateTeam(ctx context.Context, draft model.TeamDraft) (*model.Team, error) {
	if err := draft.Validate(); err != nil {
		return nil, err
	}
	ev, err := s.ro().Events.Get(ctx, draft.EventID)
	if err != nil {
		return nil, err
	}
	if !ev.GroupAllowed(draft.GroupCode) {
		return nil, &model.FieldError{
			Field: "group",
			Msg:   fmt.Sprintf("组别「%s」不属于赛项「%s」", draft.GroupCode, ev.Name),
		}
	}
	if draft.Source == "" {
		draft.Source = model.SourceManual
	}

	t := &model.Team{
		EventID: draft.EventID, TeamNo: draft.TeamNo, Name: draft.Name,
		School: draft.School, Coach: draft.Coach, GroupCode: draft.GroupCode,
		Members: draft.Members, Status: model.TeamActive, Source: draft.Source,
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Teams.Create(ctx, t); err != nil {
			return err
		}
		return log(ctx, r, model.ActConfig, fmt.Sprintf("队伍 %s（%s）", t.Name, t.TeamNo),
			"—", fmt.Sprintf("%s / %s / %s", t.School, t.Coach, t.GroupCode), "手工新增队伍")
	}); err != nil {
		return nil, err
	}
	return t, nil
}

// UpdateTeam 修改队伍信息（名称 / 学校 / 教练 / 组别 / 成员）。
//
// 组别变化按「改组」留痕，其余按「改配置」留痕 —— 因为只有改组会影响赛项归属。
//
// ⚠ 语义是**整体覆盖**而不是字段级 patch：未提供的字段会被清空。
// 调用方（api 层）必须回传完整值。这样做是为了避免「空字符串到底是想清空、
// 还是忘了传」这个歧义 —— 队伍信息只有五个短字段，整体回传更省心。
func (s *Service) UpdateTeam(ctx context.Context, id int64, in model.Team, reason string) (*model.Team, error) {
	old, err := s.ro().Teams.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// 先留住原组别 —— 后面 old 会被就地改写，而审计的「旧值」必须是改动前的
	prevGroup := old.GroupCode

	// 只允许改动这几个字段：队伍编号与来源不可改 ——
	// 改了编号等于换了一支队伍，应该走「删掉误录的 + 重新导入」并留下两条独立痕迹。
	old.Name = strings.TrimSpace(in.Name)
	old.School = strings.TrimSpace(in.School)
	old.Coach = strings.TrimSpace(in.Coach)
	old.Members = strings.TrimSpace(in.Members)
	newGroup := strings.TrimSpace(in.GroupCode)

	if old.Name == "" {
		return nil, &model.FieldError{Field: "name", Msg: "队伍名称不能为空"}
	}
	groupChanged := newGroup != "" && newGroup != old.GroupCode
	if groupChanged {
		ev, err := s.ro().Events.Get(ctx, old.EventID)
		if err != nil {
			return nil, err
		}
		if !ev.GroupAllowed(newGroup) {
			return nil, &model.FieldError{
				Field: "group",
				Msg:   fmt.Sprintf("组别「%s」不属于赛项「%s」", newGroup, ev.Name),
			}
		}
		if err := requireReason(reason); err != nil {
			return nil, err
		}
		old.GroupCode = newGroup
	}
	if !groupChanged && strings.TrimSpace(reason) == "" {
		reason = "队伍信息修正"
	}

	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Teams.Update(ctx, old); err != nil {
			return err
		}
		if groupChanged {
			return log(ctx, r, model.ActRegroup, teamLabel(old),
				"组别 "+prevGroup, "组别 "+newGroup, reason)
		}
		return log(ctx, r, model.ActConfig, teamLabel(old), "队伍信息", describeTeam(old), reason)
	}); err != nil {
		return nil, err
	}
	return old, nil
}

// WithdrawTeam 弃赛（软删除）。
//
// 必须说明原因：弃赛会直接影响排名与奖项，事后必须能回答「谁批的、为什么」。
func (s *Service) WithdrawTeam(ctx context.Context, id int64, reason string) (*model.Team, error) {
	if err := requireReason(reason); err != nil {
		return nil, err
	}
	t, err := s.ro().Teams.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status == model.TeamWithdrawn {
		return t, nil // 幂等：重复弃赛不报错、也不再留一条重复痕迹
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Teams.SetStatus(ctx, id, model.TeamWithdrawn); err != nil {
			return err
		}
		return log(ctx, r, model.ActWithdraw, teamLabel(t), "在册", "弃赛", reason)
	}); err != nil {
		return nil, err
	}
	t.Status = model.TeamWithdrawn
	return t, nil
}

// RestoreTeam 恢复弃赛队伍。
func (s *Service) RestoreTeam(ctx context.Context, id int64, reason string) (*model.Team, error) {
	t, err := s.ro().Teams.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Status == model.TeamActive {
		return t, nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "撤销弃赛"
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Teams.SetStatus(ctx, id, model.TeamActive); err != nil {
			return err
		}
		return log(ctx, r, model.ActWithdraw, teamLabel(t), "弃赛", "在册（恢复）", reason)
	}); err != nil {
		return nil, err
	}
	t.Status = model.TeamActive
	return t, nil
}

// AssignTeamSeat 归台：把队伍落到某个赛台与台内顺位；seatID = nil 表示取消归台。
//
// 与「弃赛」的区别要说清：弃赛是**整队退赛**（软删除、不进榜单、要走「恢复」才回来）；
// 归台只是换个位置打，成绩与名次一切照旧。
//
// 留痕用独立的「调赛台」动作而不是并进「改配置」：排查
// 「这队怎么跑到 3 号台去了」时，只有独立动作名能一眼定位。
//
// 幂等：目标值与当前值相同时直接返回，不写库也不留痕 ——
// 现场是「点一下看着没反应就再点一下」的环境，重复痕迹会淹没真正的改动。
func (s *Service) AssignTeamSeat(ctx context.Context, id int64, seatID *int64, order int, reason string) (*model.Team, error) {
	t, err := s.ro().Teams.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// 取消归台时顺位必须一并归零，否则会留下「未排台但顺位 5」这种
	// 没有对应赛台的怪状态（下一个读它的人只能靠猜）。
	if seatID != nil && *seatID <= 0 {
		seatID = nil
	}
	if seatID == nil {
		order = 0
	} else {
		if order < 1 {
			return nil, &model.FieldError{Field: "seatOrder", Msg: "台内顺位从 1 起"}
		}
		// 先查赛台存在性：外键也会兜底，但那会把「赛台不存在」变成
		// 一个笼统的约束错误，运营看不出该去建台还是改 id。
		if _, err := s.ro().Seats.Get(ctx, *seatID); err != nil {
			return nil, err
		}
	}
	if sameSeat(t.SeatID, seatID) && t.SeatOrder == order {
		return t, nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "调整赛台与顺位"
	}
	before, after := seatText(t.SeatID, t.SeatOrder), seatText(seatID, order)
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Teams.SetSeat(ctx, id, seatID, order); err != nil {
			return err
		}
		return log(ctx, r, model.ActSeat, teamLabel(t), before, after, reason)
	}); err != nil {
		return nil, err
	}
	t.SeatID, t.SeatOrder = seatID, order
	return t, nil
}

// SetTeamSession 设置队伍参赛轮次（仅第 1 轮 / 仅第 2 轮 / 两轮都打）。
//
// 这是「某队下午不来」的**正解**，比弃赛轻得多：
//
//	改参赛轮次  成绩**全部保留**，榜单在总分下标注「仅第 N 轮」，发布门按新轮次重算
//	弃赛        整队退赛（软删除），不进榜单，需走「恢复」才能回来
//
// 必须说明原因：它会直接改变「发布门是否就位」与取优轮的口径，
// 事后要能回答「谁把两轮改成一轮的」。
func (s *Service) SetTeamSession(ctx context.Context, id int64, session model.TeamSession, reason string) (*model.Team, error) {
	t, err := s.ro().Teams.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if session == "" {
		session = model.SessionBoth
	}
	if !session.Valid() {
		return nil, &model.FieldError{Field: "session", Msg: "参赛轮次只能是 1 / 2 / both"}
	}
	// 弃赛队的参赛轮次不产生任何输出，而「把弃赛队改成 two」极容易被误当成恢复参赛 ——
	// 明确拒绝并给出正确路径，比默默改掉一个不生效的字段好。
	if t.Status == model.TeamWithdrawn {
		return nil, &model.FieldError{
			Field: "session",
			Msg:   "该队已弃赛，改参赛轮次不影响任何结果；要恢复参赛请走「恢复」",
		}
	}
	if t.Session == session {
		return t, nil // 幂等
	}
	if err := requireReason(reason); err != nil {
		return nil, err
	}
	if err := s.tx(ctx, func(r store.Repos) error {
		if err := r.Teams.SetSession(ctx, id, session); err != nil {
			return err
		}
		return log(ctx, r, model.ActConfig, teamLabel(t),
			"参赛轮次 "+sessionText(t.Session), "参赛轮次 "+sessionText(session), reason)
	}); err != nil {
		return nil, err
	}
	t.Session = session
	return t, nil
}

// sameSeat 判断两个「归台」是否为同一目标（含两者都未排台）。
func sameSeat(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// seatText 归台的中文描述（审计留痕与提示文案用）。
func seatText(seatID *int64, order int) string {
	if seatID == nil {
		return "未排台"
	}
	return fmt.Sprintf("赛台 #%d · 顺位 %d", *seatID, order)
}

// sessionText 参赛轮次的审计 / 提示文案。
//
// 取值由数据库 CHECK（1 / 2 / both）+ NOT NULL DEFAULT 'both' 双重兜底，
// 从库里读回来的值必然合法。这里仍兜一道：**内存里新构造的 Team 对象**
// （未经读库）Session 可能为空，那时按默认口径说成「两轮」——
// 留一个空字符串会让人读成「这队一轮都不打」。
//
// 注意不要写成「两轮（默认）」：库里存的就是 both，不是「没设过」，
// 文案必须与库里的值同口径，否则审计看着像在说另一件事。
func sessionText(s model.TeamSession) string {
	if !s.Valid() {
		return model.SessionBoth.Label()
	}
	return s.Label()
}

// DeleteTeam 物理删除队伍。
//
// 已录入成绩的队伍会被数据库外键 RESTRICT 拦下（返回 store.ErrInUse），
// 这是刻意的：删掉带成绩的队伍等于毁掉证据，正确做法是「弃赛」。
func (s *Service) DeleteTeam(ctx context.Context, id int64, reason string) error {
	t, err := s.ro().Teams.Get(ctx, id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "误录队伍清理"
	}
	return s.tx(ctx, func(r store.Repos) error {
		if err := r.Teams.Delete(ctx, id); err != nil {
			return err
		}
		return log(ctx, r, model.ActDelete, teamLabel(t), describeTeam(t), "已删除", reason)
	})
}

// ---------------------------------------------------------------------------
// 报名导入（WRC 官网导出表）
//
// 五步链路：解析（由调用方的 xlsx 层完成）→ 列映射 → 校验 → 四色比对 → 勾选入库。
// 本文件的职责是后三步。核心语义是**合并式 upsert**：
//
//	文件里没出现的队伍保持原样，绝不因为「这次没导出」就被删掉。
// ============================================================================

// ImportRow 一行报名数据。字段由 xlsx / CSV 解析层按列映射归一后提供。
type ImportRow struct {
	TeamNo  string `json:"no"`
	Name    string `json:"name"`
	School  string `json:"school"`
	Coach   string `json:"coach"`
	Group   string `json:"group"`
	Members string `json:"members"`
	LineNo  int    `json:"lineNo,omitempty"` // 原始行号，便于运营定位到具体哪一行
}

// ImportOverrideMode 冲突行的人工裁决方式。
//
// 「以文件为准」是**唯一**能绕开「冲突行不得入库」的途径，且必须**逐行显式声明** ——
// 这样它不会变成一个默默生效的开关：调用方要在 overrides 里点名第几行、怎么裁。
type ImportOverrideMode string

const (
	// OverrideFile 以文件为准：即使服务端判定冲突，也按文件值强制写入库内。
	OverrideFile ImportOverrideMode = "file"
	// OverrideDB 以库内为准：丢弃文件值（等同不写入，但显式记一笔，便于追溯"这行被人工判过"）。
	OverrideDB ImportOverrideMode = "db"
	// OverrideSkip 跳过：不写入。
	OverrideSkip ImportOverrideMode = "skip"
)

// Valid 校验裁决取值是否合法。
func (m ImportOverrideMode) Valid() bool {
	switch m {
	case OverrideFile, OverrideDB, OverrideSkip:
		return true
	}
	return false
}

// ImportOverride 一行的人工裁决（行号 → 方式）。
//
// 用行号而不是队伍编号定位：同一份文件里可能出现两条同编号的行，
// 那正是「编号重复」这类冲突本身，用编号根本区分不开裁的是哪一条。
type ImportOverride struct {
	Line int
	Mode ImportOverrideMode
}

// ImportStatus 单行变更类型（界面上的四色）。
type ImportStatus string

const (
	ImportInsert   ImportStatus = "insert"   // 🟢 新增
	ImportUpdate   ImportStatus = "update"   // 🟡 更新
	ImportSkip     ImportStatus = "skip"     // ⚪ 无变化
	ImportConflict ImportStatus = "conflict" // 🔴 冲突，必须人工裁决
)

// FieldChange 单个字段的「旧 → 新」。
type FieldChange struct {
	Field  string `json:"field"`
	Label  string `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// ImportDiffRow 一行的比对结果。
type ImportDiffRow struct {
	// Line 该行在源文件中的行号，也是勾选入库时定位一行的唯一标识。
	// 不能用队伍编号定位：同编号可能出现多行，那正是「编号重复」这类冲突本身。
	Line          int           `json:"line"`
	Status        ImportStatus  `json:"status"`
	Row           ImportRow     `json:"row"`
	ExistingID    int64         `json:"existingId,omitempty"`
	Changes       []FieldChange `json:"changes,omitempty"`
	Conflicts     []string      `json:"conflicts,omitempty"`
	FirstSeenLine int           `json:"firstSeenLine,omitempty"` // 编号重复时指向首次出现的行
	Selectable    bool          `json:"selectable"`              // 是否可勾选入库
	// CanOverride 该冲突能否由人工裁决「以文件为准」强制覆盖。
	//
	// 只对**结构性变更**开放：库内已有成绩的队伍改组别 —— 这是"现场确实要改"的场景，
	// 需要一个明确的出口。而「编号为空 / 组别不属于赛项」这类是数据本身的错误，
	// 覆盖只会把脏数据写进库，所以恒为 false。
	CanOverride bool `json:"canOverride"`
}

// ImportPreview 预览结果。
type ImportPreview struct {
	EventID string              `json:"eventId"`
	Rows    []ImportDiffRow     `json:"rows"`
	Summary model.ImportSummary `json:"summary"`
}

// PreviewImport 比对报名数据与库内队伍，产出四色清单（不落库）。
//
// 校验规则（与前端行为一致，但把「前端恒真检查」的漏洞补上了）：
//
//	编号必填且纯数字        → 否则 conflict
//	编号在文件内重复        → 后出现的判 conflict，并指向首次出现的行号
//	名称 / 组别必填          → 否则 conflict
//	组别必须属于该赛项       → 否则 conflict（这是最容易被漏掉的一类错）
func (s *Service) PreviewImport(ctx context.Context, eventID string, rows []ImportRow) (*ImportPreview, error) {
	ev, err := s.ro().Events.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	existing, err := s.ro().Teams.ListByEvent(ctx, eventID, true)
	if err != nil {
		return nil, err
	}
	byNo := make(map[string]model.Team, len(existing))
	for _, t := range existing {
		byNo[t.TeamNo] = t
	}
	// 一次性取本赛项全部成绩，用于判「这支队是否已有成绩」（避免逐队查询）
	scoreMap, err := s.ro().Scores.ListByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	hasScore := make(map[int64]bool, len(scoreMap))
	for teamID, recs := range scoreMap {
		if len(recs) > 0 {
			hasScore[teamID] = true
		}
	}

	out := &ImportPreview{EventID: eventID, Rows: make([]ImportDiffRow, 0, len(rows))}
	firstSeen := map[string]int{}

	for i, r := range rows {
		line := rowLine(r, i)
		r.TeamNo = strings.TrimSpace(r.TeamNo)
		r.Name = strings.TrimSpace(r.Name)
		r.Group = strings.TrimSpace(r.Group)

		item := ImportDiffRow{Line: line, Row: r}

		// —— 校验：任何一条不通过即 conflict，不可勾选 ——
		switch {
		case r.TeamNo == "":
			item.Conflicts = append(item.Conflicts, "队伍编号为空")
		case !isAllDigits(r.TeamNo):
			item.Conflicts = append(item.Conflicts, "队伍编号必须为纯数字："+r.TeamNo)
		}
		if r.Name == "" {
			item.Conflicts = append(item.Conflicts, "队伍名称为空")
		}
		if r.Group == "" {
			item.Conflicts = append(item.Conflicts, "组别为空")
		} else if !ev.GroupAllowed(r.Group) {
			item.Conflicts = append(item.Conflicts, fmt.Sprintf(
				"组别「%s」不属于赛项「%s」（可选：%s）", r.Group, ev.Name, strings.Join(ev.Groups, "、")))
		}
		if prev, dup := firstSeen[r.TeamNo]; dup {
			item.Conflicts = append(item.Conflicts,
				fmt.Sprintf("编号 %s 在文件内重复（首次出现在第 %d 行）", r.TeamNo, prev))
			item.FirstSeenLine = prev
		} else if r.TeamNo != "" {
			firstSeen[r.TeamNo] = line
		}

		if len(item.Conflicts) > 0 {
			item.Status = ImportConflict
			item.Selectable = false
			out.Summary.Conflict++
			out.Rows = append(out.Rows, item)
			continue
		}

		// —— 与库内比对 ——
		old, exists := byNo[r.TeamNo]
		if !exists {
			item.Status = ImportInsert
			item.Selectable = true
			out.Summary.Insert++
			out.Rows = append(out.Rows, item)
			continue
		}

		item.ExistingID = old.ID
		item.Changes = diffTeam(old, r)
		// 结构性变更 + 该队已有成绩：直接写会破坏成绩与队伍的归属，判为冲突。
		// 但它与"数据本身有错"不同 —— 现场确实可能要求改组别，
		// 所以标 CanOverride，留一个人工裁决「以文件为准」的出口。
		if hasScore[old.ID] && strings.TrimSpace(old.GroupCode) != r.Group {
			item.Status = ImportConflict
			item.Selectable = false
			item.CanOverride = true
			item.Conflicts = append(item.Conflicts, fmt.Sprintf(
				"该队已有成绩，变更组别（%s → %s）会影响成绩归属；确需变更请人工裁决「以文件为准」",
				old.GroupCode, r.Group))
			out.Summary.Conflict++
			out.Rows = append(out.Rows, item)
			continue
		}
		if len(item.Changes) == 0 {
			item.Status = ImportSkip
			item.Selectable = false // 没有变化就不用写，勾了也没意义
			out.Summary.Skip++
		} else {
			item.Status = ImportUpdate
			item.Selectable = true
			out.Summary.Update++
		}
		out.Rows = append(out.Rows, item)
	}
	return out, nil
}

// CommitImport 执行入库。
//
// 两个关键设计：
//
//  1. **服务端重新算一遍比对结果**，只采纳客户端勾选的行。
//     预览与提交之间可能隔着几分钟，期间别的运营可能已经导过一次，
//     也可能有人手工改了报文 —— 重算能挡掉这两类情况。
//  2. **用「行号」而不是「队伍编号」定位勾选项**。
//     同一份文件里可能出现两条同编号的行（正是「编号重复」这种冲突），
//     用编号定位根本无法区分它们到底勾了哪一条。
//  3. **冲突行默认拒绝整批入库**。只有同时满足两条才放行：
//     ① overrides 里显式声明「以文件为准」；② 该行 CanOverride（结构性变更，非数据错误）。
//     放行会在明细里标 forced=true，并在操作审计里写明强制覆盖了几行。
func (s *Service) CommitImport(ctx context.Context, eventID string, rows []ImportRow,
	selectedLines []int, overrides []ImportOverride, note string) (*model.ImportLog, error) {

	if len(selectedLines) == 0 {
		return nil, ErrNothingSelected
	}
	preview, err := s.PreviewImport(ctx, eventID, rows)
	if err != nil {
		return nil, err
	}

	selected := make(map[int]bool, len(selectedLines))
	for _, ln := range selectedLines {
		selected[ln] = true
	}

	// 人工裁决：行号 → 方式。未声明的行一律按默认处理（冲突即拒绝）。
	ovr := make(map[int]ImportOverrideMode, len(overrides))
	for _, o := range overrides {
		ovr[o.Line] = o.Mode
	}

	logEntry := &model.ImportLog{
		Source:   "excel",
		Operator: CurrentUser(ctx),
		Detail:   []any{},
		Status:   "success",
	}
	detail := make([]any, 0, len(preview.Rows))

	for i := range preview.Rows {
		item := &preview.Rows[i]
		if !selected[item.Line] {
			continue
		}
		switch item.Status {
		case ImportInsert, ImportUpdate:
			detail = append(detail, map[string]any{
				"line":    item.Line,
				"act":     string(item.Status),
				"no":      item.Row.TeamNo,
				"name":    item.Row.Name,
				"changes": item.Changes,
			})
		case ImportSkip:
			// 勾了但状态是「无变化」：不算错，只是没有写入，明确记录以免运营困惑
			detail = append(detail, map[string]any{
				"line": item.Line, "act": "ignored",
				"no": item.Row.TeamNo, "reason": "库内数据无变化",
			})
		case ImportConflict:
			if ovr[item.Line] != OverrideFile {
				return nil, fmt.Errorf("%w：第 %d 行 编号 %s（%s）",
					ErrConflictRows, item.Line, item.Row.TeamNo, strings.Join(item.Conflicts, "；"))
			}
			if !item.CanOverride {
				return nil, fmt.Errorf("%w：第 %d 行 编号 %s 的冲突不允许「以文件为准」覆盖（%s）—— 这是数据本身的错误，请改文件后重导",
					ErrConflictRows, item.Line, item.Row.TeamNo, strings.Join(item.Conflicts, "；"))
			}
			// 显式声明「以文件为准」：明细标明是强制覆盖。
			// act 先占位，写入后按**实际 Upsert 结果**回填 ——
			// 校验类冲突在查库之前就返回了，这里拿不到「库内是否已有该编号」。
			detail = append(detail, map[string]any{
				"line":    item.Line,
				"act":     string(ImportUpdate),
				"no":      item.Row.TeamNo,
				"name":    item.Row.Name,
				"changes": item.Changes,
				"forced":  true,
				"reason":  "服务端判定为冲突，已按人工裁决「以文件为准」覆盖库内：" + strings.Join(item.Conflicts, "；"),
			})
		}
	}

	if len(detail) == 0 {
		logEntry.Summary = preview.Summary
		logEntry.Detail = []any{}
		return logEntry, nil
	}

	if strings.TrimSpace(note) == "" {
		note = "WRC 报名导入"
	}

	err = s.tx(ctx, func(r store.Repos) error {
		created, updated, forcedCount := 0, 0, 0
		forcedActs := make(map[int]string, 4) // 行号 → 覆盖行的实际写入动作
		for i := range preview.Rows {
			item := &preview.Rows[i]
			if !selected[item.Line] {
				continue
			}
			// 冲突行只有被显式裁决为「以文件为准」时才写入（前面已挡掉其它情况）
			forced := item.Status == ImportConflict && ovr[item.Line] == OverrideFile
			if !forced && item.Status != ImportInsert && item.Status != ImportUpdate {
				continue
			}
			if forced {
				forcedCount++
			}
			t := &model.Team{
				EventID: eventID, TeamNo: item.Row.TeamNo, Name: item.Row.Name,
				School: item.Row.School, Coach: item.Row.Coach,
				GroupCode: item.Row.Group, Members: item.Row.Members,
				Status: model.TeamActive, Source: model.SourceExcel,
			}
			isNew, err := r.Teams.Upsert(ctx, t)
			if err != nil {
				return err
			}
			if isNew {
				created++
				if forced {
					forcedActs[item.Line] = string(ImportInsert)
				}
			} else {
				updated++
				if forced {
					forcedActs[item.Line] = string(ImportUpdate)
				}
			}
		}

		// 覆盖行的动作按实际写入结果回填（见上：预览阶段判不出 insert/update）
		for _, d := range detail {
			m, ok := d.(map[string]any)
			if !ok {
				continue
			}
			if f, _ := m["forced"].(bool); !f {
				continue
			}
			if ln, ok := m["line"].(int); ok {
				if act, hit := forcedActs[ln]; hit {
					m["act"] = act
				}
			}
		}

		logEntry.Summary = preview.Summary
		logEntry.Detail = detail

		// 导入审计 + 操作审计，与队伍写入同事务
		if err := r.Audits.AppendImport(ctx, logEntry); err != nil {
			return err
		}
		after := fmt.Sprintf("实际写入：新增 %d 支 / 更新 %d 支", created, updated)
		if forcedCount > 0 {
			after += fmt.Sprintf("（其中 %d 行为人工裁决「以文件为准」，强制覆盖库内）", forcedCount)
		}
		return log(ctx, r, model.ActImport,
			fmt.Sprintf("赛项 %s 队伍导入", eventID),
			fmt.Sprintf("导出文件 %d 行（新增%d 更新%d 无变化%d 冲突%d）",
				len(preview.Rows), preview.Summary.Insert, preview.Summary.Update,
				preview.Summary.Skip, preview.Summary.Conflict),
			after,
			note)
	})
	if err != nil {
		return nil, err
	}
	return logEntry, nil
}

// ---------------------------------------------------------------------------

func diffTeam(old model.Team, r ImportRow) []FieldChange {
	var out []FieldChange
	add := func(field, label, before, after string) {
		if strings.TrimSpace(before) == strings.TrimSpace(after) {
			return
		}
		out = append(out, FieldChange{Field: field, Label: label, Before: before, After: after})
	}
	add("name", "队伍名称", old.Name, r.Name)
	add("school", "学校", old.School, r.School)
	add("coach", "指导教师", old.Coach, r.Coach)
	add("group", "组别", old.GroupCode, r.Group)
	add("members", "选手", old.Members, r.Members)
	return out
}

func teamLabel(t *model.Team) string {
	if t == nil {
		return ""
	}
	return fmt.Sprintf("%s（%s）", t.Name, t.TeamNo)
}

func describeTeam(t *model.Team) string {
	if t == nil {
		return ""
	}
	return fmt.Sprintf("学校=%s；教练=%s；组别=%s；选手=%s",
		t.School, t.Coach, t.GroupCode, t.Members)
}

// rowLine 取一行的有效行号：优先用解析层给出的源文件行号，
// 没给则退化成序号（从 1 开始）。两侧必须用同一套规则，
// 否则预览与提交会对不上「勾了哪一行」。
func rowLine(r ImportRow, i int) int {
	if r.LineNo > 0 {
		return r.LineNo
	}
	return i + 1
}

func isAllDigits(s string) bool {
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
