#!/usr/bin/env bash
# ============================================================================
# 启动 PostgreSQL（若已在运行则直接返回）
#
# 为什么需要这个脚本：此前启动库只能手敲一长串 pg_ctl 命令，
# 路径还因机器而异（PG_DIR）。这里把路径与参数收在一处。
#
# 用法：
#   bash scripts/pg_start.sh
#   PG_DIR=/path/to/pgsql bash scripts/pg_start.sh
#
# 端口默认 15432（**不是** 5432）：本机沙箱会拦 127.0.0.1:5432 的库连接，
# 起在 5432 谁也连不上。详见 scripts/env.sh 里的同名注释。
# 确需换端口：PG_PORT=5432 bash scripts/pg_start.sh
#
# 幂等：已在跑时不会重复启动，也不会报错。
# ============================================================================
set -euo pipefail

PG_DIR="${PG_DIR:-D:/Desktop/workbuddy/pgsql}"
HOST="${PG_HOST:-127.0.0.1}"
PORT="${PG_PORT:-15432}"
PGDATA="${PGDATA:-$(dirname "$PG_DIR")/pgdata}"

if [ ! -f "$PG_DIR/bin/pg_ctl.exe" ]; then
  echo "找不到 pg_ctl：$PG_DIR/bin/pg_ctl.exe"
  echo "请通过 PG_DIR 指定 PostgreSQL 安装目录。"
  exit 1
fi

# 已在运行则直接返回（-C 只看状态，不启动）
if "$PG_DIR/bin/pg_ctl.exe" -D "$PGDATA" status >/dev/null 2>&1; then
  echo "==> PostgreSQL 已在运行（$PGDATA，端口 $PORT）"
  exit 0
fi

echo "==> 启动 PostgreSQL（$PGDATA，端口 $PORT）"

# 上一次非正常退出（强杀 / 断电 / 杀软拦截）会留下 postmaster.pid，
# 而服务实际已不在跑 —— 此时直接 start 会报 "could not start server"。
# 先做一次就绪探测，确认真的没在跑才移走残留并重试，避免误动运行中的 pid 文件。
#
# 用 mv 而不是 rm：pid 文件是运行态的锁，移走即可让 PG 重新创建；
# 保留 .stale 后缀便于事后排查，也避免误删。
if [ -f "$PGDATA/postmaster.pid" ] && ! "$PG_DIR/bin/pg_isready.exe" -h "$HOST" -p "$PORT" -q 2>/dev/null; then
  echo "==> 发现上次异常退出留下的 postmaster.pid，先移走后重试"
  mv -f "$PGDATA/postmaster.pid" "$PGDATA/postmaster.pid.stale" 2>/dev/null || true
fi

if "$PG_DIR/bin/pg_ctl.exe" -D "$PGDATA" -l "$PGDATA/pg.log" -o "-p $PORT" -w start; then
  echo "==> 启动完成，日志：$PGDATA/pg.log"
  exit 0
fi

echo "==> 启动失败，日志尾部："
tail -n 10 "$PGDATA/pg.log" 2>/dev/null || true
exit 1
