#!/usr/bin/env bash
# ============================================================================
# 准备集成测试库
#
# 刻意与业务库 neuroscore 分开：集成测试会 TRUNCATE 全部表，
# 如果指向业务库，跑一次测试就把演示数据清空了。
#
# 用法：
#   bash scripts/test_db.sh          # 重建并跑迁移
#   PG_DIR=/path/to/pgsql bash scripts/test_db.sh
#
# 测试侧通过 TEST_DATABASE_URL 连接；未设置时使用默认值（见 integration_test.go）。
# ============================================================================
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$HERE")"
PG_DIR="${PG_DIR:-D:/Desktop/workbuddy/pgsql}"
HOST="${PG_HOST:-127.0.0.1}"
PORT="${PG_PORT:-5432}"
USER="${PG_USER:-postgres}"

# 两个库，不是一个。
#
# 原因：`go test ./...` 默认**并行**跑不同包，而 service 与 api 两个测试包
# 都会在用例开始前 TRUNCATE 全表。共用一个库时它们会互相踩踏 ——
# 症状是「单独跑能过、全量跑就挂」，且报错五花八门（编号重复、资源不存在……），
# 属于最难排查的一类问题。物理分开是最省心的解法。
DBS=("${TEST_DB:-neuroscore_test}" "${TEST_DB_API:-neuroscore_test_api}")

if [ ! -x "$PG_DIR/bin/psql" ]; then
  echo "找不到 psql：$PG_DIR/bin/psql"
  echo "请通过 PG_DIR 指定 PostgreSQL 安装目录。"
  exit 1
fi

# 注意：psql 是原生 Windows 程序，认不出 Git Bash 的 /d/... 路径。
# 先切到项目根目录再传相对路径，是跨 shell 最稳的写法。
cd "$ROOT"

for DB in "${DBS[@]}"; do
  echo "==> 重建测试库 $DB"
  "$PG_DIR/bin/dropdb" -h "$HOST" -p "$PORT" -U "$USER" --if-exists "$DB"
  "$PG_DIR/bin/createdb" -h "$HOST" -p "$PORT" -U "$USER" "$DB"

  echo "==> 按序执行迁移"
  for f in migrations/*.up.sql; do
    echo "    - $f"
    "$PG_DIR/bin/psql" -h "$HOST" -p "$PORT" -U "$USER" -d "$DB" \
      -v ON_ERROR_STOP=1 -q -f "$f"
  done

  TABLES=$("$PG_DIR/bin/psql" -h "$HOST" -p "$PORT" -U "$USER" -d "$DB" -tAc \
    "select count(*) from information_schema.tables where table_schema='public'")
  echo "==> $DB 就绪（$TABLES 张表）"
done
