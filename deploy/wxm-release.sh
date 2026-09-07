#!/usr/bin/env bash
# WindHub 生产发布脚本（wxm-tenant-platform-hz-01）
#
# 用法:
#   deploy/wxm-release.sh <slug> [commit-ish]
#   deploy/wxm-release.sh test [commit-ish]   # 只跑 gofmt + vet + 单测，不发布
#   deploy/wxm-release.sh rollback            # 回滚到上一个镜像
#   deploy/wxm-release.sh status              # 只看当前状态
#
# 环境变量 
#   WXM_SKIP_TESTS=1   发布前不跑测试（不推荐；这是个计费服务）
#
# 例:
#   deploy/wxm-release.sh video-refund              # 发布 HEAD
#   deploy/wxm-release.sh image-fix 275649c1a       # 发布指定提交
#
# 这套流程是 2026-09-07 多次手工发布中踩出来的，每一步都对应一个真实的坑：
#
#   1. 源码用 `git archive | ssh tar x` 推送，不在服务器上 git clone
#      —— 服务器访问 GitHub 大文件传输会被 TLS 中断（ls-remote 能通、clone 不通）。
#   2. 传输与构建必须分成两条 ssh 命令
#      —— 把 `nohup docker build &` 和 tar 放同一条命令里会破坏 stdin，
#         tar 报 "This does not look like a tar archive"。
#   3. 用 deploy/Dockerfile 而非根 Dockerfile
#      —— 根 Dockerfile 需要 BuildKit（syntax 指令、--mount=type=cache、
#         --platform=$BUILDPLATFORM），而生产机没装 buildx。
#   4. 基础镜像必须走 docker.m.daocloud.io
#      —— 生产机连不上 Docker Hub（registry-1.docker.io 超时）。
#   5. 构建在服务器后台跑并写日志
#      —— 前端 + Go 编译约 8~12 分钟，超过多数 ssh 会话的耐心。
#   6. 换镜像前先备份 compose，失败或回滚时原样还原。
set -euo pipefail

HOST="${WXM_HOST:-wxm-tenant-platform-hz-01}"
STACK="${WXM_STACK:-/srv/wxm/platform/shared/sub2api}"
VERSION="${WXM_VERSION:-0.1.165}"
PREFIX="${WXM_TAG_PREFIX:-sub2api:${VERSION}-wxm2}"
MIRROR="docker.m.daocloud.io/library"
GO_IMAGE="${WXM_GO_IMAGE:-$MIRROR/golang:1.26.5-alpine}"
# Go 模块与构建缓存挂持久卷。不挂的话每次都是冷容器，实测一轮 10m53s，其中绝大
# 部分是重下模块；挂上后只剩真实的编译与跑测时间。
GO_CACHE_MOUNTS="-v s2a-gomodcache:/go/pkg/mod -v s2a-gobuildcache:/root/.cache/go-build"

ssh_do() { ssh -o ConnectTimeout=20 -o BatchMode=yes "$HOST" "$@"; }

current_image() { ssh_do "grep -m1 -oE 'sub2api:[^[:space:]]+' $STACK/docker-compose.yml"; }

health_wait() {
  ssh_do "for i in \$(seq 1 18); do sleep 10;
    S=\$(docker inspect -f '{{.State.Status}}/{{if .State.Health}}{{.State.Health.Status}}{{else}}-{{end}}' sub2api 2>/dev/null);
    case \"\$S\" in */healthy) echo \"healthy @ \$((i*10))s\"; exit 0;; esac; done; echo 'TIMEOUT'; exit 1"
}

# run_tests 在服务器的 golang 容器里跑校验。
#
# 为什么在服务器上跑：开发机（Windows）没装 Go 工具链，而生产机已经有构建用的
# golang 镜像和暖的模块缓存。测的是 `git archive <ref>` 出来的提交快照，与随后
# 构建镜像用的是同一份源码，不会测 A 发 B。
run_tests() {
  local ref="$1" sha work
  sha=$(git rev-parse --short=9 "$ref")
  work="/tmp/s2a-test-${sha}"

  echo "== 校验 $sha =="
  ssh_do "rm -rf $work && mkdir -p $work"
  git archive "$sha" --format=tar | ssh_do "tar x -C $work"

  # gofmt 只看本次提交碰过的文件：仓库里本来就有几个长期未格式化的
  # 存量文件，全量检查会永远红，变成噪声后就没人看了。
  local changed
  changed=$(git diff-tree --no-commit-id --name-only -r "$sha" -- 'backend/*.go' | sed 's|^backend/|./|' | tr '
' ' ')
  if [ -n "$changed" ]; then
    local unformatted
    unformatted=$(ssh_do "docker run --rm -v $work/backend:/w -w /w $GO_IMAGE gofmt -l $changed 2>/dev/null" || true)
    if [ -n "$unformatted" ]; then
      echo "  gofmt 未通过:" >&2; echo "$unformatted" >&2
      ssh_do "rm -rf $work"; return 1
    fi
    echo "  gofmt: ok"
  fi

  echo "  vet + 单测中（冷缓存约 11 分钟，热缓存约 4 分钟）…"
  if ! ssh_do "docker run --rm -v $work/backend:/w -w /w $GO_CACHE_MOUNTS       -e GOFLAGS=-mod=mod -e GOPROXY=https://goproxy.cn,direct       -e GOSUMDB=sum.golang.google.cn -e CGO_ENABLED=0 $GO_IMAGE       sh -c 'go vet -tags unit ./internal/... && go test -tags unit ./internal/... -count=1' 2>&1       | grep -vE '^go: downloading' | tail -40"; then
    echo "  校验失败，不发布" >&2
    ssh_do "rm -rf $work"; return 1
  fi
  ssh_do "rm -rf $work"
  echo "  校验通过"
}

case "${1:-}" in
  test)
    run_tests "${2:-HEAD}"
    exit $?
    ;;
  status)
    echo "== 运行镜像 =="; ssh_do "docker ps --filter name=^sub2api\$ --format '{{.Image}}  {{.Status}}'"
    echo "== /health ==";  ssh_do "curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18082/health"
    echo "== 可回滚的备份 =="; ssh_do "ls -1t $STACK/docker-compose.yml.bak-* 2>/dev/null | head -5"
    exit 0
    ;;
  rollback)
    LAST=$(ssh_do "ls -1t $STACK/docker-compose.yml.bak-* 2>/dev/null | head -1")
    [ -n "$LAST" ] || { echo "没有可回滚的备份" >&2; exit 1; }
    echo "回滚到: $LAST"
    ssh_do "cd $STACK && cp -p '$LAST' docker-compose.yml && docker compose up -d >/dev/null"
    health_wait; ssh_do "docker ps --filter name=^sub2api\$ --format '{{.Image}}  {{.Status}}'"
    exit 0
    ;;
  "")
    echo "用法: $0 <slug> [commit-ish] | test [commit-ish] | rollback | status" >&2; exit 1
    ;;
esac

SLUG="$1"; REF="${2:-HEAD}"
SHA=$(git rev-parse --short=9 "$REF")
TAG="${PREFIX}-${SLUG}-$(date +%Y%m%d)-${SHA}"
WORK="/tmp/s2a-rel-${SHA}"
LOG="/tmp/s2a-rel-${SHA}.log"

echo "== 发布 =="
echo "  提交: $SHA   镜像: $TAG"
echo "  当前: $(current_image)"

# 计费服务，默认先过测试再构建。构建本身要 8~12 分钟，热缓存下多这四五分钟换
# 一次「不会把编译得过但逻辑碰坏的代码推上去」，值。
if [ "${WXM_SKIP_TESTS:-}" = "1" ]; then
  echo "  ⚠ 已跳过测试（WXM_SKIP_TESTS=1）"
else
  run_tests "$SHA" || exit 1
fi

# 1) 推送源码（独立 ssh，不与构建混在一条命令里）
ssh_do "rm -rf $WORK $LOG && mkdir -p $WORK"
git archive "$SHA" --format=tar | ssh_do "tar x -C $WORK"
echo "  源码: $(ssh_do "find $WORK -type f | wc -l") 个文件"

# 2) 后台构建
ssh_do "cd $WORK && nohup docker build -t '$TAG' \
  --build-arg VERSION='$VERSION' --build-arg COMMIT='$SHA' \
  --build-arg NODE_IMAGE=$MIRROR/node:24-alpine \
  --build-arg GOLANG_IMAGE=$MIRROR/golang:1.26.5-alpine \
  --build-arg ALPINE_IMAGE=$MIRROR/alpine:3.21 \
  --build-arg GOPROXY=https://goproxy.cn,direct --build-arg GOSUMDB=sum.golang.google.cn \
  -f deploy/Dockerfile . > $LOG 2>&1 &" >/dev/null
echo "  构建中（约 8~12 分钟）…"

for _ in $(seq 1 60); do
  sleep 20
  if ssh_do "grep -q 'Successfully tagged' $LOG 2>/dev/null"; then break; fi
  if ssh_do "grep -qE 'returned a non-zero code|^ERROR' $LOG 2>/dev/null"; then
    echo "构建失败，末尾日志：" >&2; ssh_do "tail -25 $LOG" >&2; exit 1
  fi
  printf '    %s\n' "$(ssh_do "grep -E '^Step' $LOG | tail -1")"
done
ssh_do "grep -q 'Successfully tagged' $LOG" || { echo "构建超时" >&2; exit 1; }
echo "  构建完成: $(ssh_do "docker images '$TAG' --format '{{.Size}}'")"

# 3) 备份 compose 并换镜像
OLD=$(current_image)
BAK="docker-compose.yml.bak-${SLUG}-$(date +%Y%m%d)-${SHA}"
ssh_do "cd $STACK && cp -p docker-compose.yml '$BAK' && sed -i 's|^\( *image: \)${OLD}\$|\1${TAG}|' docker-compose.yml && diff '$BAK' docker-compose.yml || true"

# 4) 重启并等待健康；不健康则自动回滚
ssh_do "cd $STACK && docker compose up -d 2>&1 | tail -2"
if ! health_wait; then
  echo "健康检查未通过，自动回滚" >&2
  ssh_do "cd $STACK && cp -p '$BAK' docker-compose.yml && docker compose up -d >/dev/null"
  health_wait || true; exit 1
fi

ssh_do "docker ps --filter name=^sub2api\$ --format '  {{.Image}}  {{.Status}}'"
ssh_do "curl -s -o /dev/null -w '  /health: %{http_code}\n' http://127.0.0.1:18082/health"
ssh_do "rm -rf $WORK $LOG"
echo "== 完成 =="
echo "  回滚: $0 rollback"
