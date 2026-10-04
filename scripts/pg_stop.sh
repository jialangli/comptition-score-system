#!/usr/bin/env bash
# ============================================================================
# 停止 PostgreSQL
#
# 用法：
#   bash scripts/pg_stop.sh
#   PG_DIR=/path/to/pgsql bash scripts/pg_stop.sh
#
# 幂等：未在运行时直接返回，不报错。
# 默认 fast 模式（中断活动事务后正常停止）；确需立即中止传 --immediate。
# ============================================================================
set -euo pipefail

PG_DIR="${PG_DIR:-D:/Desktop/workbuddy/pgsql}"
PGDATA="${PGDATA:-$(dirname "$PG_DIR")/pgdata}"
MODE="${1:-fast}"

if [ ! -f "$PG_DIR/bin/pg_ctl.exe" ]; then
  echo "找不到 pg_ctl：$PG_DIR/bin/pg_ctl.exe"
  echo "请通过 PG_DIR 指定 PostgreSQL 安装目录。"
  exit 1
fi

if ! "$PG_DIR/bin/pg_ctl.exe" -D "$PGDATA" status >/dev/null 2>&1; then
  echo "==> PostgreSQL 未在运行，无需停止"
  exit 0
fi

echo "==> 停止 PostgreSQL（$PGDATA，模式 $MODE）"
"$PG_DIR/bin/pg_ctl.exe" -D "$PGDATA" -m "$MODE" -w stop
echo "==> 已停止"
