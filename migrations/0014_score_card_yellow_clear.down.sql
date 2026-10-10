-- 0014 down：只回滚列。归一不可逆（原累计黄牌数已被折算成红牌数），
-- 如需还原请从操作审计重建，或用备份库。
ALTER TABLE scores DROP COLUMN IF EXISTS upgraded_red;
