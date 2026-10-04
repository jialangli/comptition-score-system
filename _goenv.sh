#!/usr/bin/env bash
# Go 构建环境包装器
#
# 解决本机三个环境问题：
#
#  1. GOROOT 必须显式传给子进程，否则报 "package runtime is not in std"。
#  2. Bash 沙箱注入 HTTP(S)_PROXY=127.0.0.1:18211，go mod download 会被拒 → unset 掉。
#  3. GOFLAGS 里加 -buildvcs=false：.git/refs 若缺失，go build 的 VCS 打标会失败。
#     （2026-10-04 本机 .git 两次损坏，refs 目录消失；已从远端重新克隆修复）
#
# 注意：WorkBuddy 其实早已托管一份 Go（见 scripts/env.sh，go1.25.0），
# 本脚本用的是 D:\go 那份自解压副本 —— 两者都可用，依赖缓存各自独立。
#
# 用法：
#   source _goenv.sh && go build ./...
#   ./_goenv.sh build ./...
#   ./_goenv.sh test -p 1 ./...      # 串行跑，避开 service 包内测试共用一库的竞争
set -u

export GOROOT='D:\go\go'
export GOPATH='D:\gopath'
export GOMODCACHE='D:\gopath\pkg\mod'
export GOCACHE='D:\gopath\go-build'
export GOFLAGS='-mod=mod -buildvcs=false'
export GOPROXY='https://goproxy.cn,direct'

# 关掉沙箱代理；Go 直连 goproxy.cn
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
export PATH="/d/go/go/bin:$PATH"

mkdir -p /d/gopath/pkg/mod /d/gopath/go-build 2>/dev/null || true

if [ "${1:-}" = "source" ]; then
  return 0 2>/dev/null || exit 0
fi
exec go "$@"
