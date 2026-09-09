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
#   7. 所有 ssh 都带 keepalive，启动后台任务的那条还要 -n + 远端 </dev/null
#      —— 少了 keepalive，死连接会让脚本无限等；少了 stdin 重定向，
#         nohup 的子进程攥着 ssh 通道，ssh 等不到 EOF 同样不返回。
set -euo pipefail

HOST="${WXM_HOST:-wxm-tenant-platform-hz-01}"
STACK="${WXM_STACK:-/srv/wxm/platform/shared/sub2api}"
VERSION="${WXM_VERSION:-0.1.165}"
PREFIX="${WXM_TAG_PREFIX:-sub2api:${VERSION}-wxm2}"
MIRROR="docker.m.daocloud.io/library"
GO_IMAGE="${WXM_GO_IMAGE:-$MIRROR/golang:1.26.5-alpine}"
# Go 模块与构建缓存挂持久卷。实测：冷 10m22s~10m53s → 热 9m06s，只省 15%。
# 大头不是下模块而是编译 + 跑测（internal/service 单包 157s）。卷确实在用（模块
# 868M，构建缓存 2.1G / 约 1 万条目），所以剩下的时间就是真活，不是缓存未命中。
GO_CACHE_MOUNTS="-v s2a-gomodcache:/go/pkg/mod -v s2a-gobuildcache:/root/.cache/go-build"

# 构建缓存清理的磁盘占用阈值（%）。超过就全清，否则只清 24 小时前的。
DISK_PRUNE_THRESHOLD="${WXM_DISK_PRUNE_THRESHOLD:-80}"

# ServerAliveInterval/CountMax 是必需的，不是调优：没有它，连接被中间设备静默
# 丢弃后 ssh 会永远等下去。2026-09-08 就这么卡了 25 分钟——镜像早构好了，
# 脚本却停在一条死连接上，而且全部输出被管道缓冲，外面看不到任何进展。
# 60 秒内探测到对端消失，再交给轮询里已有的重试逻辑。
SSH_OPTS="-o ConnectTimeout=20 -o BatchMode=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=4"

ssh_do() { ssh $SSH_OPTS "$HOST" "$@"; }

# 启动服务器后台任务专用：-n 把本地 stdin 接到 /dev/null，远端再补一次重定向。
# 少了任何一边，nohup 出来的进程都还攥着 ssh 通道的 stdin，ssh 等不到 EOF
# 就不返回——命令明明已经在后台跑起来了，脚本却卡在这一行。
ssh_spawn() { ssh -n $SSH_OPTS "$HOST" "$@"; }

disk_used_percent() { ssh_do "df -h / | tail -1 | awk '{print \$5}' | tr -d '%'"; }

# prune_build_cache 发布成功后维护构建缓存。
#
# 为什么需要：一天二十来次完整构建（前端 npm + Go 全量编译）能堆出 77GB
# 缓存，2026-09-08 就把磁盘从 84% 顶到 92%。这台机器还跑着 23 个其它容器。
#
# 为什么不每次全清：全清后下次构建全冷，白白多花几分钟。常规只清 24 小时
# 未用的，当天的缓存留着；只有磁盘真的吃紧了才全清。
#
# 只动构建缓存，绝不碰镜像：docker image prune -a 会把旧的 sub2api 镜像一并
# 删掉，回滚就没了退路；这台机上还有其它服务的镜像。
prune_build_cache() {
  local used
  used=$(disk_used_percent)
  echo "  磁盘占用: ${used}%"
  ssh_do "docker builder prune -f --filter until=24h 2>&1 | tail -1" | sed 's/^/  /'
  if [ "${used:-0}" -ge "$DISK_PRUNE_THRESHOLD" ]; then
    echo "  超过 ${DISK_PRUNE_THRESHOLD}% 阈值，清空全部构建缓存"
    ssh_do "docker builder prune -a -f 2>&1 | tail -1" | sed 's/^/  /'
  fi
  ssh_do "df -h / | tail -1 | awk '{printf \"  清理后: %s / %s (%s)\n\", \$3, \$2, \$5}'"
}

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

  # 没碰 Go 源码就不用跑：只改前端 / 脚本 / 文档的提交没必要付这九分钟。
  if [ -z "$(git diff-tree --no-commit-id --name-only -r "$sha" -- 'backend/**/*.go' 'backend/go.mod' 'backend/go.sum')" ]; then
    echo "== 校验 $sha =="
    echo "  本提交未改动 Go 源码，跳过单测"
    return 0
  fi

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

  # 不跑独立的 go vet：go test 自带 vet 子集（printf/bool/atomic 等），而全量 vet
  # 要把所有包再编一遍；编译错误随后的 Docker 构建一定会抳到。
  # 实测：带 vet 9m06s → 不带 8m05s。
  echo "  单测中（约 8 分钟）…"
  # 测试输出先落文件、单独取退出码，再过滤打印。
  #
  # 原先写成 `go test ... | grep | tail`，而管道的退出码取自最后一个命令（tail，永远
  # 成功），go test 的失败被吞掉——关卡在测试 FAIL 时依然打印「校验通过」并放行。
  # 这是 2026-09-08 推退款持久化时撞上的：编译都没过，关卡却说通过。
  # 不用 set -o pipefail：镜像里是 busybox ash，该选项并非处处可用。
  #
  # 失败时先只拿 FAIL/编译错误行，而不是「滤掉 ok 剩下的全要」。
  #
  # 滤 ok 还不够：部分用例会向 stdout 打大量日志（如 [test] pre_check: …），
  # 同样会把 FAIL 行挤出窗口。先精确抛 FAIL/--- FAIL/“# 包名”（编译错误头），
  # 再跟一段原始尾巴供看断言详情。这个坑踩了两次：第一次是 tail 被 ok 挤掉，
  # 改成滤 ok 后又被用例自身的日志挤掉。
  if ! ssh_do "docker run --rm -v $work/backend:/w -w /w $GO_CACHE_MOUNTS       -e GOFLAGS=-mod=mod -e GOPROXY=https://goproxy.cn,direct       -e GOSUMDB=sum.golang.google.cn -e CGO_ENABLED=0 $GO_IMAGE       sh -c 'go test -tags unit ./internal/... -count=1 > /tmp/gotest.log 2>&1; rc=\$?; if [ \$rc -eq 0 ]; then tail -5 /tmp/gotest.log; else echo \"--- FAIL summary ---\"; grep -E \"^(FAIL|--- FAIL|# )\" /tmp/gotest.log | head -25; echo \"--- tail ---\"; grep -vE \"^(ok |\?|go: downloading)\" /tmp/gotest.log | tail -25; fi; exit \$rc'"; then
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

# 计费服务：测试不过就不构建。
#
# 曾改成「测试与构建并行」想把墙钟成本压到 0，实测失败：发布全程 20m11s，
# 几乎等于两者之和。原因是两边都是 Go 工具链、都按核数并行，而这台机器只有
# 8 核且跑着 20+ 个其他容器——CPU-bound 的活在同一台机上并行不了。
#
# 关卡实测成本：改了 Go 源码 8m05s；没改 0.2s（直接跳过）。
#
# 串行反而更好：成功路径上两者相当（~20 分钟），失败路径上串行 8 分钟
# 就能告诉你挂了并直接不构建，并行得等满 20 分钟。
if [ "${WXM_SKIP_TESTS:-}" = "1" ]; then
  echo "  ⚠ 已跳过测试（WXM_SKIP_TESTS=1）"
else
  run_tests "$SHA" || exit 1
fi

# 1) 推送源码（独立 ssh，不与构建混在一条命令里）
ssh_do "rm -rf $WORK $LOG && mkdir -p $WORK"
git archive "$SHA" --format=tar | ssh_do "tar x -C $WORK"
echo "  源码: $(ssh_do "find $WORK -type f | wc -l") 个文件"

# 2) 构建。
#
# 先看镜像在不在：本脚本已因 SSH 中断失败过三次（Connection reset by peer /
# client_loop: send disconnect），每次镜像其实都已构好，只是没走到换镜像那步，
# 得手工补完。镜像 tag 含 commit sha，同名即同代码，直接复用安全。
if ssh_do "docker images -q '$TAG' 2>/dev/null | grep -q ."; then
  echo "  镜像已存在，跳过构建（上次中断后重跑即可自愈）"
else
ssh_spawn "cd $WORK && nohup docker build -t '$TAG' \
  --build-arg VERSION='$VERSION' --build-arg COMMIT='$SHA' \
  --build-arg NODE_IMAGE=$MIRROR/node:24-alpine \
  --build-arg GOLANG_IMAGE=$MIRROR/golang:1.26.5-alpine \
  --build-arg ALPINE_IMAGE=$MIRROR/alpine:3.21 \
  --build-arg GOPROXY=https://goproxy.cn,direct --build-arg GOSUMDB=sum.golang.google.cn \
  -f deploy/Dockerfile . < /dev/null > $LOG 2>&1 &" >/dev/null
echo "  构建中（约 8~12 分钟）…"

# 轮询里的每一次 ssh 都可能掉线。不能让单次掉线直接结束发布：构建是 nohup
# 后台跑的，服务器侧不受影响，重试就能接上。连续多次掉线才当真故障。
consecutive_ssh_failures=0
for _ in $(seq 1 60); do
  sleep 20
  if ssh_do "grep -q 'Successfully tagged' $LOG 2>/dev/null"; then break; fi
  probe=$(ssh_do "grep -cE 'returned a non-zero code|^ERROR' $LOG 2>/dev/null || echo 0" 2>/dev/null) || probe=""
  if [ -z "$probe" ]; then
    consecutive_ssh_failures=$((consecutive_ssh_failures + 1))
    echo "    （ssh 探测失败 $consecutive_ssh_failures/5，构建在服务器后台继续）"
    if [ "$consecutive_ssh_failures" -ge 5 ]; then
      echo "ssh 连续失败，放弃轮询。镜像可能已构好，重跑本命令会直接跳过构建" >&2
      exit 1
    fi
    continue
  fi
  consecutive_ssh_failures=0
  if [ "$probe" != "0" ]; then
    echo "构建失败，末尾日志：" >&2; ssh_do "tail -25 $LOG" >&2; exit 1
  fi
  printf '    %s
' "$(ssh_do "grep -E '^Step' $LOG | tail -1" 2>/dev/null)"
done
ssh_do "grep -q 'Successfully tagged' $LOG" || { echo "构建超时" >&2; exit 1; }
echo "  构建完成: $(ssh_do "docker images '$TAG' --format '{{.Size}}'")"
fi

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

# 只在发布成功后清理：失败/回滚时留着缓存，重试才快。
echo "== 构建缓存维护 =="
prune_build_cache

echo "== 完成 =="
echo "  回滚: $0 rollback"
