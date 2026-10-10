-- 0015 down：回滚 slot_type 取值域（先把 rematch 行改回 extra，否则约束加不回去）。
UPDATE slots SET slot_type = 'extra' WHERE slot_type = 'rematch';

ALTER TABLE slots DROP CONSTRAINT IF EXISTS slots_slot_type_check;
ALTER TABLE slots ADD CONSTRAINT slots_slot_type_check
    CHECK (slot_type IN ('normal', 'extra'));

COMMENT ON COLUMN slots.slot_type IS
    'normal=正式场次 extra=加时赛（独立场次，仅影响冠亚季军/晋级时启用）';
