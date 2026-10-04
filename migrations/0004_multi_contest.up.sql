-- ============================================================================
-- 0004：多赛事维度（contest_id）
--
-- 背景：前端 demo 的核心模型是「一场赛事一份独立数据空间」—— 每场赛事有自己
-- 的队伍、成绩、工单、留底、审计、裁判码，互不串场。而后端 13 张表**全部没有
-- contest_id**，等价于「整个库只服务一场赛事」。
--
-- 这个 gap 导致一个具体问题：跨赛事汇总页在后端无法表达。前端能算出「追光队
-- 在 3 场赛事里各拿了第几名」，是因为它按赛事分别读数据空间再拼；后端没有赛事
-- 维度，同一张 teams 表里两场赛事的同名队伍会直接撞车。
--
-- 设计要点：
--
--   1. **用 DEFAULT 保证可回滚 + 零停机**：contest_id 一律 `NOT NULL DEFAULT
--      'ct_default'` backfill，存量行自动落到默认赛事。这比「先加可空列 → 回填
--      → 再改 NOT NULL」三步走更安全：任何一步失败都能直接 ROLLBACK，且不会
--      出现「列已加但回填未完成」的中间态。
--
--   2. **先建 contests 表，其余表外键引用它**：避免出现指向不存在赛事的孤儿
--      数据。ON DELETE RESTRICT —— 删赛事必须先把该赛事数据清干净，不能靠级联
--      静默删掉一整场赛事的成绩。
--
--   3. **唯一约束要「带上赛事维度」重做**，这是本迁移真正的重点：
--        原来的 UNIQUE(seat_id, period) 意味着「赛台 1 上午只能有一场」——
--        在多赛事下这是错的，A 赛事和 B 赛事各有一场完全正常。
--        改为 UNIQUE(contest_id, seat_id, period)。
--      涉及三处：slots、slot_snapshots，以及 teams 的编号唯一性（队伍编号**只在
--      赛事内唯一**，跨赛事可重复 —— 这是前端已实现的口径，后端要对齐）。
--
--   4. **screen_config 的主键是 event_id**（单行配置表），加 contest_id 后需改为
--      复合主键，否则两场赛事会互相覆盖大屏配置。
--
-- 影响面：13 张表全部加列；3 处唯一约束重建；1 处主键调整。存量数据全部归入
-- 默认赛事 ct_default，语义等价于「迁移前的单赛事状态」，不丢数据。
-- ============================================================================

BEGIN;

-- ----------------------------------------------------------------------------
-- 0. 赛事表：其余表的 contest_id 都引用它
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS contests (
    id           TEXT        PRIMARY KEY,
    name         TEXT        NOT NULL,
    season       TEXT        NOT NULL DEFAULT '',
    start_date   DATE,
    end_date     DATE,
    venue        TEXT        NOT NULL DEFAULT '',
    host         TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL DEFAULT 'prep',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at  TIMESTAMPTZ
);

COMMENT ON TABLE contests IS
    '一场赛事一条记录。前端的「一场赛事一份数据空间」在后端落地为各表的 contest_id 分区。';

-- 默认赛事：承载迁移前的存量数据，保证 0004 可平滑上线
INSERT INTO contests (id, name, status)
VALUES ('ct_default', '默认赛事（迁移前存量数据）', 'prep')
ON CONFLICT (id) DO NOTHING;

-- ----------------------------------------------------------------------------
-- 1. 逐表加 contest_id（NOT NULL + DEFAULT，存量自动归入 ct_default）
-- ----------------------------------------------------------------------------
ALTER TABLE events                 ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE tasks                  ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE teams                  ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE scores                 ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE score_change_requests  ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE seats                  ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE slots                  ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE slot_teams             ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE slot_snapshots         ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE screen_config          ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE config_snapshots       ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE import_logs            ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';
ALTER TABLE audit_logs             ADD COLUMN IF NOT EXISTS contest_id TEXT NOT NULL DEFAULT 'ct_default';

-- ----------------------------------------------------------------------------
-- 2. 外键：RESTRICT —— 不允许删掉还挂着数据的赛事
-- ----------------------------------------------------------------------------
ALTER TABLE events                ADD CONSTRAINT fk_events_contest                FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE tasks                 ADD CONSTRAINT fk_tasks_contest                 FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE teams                 ADD CONSTRAINT fk_teams_contest                 FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE scores                ADD CONSTRAINT fk_scores_contest                FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE score_change_requests ADD CONSTRAINT fk_score_change_requests_contest FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE seats                 ADD CONSTRAINT fk_seats_contest                 FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE slots                 ADD CONSTRAINT fk_slots_contest                 FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE slot_teams            ADD CONSTRAINT fk_slot_teams_contest            FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE slot_snapshots        ADD CONSTRAINT fk_slot_snapshots_contest        FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE screen_config         ADD CONSTRAINT fk_screen_config_contest         FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE config_snapshots      ADD CONSTRAINT fk_config_snapshots_contest      FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE import_logs           ADD CONSTRAINT fk_import_logs_contest           FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;
ALTER TABLE audit_logs            ADD CONSTRAINT fk_audit_logs_contest            FOREIGN KEY (contest_id) REFERENCES contests(id) ON DELETE RESTRICT;

-- ----------------------------------------------------------------------------
-- 3. 唯一约束带上赛事维度 —— 本迁移真正的语义改动
--
--    队伍编号**只在赛事内唯一**：同一编号在不同赛事里可以重复（前端已按此实现，
--    跨赛事汇总榜靠「队名 + 学校」近似归并）。
-- ----------------------------------------------------------------------------

-- ⚠️ 唯一约束必须用 DROP CONSTRAINT 删，不能用 DROP INDEX。
--    UNIQUE 在 PostgreSQL 里会同时建一个约束和它依赖的索引；直接 DROP INDEX
--    会报「无法删除索引 xxx，因为表 xxx 上的约束 xxx 需要它」。
--    本次就是先写成了 DROP INDEX 才跑失败的 —— 迁移必须真跑，不能只看语法。
ALTER TABLE teams          DROP CONSTRAINT IF EXISTS teams_team_no_key;
ALTER TABLE slots          DROP CONSTRAINT IF EXISTS slots_seat_id_period_key;
ALTER TABLE slot_snapshots DROP CONSTRAINT IF EXISTS slot_snapshots_slot_id_team_no_key;

-- teams: 队伍编号**只在赛事内唯一**，跨赛事可重复（与前端口径一致）
CREATE UNIQUE INDEX IF NOT EXISTS uq_teams_contest_no
    ON teams (contest_id, team_no);

-- slots: 一赛台一时段在一场赛事内只有一场
CREATE UNIQUE INDEX IF NOT EXISTS uq_slots_contest_seat_period
    ON slots (contest_id, seat_id, period);

-- slot_snapshots: 同一场次的同一编号只留一份
CREATE UNIQUE INDEX IF NOT EXISTS uq_slot_snapshots_contest_slot_no
    ON slot_snapshots (contest_id, slot_id, team_no);

-- 0001~0003 里还有三个唯一索引没带赛事维度，一并重做。
-- 它们都在「跨赛事同编号」场景下会误报重复：
--   ux_teams_event_no   UNIQUE(event_id, team_no)          → 两场赛事同编号撞车
--   ux_scores_team_round UNIQUE(team_id, round_no)          → 两场赛事同一队号撞车
--   ux_scr_one_pending  UNIQUE(team_id, round_no) WHERE NOT approved → 同上
DROP INDEX IF EXISTS ux_teams_event_no;
CREATE UNIQUE INDEX IF NOT EXISTS uq_teams_contest_event_no
    ON teams (contest_id, event_id, team_no);

DROP INDEX IF EXISTS ux_scores_team_round;
CREATE UNIQUE INDEX IF NOT EXISTS ux_scores_contest_team_round
    ON scores (contest_id, team_id, round_no);

DROP INDEX IF EXISTS ux_scr_one_pending;
CREATE UNIQUE INDEX IF NOT EXISTS ux_scr_contest_one_pending
    ON score_change_requests (contest_id, team_id, round_no) WHERE (NOT approved);

-- ----------------------------------------------------------------------------
-- 4. screen_config 主键改为 (contest_id, event_id)
--    原本主键是 event_id —— 单行配置表，两场赛事会互相覆盖大屏配置。
-- ----------------------------------------------------------------------------
ALTER TABLE screen_config DROP CONSTRAINT IF EXISTS screen_config_pkey;
ALTER TABLE screen_config ADD PRIMARY KEY (contest_id, event_id);

-- ----------------------------------------------------------------------------
-- 4.5 events / tasks 的主键也要带 contest_id
--
--    这是本迁移里最容易漏、也最致命的一处：
--    events 原主键是 (id)，而前端三场赛事用的赛项 id 完全相同
--    （brain_planet / mars_rescue / future_city）。若主键不带赛事维度，
--    第二场赛事创建同名赛项时会直接撞主键 —— 等于「一场库只能办一场赛事」，
--    多赛事维度形同虚设。
--
--    改主键要连带重建引用它的外键（tasks / teams / slots / screen_config）。
--    做法：先全部 DROP，再按新的列组合重新 ADD。
--    ⚠️ 必须先删后建，否则旧的 FOREIGN KEY(event_id) REFERENCES events(id)
--    会因为 events(id) 不再是唯一约束而报「没有匹配的唯一约束」。
-- ----------------------------------------------------------------------------

-- 先解除依赖 events 的外键
ALTER TABLE tasks         DROP CONSTRAINT IF EXISTS tasks_event_id_fkey;
ALTER TABLE teams         DROP CONSTRAINT IF EXISTS teams_event_id_fkey;
ALTER TABLE slots         DROP CONSTRAINT IF EXISTS slots_event_id_fkey;
ALTER TABLE screen_config DROP CONSTRAINT IF EXISTS screen_config_event_id_fkey;

-- events: (id) → (contest_id, id)
ALTER TABLE events DROP CONSTRAINT IF EXISTS events_pkey;
ALTER TABLE events ADD PRIMARY KEY (contest_id, id);

-- tasks: (event_id, id) → (contest_id, event_id, id)
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_pkey;
ALTER TABLE tasks ADD PRIMARY KEY (contest_id, event_id, id);

-- 重建外键（带上 contest_id，避免跨赛事误引用）
ALTER TABLE tasks         ADD CONSTRAINT tasks_event_id_fkey         FOREIGN KEY (contest_id, event_id) REFERENCES events (contest_id, id) ON DELETE CASCADE;
ALTER TABLE teams         ADD CONSTRAINT teams_event_id_fkey         FOREIGN KEY (contest_id, event_id) REFERENCES events (contest_id, id) ON DELETE RESTRICT;
ALTER TABLE slots         ADD CONSTRAINT slots_event_id_fkey         FOREIGN KEY (contest_id, event_id) REFERENCES events (contest_id, id) ON DELETE RESTRICT;
ALTER TABLE screen_config ADD CONSTRAINT screen_config_event_id_fkey FOREIGN KEY (contest_id, event_id) REFERENCES events (contest_id, id) ON DELETE CASCADE;

-- ----------------------------------------------------------------------------
-- 5. 查询热路径索引：几乎所有读操作都带 contest_id 过滤
-- ----------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_events_contest          ON events (contest_id);
CREATE INDEX IF NOT EXISTS idx_tasks_contest           ON tasks (contest_id);
CREATE INDEX IF NOT EXISTS idx_teams_contest           ON teams (contest_id);
CREATE INDEX IF NOT EXISTS idx_scores_contest          ON scores (contest_id);
CREATE INDEX IF NOT EXISTS idx_scores_contest_round    ON scores (contest_id, team_id, round_no);
CREATE INDEX IF NOT EXISTS idx_scr_contest             ON score_change_requests (contest_id);
CREATE INDEX IF NOT EXISTS idx_seats_contest           ON seats (contest_id);
CREATE INDEX IF NOT EXISTS idx_slots_contest           ON slots (contest_id);
CREATE INDEX IF NOT EXISTS idx_slot_teams_contest      ON slot_teams (contest_id);
CREATE INDEX IF NOT EXISTS idx_slot_snapshots_contest  ON slot_snapshots (contest_id);
CREATE INDEX IF NOT EXISTS idx_config_snapshots_contest ON config_snapshots (contest_id);
CREATE INDEX IF NOT EXISTS idx_import_logs_contest     ON import_logs (contest_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_contest      ON audit_logs (contest_id);

COMMIT;
