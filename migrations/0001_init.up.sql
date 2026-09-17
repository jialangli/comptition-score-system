-- ============================================================================
-- 赛事统分后台管理系统 · 初始 Schema
-- 对齐：《赛事统分系统 配置Schema v1》《赛事评分系统 需求确认单 v1.3》
-- 目标库：PostgreSQL 17
--
-- 设计要点
--   1. 规则类字段用 JSONB，字段名与前端 Schema v1 逐字一致 → 省掉映射层
--   2. 赛台是"赛事级资源"，不带赛项归属（支持跨赛项共用）
--   3. 加时赛队伍存 slot_snapshots 独立表 → 物理上杜绝污染主库
--   4. 唯一索引落实「一号一队」；删队用 status 软删除，不物理删
--   5. 外键一律 RESTRICT，避免误删带成绩的队伍
-- ============================================================================

BEGIN;

-- ----------------------------------------------------------------------------
-- 1. 赛项（events）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS events (
    id              TEXT        PRIMARY KEY,
    name            TEXT        NOT NULL,
    groups          JSONB       NOT NULL DEFAULT '[]'::jsonb,
    score_rule      JSONB       NOT NULL DEFAULT '{}'::jsonb,   -- {template, params}
    bonus_rules     JSONB       NOT NULL DEFAULT '[]'::jsonb,   -- [{template, params}]
    penalty_rule    JSONB       NOT NULL DEFAULT '{}'::jsonb,   -- {template, params}
    rank_rule       JSONB       NOT NULL DEFAULT '{}'::jsonb,   -- {tieBreak, awardTiers}
    custom_formula  TEXT,                                        -- v1 固定为 NULL
    config_version  TEXT        NOT NULL DEFAULT 'v1.0',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE  events            IS '赛项：评分规则、奖项规则的整体配置载体';
COMMENT ON COLUMN events.groups     IS '适用组别，如 ["小学组","初中组"]';
COMMENT ON COLUMN events.score_rule IS '计分模板，template ∈ weighted_sum|sum|average';

-- ----------------------------------------------------------------------------
-- 2. 任务项（tasks）—— 计分的最小单元
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tasks (
    event_id    TEXT    NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    id          TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    type        TEXT    NOT NULL CHECK (type IN ('numeric','count','enum')),
    max_score   NUMERIC,
    weight      NUMERIC NOT NULL DEFAULT 0,
    control     TEXT,
    enum_map    JSONB,
    sort_order  INT     NOT NULL DEFAULT 0,
    PRIMARY KEY (event_id, id)
);

COMMENT ON COLUMN tasks.type   IS 'numeric=数值评分 count=计数得分 enum=等级评分';
COMMENT ON COLUMN tasks.weight IS 'numeric 为归一化权重；count 为每单位分';

-- ----------------------------------------------------------------------------
-- 3. 队伍（teams）—— 编号唯一，退赛软删除
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS teams (
    id          BIGSERIAL   PRIMARY KEY,
    event_id    TEXT        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    team_no     TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    school      TEXT        NOT NULL DEFAULT '',
    coach       TEXT        NOT NULL DEFAULT '',
    group_code  TEXT        NOT NULL,
    members     TEXT        NOT NULL DEFAULT '',
    status      TEXT        NOT NULL DEFAULT 'active'   CHECK (status IN ('active','withdrawn')),
    source      TEXT        NOT NULL DEFAULT 'manual'   CHECK (source IN ('excel','manual','api')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 「一号一队」强校验：同一赛项内队伍编号唯一
CREATE UNIQUE INDEX IF NOT EXISTS ux_teams_event_no   ON teams (event_id, team_no);
CREATE INDEX        IF NOT EXISTS ix_teams_event_grp  ON teams (event_id, group_code);
CREATE INDEX        IF NOT EXISTS ix_teams_status     ON teams (status);

COMMENT ON COLUMN teams.status IS 'active=在册 withdrawn=弃赛（软删除，保留历史成绩）';

-- ----------------------------------------------------------------------------
-- 4. 打分记录（scores）—— 两轮制，每轮独立一条
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS scores (
    id           BIGSERIAL   PRIMARY KEY,
    team_id      BIGINT      NOT NULL REFERENCES teams(id) ON DELETE RESTRICT,
    round_no     SMALLINT    NOT NULL CHECK (round_no BETWEEN 1 AND 2),
    task_values  JSONB       NOT NULL DEFAULT '{}'::jsonb,   -- {taskId: 值}
    duration_sec NUMERIC     NOT NULL DEFAULT 0,
    yellow       SMALLINT    NOT NULL DEFAULT 0,
    red          SMALLINT    NOT NULL DEFAULT 0,
    signed       BOOLEAN     NOT NULL DEFAULT false,          -- 选手代表已签字
    operator     TEXT        NOT NULL DEFAULT '',             -- 记分员/裁判
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_scores_team_round ON scores (team_id, round_no);

COMMENT ON TABLE  scores            IS '两轮制成绩明细；最终名次由 engine 取优后计算';
COMMENT ON COLUMN scores.task_values IS '各任务得分，键为 task.id';

-- ----------------------------------------------------------------------------
-- 5. 赛台（seats）—— 赛事级资源，可跨赛项共用
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS seats (
    id         BIGSERIAL   PRIMARY KEY,
    name       TEXT        NOT NULL,
    sort_order INT         NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE seats IS '赛台数量由运营按参赛人数自行配置；同一赛台可跨时段、跨赛项复用';

-- ----------------------------------------------------------------------------
-- 6. 场次（slots）—— 赛台 × 时段 = 一个场次
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS slots (
    id          BIGSERIAL   PRIMARY KEY,
    seat_id     BIGINT      NOT NULL REFERENCES seats(id)  ON DELETE CASCADE,
    period      TEXT        NOT NULL,                       -- 上午 / 下午
    time_range  TEXT        NOT NULL DEFAULT '',            -- 09:00–12:00
    event_id    TEXT        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    group_code  TEXT        NOT NULL,
    slot_type   TEXT        NOT NULL DEFAULT 'normal' CHECK (slot_type IN ('normal','extra')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (seat_id, period)
);

COMMENT ON COLUMN slots.slot_type IS 'normal=正式场次 extra=加时赛（独立场次，仅影响冠亚季军/晋级时启用）';

-- ----------------------------------------------------------------------------
-- 7. 场次队伍（slot_teams）—— 正式场次的队伍来自主库
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS slot_teams (
    slot_id BIGINT NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    team_id BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    PRIMARY KEY (slot_id, team_id)
);

-- ----------------------------------------------------------------------------
-- 8. 加时赛场内快照（slot_snapshots）—— 独立存储，绝不写入主库
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS slot_snapshots (
    id         BIGSERIAL   PRIMARY KEY,
    slot_id    BIGINT      NOT NULL REFERENCES slots(id) ON DELETE CASCADE,
    team_no    TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    school     TEXT        NOT NULL DEFAULT '',
    coach      TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (slot_id, team_no)
);

COMMENT ON TABLE slot_snapshots IS
    '加时赛队伍以导入为准，仅在本场次内生效；归档时独立标记，不污染队伍主库';

-- ----------------------------------------------------------------------------
-- 9. 操作审计日志（audit_logs）—— 六类操作全留痕，保留 ≥2 年
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS audit_logs (
    id         BIGSERIAL   PRIMARY KEY,
    operator   TEXT        NOT NULL DEFAULT '',
    action     TEXT        NOT NULL,   -- 改配置/改分/弃赛/改组/调赛台/删队/导入队伍
    target     TEXT        NOT NULL DEFAULT '',
    before_val TEXT        NOT NULL DEFAULT '',
    after_val  TEXT        NOT NULL DEFAULT '',
    reason     TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ix_audit_created ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS ix_audit_action  ON audit_logs (action);
CREATE INDEX IF NOT EXISTS ix_audit_op      ON audit_logs (operator);

COMMENT ON TABLE audit_logs IS
    '争议追溯依据。每条含操作人/时间/对象/旧值→新值/原因；归档策略见 README（≥2 年）';

-- ----------------------------------------------------------------------------
-- 10. 报名导入日志（import_logs）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS import_logs (
    id         BIGSERIAL   PRIMARY KEY,
    source     TEXT        NOT NULL DEFAULT 'excel',
    operator   TEXT        NOT NULL DEFAULT '',
    summary    JSONB       NOT NULL DEFAULT '{}'::jsonb,   -- {insert,update,skip,conflict}
    detail     JSONB       NOT NULL DEFAULT '[]'::jsonb,   -- [{act,no,changes}]
    status     TEXT        NOT NULL DEFAULT 'success' CHECK (status IN ('success','rolled_back')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ix_import_created ON import_logs (created_at DESC);

-- ----------------------------------------------------------------------------
-- 11. 配置快照（config_snapshots）—— 改配置前留档，可回滚
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS config_snapshots (
    id         BIGSERIAL   PRIMARY KEY,
    note       TEXT        NOT NULL DEFAULT '',
    payload    JSONB       NOT NULL,   -- {schemaVersion, events:[...]}
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ix_snap_created ON config_snapshots (created_at DESC);

-- ----------------------------------------------------------------------------
-- 12. 大屏配置（screen_config）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS screen_config (
    event_id     TEXT        PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    page_size    INT         NOT NULL DEFAULT 10,
    interval_sec INT         NOT NULL DEFAULT 60,
    pinned       TEXT,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN screen_config.page_size IS '每屏条数（已确认：10 条）';

-- ----------------------------------------------------------------------------
-- 13. updated_at 自动维护
-- ----------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['events','teams','scores'] LOOP
        EXECUTE format(
            'DROP TRIGGER IF EXISTS trg_%1$s_touch ON %1$s;
             CREATE TRIGGER trg_%1$s_touch BEFORE UPDATE ON %1$s
             FOR EACH ROW EXECUTE FUNCTION touch_updated_at();', t);
    END LOOP;
END $$;

COMMIT;
