-- ============================================================================
-- 0005：争议工单表（disputes）
--
-- 背景：前端 P8 家族定义了一整套争议裁定流程，但后端**没有承载它的实体**——
-- 目前只有 score_change_requests（改分申请单），二者是两回事：
--
--   改分申请单：裁判发起「我要改这个分」→ 裁判长授权。方向是「申请改数」。
--   争议工单  ：现场出现两份成绩 / 申诉复核 → 裁判长裁定结论。方向是「判定是非」。
--
-- 缺口带来的具体问题（对应 PROGRESS.md P0-2）：
--   1. P8 待裁定队列无处查询 —— 后端没有「还有几件没裁」的数据源；
--   2. 裁定三态（P8a 维持原判 / P8b 授权改分 / P8c 取消资格）没有落点，
--      结论只存在于前端页面演示里，刷新即失；
--   3. E 项「离线补传发现同队同轮已存在服务端记录 → 自动建同步冲突告警单」
--      无处可建：当前 /sync 冲突时只返回一句错误字符串，两份成绩就这样静静躺着。
--
-- 设计要点：
--
--   1. **状态三态而非布尔**：待裁定 / 已裁定 / 已撤回。
--      撤回是独立状态而不是物理删除 —— 工单提错了也要留痕（谁提的、什么时候撤的），
--      这与「队伍弃赛只做软删除」是同一条原则。
--
--   2. **防重复靠部分唯一索引，不靠应用层先查再插**。
--      同一队同一轮同一类型，最多一条「待裁定」工单：
--      ux_disputes_one_open ... WHERE status='pending'。
--      为什么带 WHERE：已裁定 / 已撤回之后若确需再提（P8e「再裁定一次」），
--      必须还能提 —— 与 0003 的 ux_scr_one_pending 同一手法。
--      放在数据库是因为「先查再插」在并发补传下两个请求都会查到「没有」然后都插进去。
--
--   3. **允许再裁定**：Decide 故意不加 `AND status='pending'` 守卫
--      （与 0003 的 ChangeStore.Decide 相反）。P8e 明确支持「确需推翻时再裁定一次并留痕」，
--      每次裁定都写审计，历史结论可追溯；本表只保留**最新**结论。
--
--   4. **来源列区分人工与系统**：source = referee（裁判人工上报）/ system（系统·补传）。
--      二者共用同一队列与裁定流程，但「提出人 / 来源」列必须能分开 ——
--      否则运营分不清「这是人报的」还是「这是补传撞车自动生成的」。
--
--   5. **裁定结论只记录，不联动改榜**。verdict=disqualify（取消资格）后
--      名次顺延与递补由工作人员在后台执行（前端 P8c note 7 口径），
--      本表不越权去改 scores / teams，避免「裁定」与「执行」耦合。
--
-- 影响面：纯新增表，不改任何既有表结构，历史数据无需回填。
-- ============================================================================

BEGIN;

CREATE TABLE IF NOT EXISTS disputes (
    id           BIGSERIAL   PRIMARY KEY,
    contest_id   TEXT        NOT NULL DEFAULT 'ct_default'
                             REFERENCES contests(id) ON DELETE RESTRICT,
    team_id      BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    round_no     SMALLINT    NOT NULL CHECK (round_no BETWEEN 1 AND 2),
    -- 类型：重复打分（D-00x 主场景）/ 同步冲突（离线补传撞车）/ 其他
    kind         TEXT        NOT NULL CHECK (kind IN ('duplicate', 'sync_conflict', 'other')),
    -- 来源：referee=裁判人工上报；system=系统自动生成（离线补传冲突）
    source       TEXT        NOT NULL CHECK (source IN ('referee', 'system')),
    -- 状态：pending=待裁定 / decided=已裁定 / withdrawn=已撤回（处理前可撤）
    status       TEXT        NOT NULL DEFAULT 'pending'
                             CHECK (status IN ('pending', 'decided', 'withdrawn')),
    reason       TEXT        NOT NULL DEFAULT '',   -- 提出原因（人工填写 / 系统生成）
    operator     TEXT        NOT NULL DEFAULT '',   -- 提出人；系统建单时写「系统·补传」
    -- 裁定结论：仅已裁定时有值。uphold=维持原判(P8a) / adjust=授权改分(P8b) / disqualify=取消资格(P8c)
    verdict      TEXT        CHECK (verdict IN ('uphold', 'adjust', 'disqualify')),
    decider      TEXT        NOT NULL DEFAULT '',   -- 裁定人（裁判长）
    decision_reason TEXT     NOT NULL DEFAULT '',   -- 裁定原因（必填，随裁定单留痕）
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at   TIMESTAMPTZ                        -- 裁定 / 撤回时间；待裁定时为 NULL
);

-- 核心约束：同一队同一轮同一类型，最多一条「待裁定」工单。
CREATE UNIQUE INDEX IF NOT EXISTS ux_disputes_one_open
    ON disputes (contest_id, team_id, round_no, kind)
    WHERE status = 'pending';

-- 待裁定队列入口：先到先裁
CREATE INDEX IF NOT EXISTS ix_disputes_open_created
    ON disputes (contest_id, created_at)
    WHERE status = 'pending';

-- 按队伍查历史工单（含已裁定 / 已撤回），用于成绩单页标注「（成绩作废）」
CREATE INDEX IF NOT EXISTS ix_disputes_team
    ON disputes (contest_id, team_id, created_at DESC);

COMMENT ON TABLE  disputes               IS '争议工单：现场申诉与补传冲突的裁定载体，全程留痕';
COMMENT ON COLUMN disputes.kind          IS 'duplicate=重复打分 / sync_conflict=离线补传同步冲突 / other=其他';
COMMENT ON COLUMN disputes.source        IS 'referee=裁判人工上报 / system=系统自动生成（补传冲突）';
COMMENT ON COLUMN disputes.status        IS 'pending=待裁定 / decided=已裁定 / withdrawn=已撤回';
COMMENT ON COLUMN disputes.verdict       IS '裁定结论：uphold 维持原判 / adjust 授权改分 / disqualify 取消资格；仅已裁定有值';
COMMENT ON COLUMN disputes.decided_at    IS '裁定或撤回的时间；待裁定时为 NULL';

COMMIT;
