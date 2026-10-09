#!/usr/bin/env bash
# ============================================================================
# 开发环境变量（仅对当前 shell 生效，不修改系统环境变量）
# 用法：source scripts/env.sh && go build ./...
# ============================================================================

# ---- Go 工具链（便携版解压在用户态目录）----
export GOROOT="C:/Users/ljl20/.workbuddy/binaries/go/versions/go"
export PATH="/c/Users/ljl20/.workbuddy/binaries/go/versions/go/bin:$PATH"

# ---- Go 工作区与模块缓存（避开 C:\Program Files 的写权限问题）----
export GOPATH="C:/Users/ljl20/.workbuddy/binaries/go/gopath"
export GOMODCACHE="$GOPATH/pkg/mod"
export GOBIN=""

# ---- 模块代理（国内加速）----
export GOPROXY="https://goproxy.cn,direct"
export GOSUMDB="sum.golang.org"

# ---- 本地 PostgreSQL（便携版）----
export PGHOME="/d/Desktop/workbuddy/pgsql"
export PGDATA="/d/Desktop/workbuddy/pgdata"

# ---- 数据库端口：默认 15432，不是 5432 ----
#
# ⚠️ 别改回 5432。本机沙箱会拦 127.0.0.1:5432 的**连接**：
#    psql --version 正常，但一连库进程就被直接杀掉；curl/程序侧表现为超时（rc=28），
#    而 5432 以外的端口一切正常（连不上是 rc=7）。所以把 PG 起在 15432 绕开它。
#
# 这是**一处可逆覆盖**：只改端口参数，没有动 PostgreSQL 的配置文件（postgresql.conf）。
# 想回到 5432，`unset PG_PORT` 后按老方式启动即可。
#
# 口径统一：本变量同时喂给
#   · SCORE_DB_URL        —— 后端服务连的库
#   · scripts/pg_start.sh —— 起库用哪个端口（它自己也会兜底取 PG_PORT）
#   · scripts/test_db.sh  —— 重建两个测试库
#   · 集成测试            —— TEST_DATABASE_URL 里的端口（见下面注释）
export PG_HOST="127.0.0.1"
export PG_PORT="15432"
export PG_USER="postgres"

# ---- 本服务配置 ----
export SCORE_ADDR=":8080"
export SCORE_DB_URL="postgres://${PG_USER}@${PG_HOST}:${PG_PORT}/neuroscore?sslmode=disable"
export SCORE_DEV="true"
export SCORE_STATIC_DIR="web"

# ---- 集成测试库（可选：不想每次手敲就取消下面两行注释）----
# 与业务库分开，测试会 TRUNCATE；两个库对应 service / api 两个测试包，避免并行互踩。
# export TEST_DATABASE_URL="postgres://${PG_USER}@${PG_HOST}:${PG_PORT}/neuroscore_test?sslmode=disable"
# export TEST_DATABASE_URL_API="postgres://${PG_USER}@${PG_HOST}:${PG_PORT}/neuroscore_test_api?sslmode=disable"
