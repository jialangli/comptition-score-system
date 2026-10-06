-- 0010 回滚：撤掉 appeal 类目与改分单关联列。
BEGIN;

ALTER TABLE score_change_requests DROP COLUMN IF EXISTS appeal_evidence_id;

ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_kind_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_kind_check
    CHECK (kind IN ('score_sheet', 'signature', 'submit_snapshot',
                    'decision', 'release'));

COMMENT ON COLUMN evidence.kind IS
    'score_sheet 成绩表 / signature 签名图 / submit_snapshot 提交留底截图 / decision 裁定单 / release 发布产物';

ALTER TABLE evidence DROP CONSTRAINT IF EXISTS evidence_source_check;
ALTER TABLE evidence ADD CONSTRAINT evidence_source_check
    CHECK (source IN ('referee_submit', 'chief_decide', 'staff_publish'));

COMMENT ON COLUMN evidence.source IS
    '产生端：referee_submit 裁判提交成绩 / chief_decide 裁判长裁定 / staff_publish 工作人员发布';

COMMIT;
