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

# ---- 本服务配置 ----
export SCORE_ADDR=":8080"
export SCORE_DB_URL="postgres://postgres@127.0.0.1:5432/neuroscore?sslmode=disable"
export SCORE_DEV="true"
export SCORE_STATIC_DIR="web"

# ---- 本地 PostgreSQL（便携版）----
export PGHOME="/d/Desktop/workbuddy/pgsql"
export PGDATA="/d/Desktop/workbuddy/pgdata"
