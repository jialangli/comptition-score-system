-- 0011 回滚：评分方式撤掉 toggle。
--
-- ⚠️ 回滚前必须先处理已存在的 toggle 任务行，否则重建 CHECK 会失败。
-- 本脚本按约定「先报错、不静默删数据」：若有 toggle 行存在，ADD CONSTRAINT 会
-- 直接失败（ON_ERROR_STOP=1 下整个迁移中止），由人工决定改回 numeric 还是删除任务。
-- 若确认要清掉，先执行：
--   UPDATE tasks SET type='numeric' WHERE type='toggle';

BEGIN;

DO $$
DECLARE c record;
BEGIN
  FOR c IN
    SELECT conname
      FROM pg_constraint
     WHERE conrelid = 'tasks'::regclass
       AND contype  = 'c'
       AND pg_get_constraintdef(oid) LIKE '%numeric%'
  LOOP
    EXECUTE format('ALTER TABLE tasks DROP CONSTRAINT %I', c.conname);
  END LOOP;
END $$;

ALTER TABLE tasks ADD CONSTRAINT tasks_type_check
    CHECK (type IN ('numeric','count','enum'));

COMMENT ON COLUMN tasks.type IS
    'numeric=数值评分 count=计数得分 enum=等级评分';

COMMIT;
