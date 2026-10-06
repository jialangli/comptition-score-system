-- ============================================================================
-- 0009：留底证据库（evidence）
--
-- 背景：前端多处引用「证据层 / 留底三件」，后端此前没有专门的表。
-- 规格来自 wireframe：
--
--   - **证据三件**：成绩表 / 签名图 / 提交留底截图（P2e note）
--   - **产生端各不相同**（P2e note 7）：
--       裁判提交成绩时 → 成绩 + 签名 + 提交留底
--       裁判长提交裁定时 → 裁定层（P8）
--       工作人员发布时 → P11 发布产物
--     「规范统一、触发点各异」，所以 source 必须能区分谁产生的。
--   - **状态一律以「是否已上云」为准**：本地先落、异步同步。
--   - **作废不抹除证据**：成绩作废只改「是否计入排名」，证据仍完整留存。
--
-- 设计要点：
--
--   1. **只存元数据，不存二进制本体**。
--      图片本体属于对象存储 / 文件服务的职责，塞进 Postgres 会让库体积
--      随赛事线性膨胀、备份变慢。本表回答的是合规追溯真正要问的问题：
--      「这一轮的证据三件齐不齐、谁产生的、上云了没有」。
--      storage_url 留给后续接入对象存储时填写，当前可为空。
--
--   2. **同一（队伍, 轮次, 类型）只留一条**：UNIQUE 约束落在业务键上。
--      补传会重试，不去重会让「三件」变成「七件」，反而没法判断齐不齐。
--
--   3. **作废 / 冲突不删证据**：本表与 scores、disputes 之间没有级联删除。
--      成绩作废改的是「是否计入排名」，不是「证据是否留存」。
--
--   4. round_no 可空：发布类与部分裁定类证据不挂在具体轮次上。
--
-- 影响面：纯新增表，不改任何既有表结构，历史数据无需回填。
-- ============================================================================

BEGIN;

CREATE TABLE IF NOT EXISTS evidence (
    id          BIGSERIAL   PRIMARY KEY,
    contest_id  TEXT        NOT NULL DEFAULT 'ct_default'
                            REFERENCES contests(id) ON DELETE RESTRICT,
    team_id     BIGINT      NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    round_no    SMALLINT    CHECK (round_no IS NULL OR round_no BETWEEN 1 AND 2),
    -- 证据类型：成绩表 / 签名图 / 提交留底截图 / 裁定单 / 发布产物
    kind        TEXT        NOT NULL CHECK (kind IN
                    ('score_sheet', 'signature', 'submit_snapshot', 'decision', 'release')),
    -- 产生端：裁判提交成绩 / 裁判长提交裁定 / 工作人员发布
    source      TEXT        NOT NULL CHECK (source IN
                    ('referee_submit', 'chief_decide', 'staff_publish')),
    file_name   TEXT        NOT NULL,   -- 如 T-001_R1_1240.jpg
    -- 状态一律以「是否已上云」为准：local 本地已生成待同步 / synced 已上云
    status      TEXT        NOT NULL DEFAULT 'local' CHECK (status IN ('local', 'synced')),
    operator    TEXT        NOT NULL DEFAULT '',
    seat_id     BIGINT,
    dispute_id  BIGINT,                 -- 裁定类证据关联的争议工单（可空）
    -- 本体存放位置。本期后端不存二进制，接入对象存储后填写
    storage_url TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    synced_at   TIMESTAMPTZ
);

-- 同一（队伍, 轮次, 类型）只留一条：补传重试不去重会让「三件」变「七件」
CREATE UNIQUE INDEX IF NOT EXISTS ux_evidence_item
    ON evidence (contest_id, team_id, COALESCE(round_no, 0), kind, file_name);

-- 按队伍查证据（成绩单页 / P8 裁定台都按队伍取）
CREATE INDEX IF NOT EXISTS ix_evidence_team
    ON evidence (contest_id, team_id, round_no);

-- 待上云队列：断网时先落本地，联网后补传
CREATE INDEX IF NOT EXISTS ix_evidence_pending
    ON evidence (contest_id, created_at)
    WHERE status = 'local';

COMMENT ON TABLE  evidence               IS '留底证据库：证据三件等留底的元数据与上云状态（本体不入库）';
COMMENT ON COLUMN evidence.kind          IS 'score_sheet 成绩表 / signature 签名图 / submit_snapshot 提交留底截图 / decision 裁定单 / release 发布产物';
COMMENT ON COLUMN evidence.source        IS '产生端：referee_submit 裁判提交 / chief_decide 裁判长裁定 / staff_publish 工作人员发布';
COMMENT ON COLUMN evidence.status        IS 'local 本地已生成待同步 / synced 已上云。状态一律以是否已上云为准';
COMMENT ON COLUMN evidence.storage_url   IS '本体存放位置；本期后端不存二进制，接入对象存储后填写';
COMMENT ON COLUMN evidence.round_no      IS '可空：发布类与部分裁定类证据不挂在具体轮次上';

COMMIT;
