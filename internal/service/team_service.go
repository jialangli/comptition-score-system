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
func (s *Service) CommitImport(ctx context.Context, eventID string, rows []ImportRow,
	selectedLines []int, note string) (*model.ImportLog, error) {

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
			return nil, fmt.Errorf("%w：第 %d 行 编号 %s（%s）",
				ErrConflictRows, item.Line, item.Row.TeamNo, strings.Join(item.Conflicts, "；"))
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
		created, updated := 0, 0
		for i := range preview.Rows {
			item := &preview.Rows[i]
			if !selected[item.Line] {
				continue
			}
			if item.Status != ImportInsert && item.Status != ImportUpdate {
				continue
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
			} else {
				updated++
			}
		}

		logEntry.Summary = preview.Summary
		logEntry.Detail = detail

		// 导入审计 + 操作审计，与队伍写入同事务
		if err := r.Audits.AppendImport(ctx, logEntry); err != nil {
			return err
		}
		return log(ctx, r, model.ActImport,
			fmt.Sprintf("赛项 %s 队伍导入", eventID),
			fmt.Sprintf("导出文件 %d 行（新增%d 更新%d 无变化%d 冲突%d）",
				len(preview.Rows), preview.Summary.Insert, preview.Summary.Update,
				preview.Summary.Skip, preview.Summary.Conflict),
			fmt.Sprintf("实际写入：新增 %d 支 / 更新 %d 支", created, updated),
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
