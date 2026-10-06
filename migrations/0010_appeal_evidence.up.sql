-- ============================================================================
-- 0010：申述书照片（appeal）落 evidence + 改分单关联
--
-- 背景：用户确认「选手现场手写申述书，由裁判拍照，上传给后台运营」。
-- 此前 disputes / score_change_requests 均无照片字段，裁定与留底看不到原件。
--
-- 设计要点：
--
--   1. **复用 evidence 表**：申述书照片本质是「有来源、有状态」的证据
--      （与 P9 note 8 口径一致），故作为 evidence 的新类目 appeal，
--      而非另起一张表。source=referee_submit（裁判拍照上传），
--      dispute_id 指向所属争议工单，storage_url 存服务端相对路径。
--
--   2. **两处都挂**：同一张 appeal 证据行，既经 dispute_id 挂在争议工单，
--      又经 score_change_requests.appeal_evidence_id 引用挂到改分申请单
--      （P8b 授权改分生成改分单时从争议单继承同一 evidence.id）。
--
--   3. **改分单生成是既有独立缺口**：DecideDispute(adjust) 目前只记裁定结论、
--      不建改分单。本迁移仅为该链路预留 appeal_evidence_id 列与继承钩子，
--      待 P8b 改分单生成落地后自动生效。
--
-- 影响面：扩 evidence.kind 取值 + 给 score_change_requests 加可空引用列。
-- ============================================================================

BEGIN;

-- 1) evidence.kind 增加 'appeal'（申述书照片）。
--    列级 CHECK 约束由 Postgres 自动命名为 evidence_kind_check，drop 后重建。
ALTER TABLE evidence DROP CONSTRAINT evidence_kind_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_kind_check
    CHECK (kind IN ('score_sheet', 'signature', 'submit_snapshot',
                    'decision', 'release', 'appeal'));

COMMENT ON COLUMN evidence.kind IS
    'score_sheet 成绩表 / signature 签名图 / submit_snapshot 提交留底截图 / decision 裁定单 / release 发布产物 / appeal 申述书照片（选手手写·裁判拍照）';

-- 1b) evidence.source 增加 'referee_appeal'（裁判拍照上传申述书）。
--     区别于 referee_submit（裁判提交成绩），口径上申述书是「裁判拍照上传」而非「提交成绩」。
ALTER TABLE evidence DROP CONSTRAINT evidence_source_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_source_check
    CHECK (source IN ('referee_submit', 'chief_decide', 'staff_publish', 'referee_appeal'));

COMMENT ON COLUMN evidence.source IS
    '产生端：referee_submit 裁判提交成绩 / chief_decide 裁判长裁定 / staff_publish 工作人员发布 / referee_appeal 裁判拍照上传申述书';

-- 2) 改分申请单关联申述书照片（可空，删证据时一并置空）。
ALTER TABLE score_change_requests
    ADD COLUMN appeal_evidence_id BIGINT REFERENCES evidence(id) ON DELETE SET NULL;

COMMENT ON COLUMN score_change_requests.appeal_evidence_id IS
    '关联的申述书照片证据（evidence.id）；P8b 授权改分生成改分单时从争议单继承同一张，实现「两处都挂」';

COMMIT;
