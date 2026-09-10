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
#   8. 单测跳过与否，基线是「线上正在跑的提交」而不是父提交
#      —— 按父提交比，Go 改动落在前一个 commit 时会被整段跳过，
#         镜像里却装着没测过的代码。
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
# 只动构建缓存和**悬空**镜像，不碰有 tag 的镜像。
#
# 这里原先写的是「绝不碰镜像：docker image prune -a 会把旧的 sub2api 镜像一并
# 删掉」——那句话混淆了两个命令。不带 -a 的 docker image prune 只删悬空镜像
# （<none>，没有任何 tag 引用的中间层），有 tag 的回滚目标和其它项目的镜像都不动。
#
# 代价是实打实的：每次用同一 tag 重建都会让上一版的层变成悬空，一个 1~3GB。
# 2026-09-09 一天攒了 114 个、占掉 130GB，磁盘冲到 98%，而脚本每次都报
# 「回收 0B」——因为它只清 builder cache，那部分本来就是空的，真正的大头没人管。
# 加上这一行后当场从 97% 降到 27%。
#
# 仍然不用 -a：那会连带删掉这台机器上其它项目（wxm-tenant / wxm-platform /
# wxm-college）未在运行的镜像。
prune_build_cache() {
  local used
  used=$(disk_used_percent)
  echo "  磁盘占用: ${used}%"
  ssh_do "docker builder prune -f --filter until=24h 2>&1 | tail -1" | sed 's/^/  /'
  ssh_do "docker image prune -f 2>&1 | tail -1" | sed 's/^/  悬空镜像 /'
  if [ "${used:-0}" -ge "$DISK_PRUNE_THRESHOLD" ]; then
    echo "  超过 ${DISK_PRUNE_THRESHOLD}% 阈值，清空全部构建缓存"
    ssh_do "docker builder prune -a -f 2>&1 | tail -1" | sed 's/^/  /'
  fi
  ssh_do "df -h / | tail -1 | awk '{printf \"  清理后: %s / %s (%s)\n\", \$3, \$2, \$5}'"
}

current_image() { ssh_do "grep -m1 -oE 'sub2api:[^[:space:]]+' $STACK/docker-compose.yml"; }

# 从线上镜像 tag 末段取出已部署的 commit sha。tag 形如
# sub2api:<版本>-wxm2-<标签>-<日期>-<sha>，末段就是 sha。
deployed_sha() { current_image 2>/dev/null | awk -F- '{print $NF}' | tr -d '[:space:]'; }

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
  #
  # 基线必须是**线上正在跑的那个提交**，不是本提交的父提交。用父提交比会漏：
  # Go 改动在前一个 commit、这次发的是后面只改文档的 commit，单测就被跳过了，
  # 而镜像里装的仍是没测过的 Go 代码。解析不出基线时一律跑测试，宁可多花九分钟。
  local base go_changed
  base=$(deployed_sha)
  if [ -n "$base" ] && git cat-file -e "${base}^{commit}" 2>/dev/null; then
    go_changed=$(git diff --name-only "$base" "$sha" -- 'backend/**/*.go' 'backend/go.mod' 'backend/go.sum')
  else
    go_changed="unknown-baseline"
  fi
  if [ -z "$go_changed" ]; then
    echo "== 校验 $sha =="
    echo "  本提交未改动 Go 源码，跳过单测"
    return 0
  fi

  echo "== 校验 $sha =="
  # 源码上传要重试并核验落地。
  #
  # 2026-09-09 撞到过一次假失败：上传半途 Connection reset by peer，源码没落全，
  # 闸门却报「directory prefix internal does not contain main module」——看起来
  # 像 Go 代码坏了，实际是传输断了。gofmt 那步也证明不了什么：文件缺失时它的
  # 报错被 2>/dev/null 吞掉，照样打印 ok。所以这里必须显式核验。
  local uploaded=0 attempt
  for attempt in 1 2 3; do
    ssh_do "rm -rf $work && mkdir -p $work" || { sleep 3; continue; }
    if git archive "$sha" --format=tar | ssh_do "tar x -C $work"; then
      if ssh_do "test -f $work/backend/go.mod"; then
        uploaded=1
        break
      fi
    fi
    echo "    （源码上传第 $attempt 次未落全，重试）"
    sleep 3
  done
  if [ "$uploaded" -ne 1 ]; then
    echo "源码上传失败：$work/backend/go.mod 不存在。这是传输问题，不是代码问题" >&2
    ssh_do "rm -rf $work" || true
    return 1
  fi

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

# 1) 推送源码（独立 ssh，不与构建混在一条命令里）。$WORK 的清理交给下面的重试循环。
ssh_do "rm -f $LOG"
# 与校验那步同理：上传断了要重试并核验，否则可能构出一个「成功但内容不全」
# 的镜像——那比构建失败更难发现。
uploaded=0
for attempt in 1 2 3; do
  ssh_do "rm -rf $WORK && mkdir -p $WORK" || { sleep 3; continue; }
  if git archive "$SHA" --format=tar | ssh_do "tar x -C $WORK"; then
    if ssh_do "test -f $WORK/backend/go.mod && test -f $WORK/deploy/Dockerfile"; then
      uploaded=1
      break
    fi
  fi
  echo "  （源码上传第 $attempt 次未落全，重试）"
  sleep 3
done
if [ "$uploaded" -ne 1 ]; then
  echo "源码上传失败：$WORK 内容不完整。这是传输问题，不是代码问题" >&2
  exit 1
fi
echo "  源码: $(ssh_do "find $WORK -type f | wc -l") 个文件"

# 2) 构建。
#
# 先看镜像在不在：本脚本已因 SSH 中断失败过三次（Connection reset by peer /
# client_loop: send disconnect），每次镜像其实都已构好，只是没走到换镜像那步，
# 得手工补完。镜像 tag 含 commit sha，同名即同代码，直接复用安全。
if ssh_do "docker images -q '$TAG' 2>/dev/null | grep -q ."; then
  echo "  镜像已存在，跳过构建（上次中断后重跑即可自愈）"
else
# 启动构建也要能扛住一次网络抖动：这条 ssh 一旦失败，set -e 会直接结束发布，
# 而这台机器今天已经重置过三次连接。重试前先看镜像在不在、构建是不是已经在跑，
# 避免同一个 tag 起两个构建。
build_launched=0
for attempt in 1 2 3; do
  if ssh_do "docker images -q '$TAG' 2>/dev/null | grep -q ."; then build_launched=1; break; fi
  # 曾经在这里用 pgrep -f 判断「构建是否已在跑」，那是错的：pgrep -f 会匹配到
  # 承载它自己的那层 bash -c——命令行里就含有这个 tag 字符串，于是永远返回命中，
  # 构建被整个跳过（工作目录建好了，日志和镜像都没有）。改用日志文件是否存在来判断。
  if ssh_do "test -s $LOG"; then build_launched=1; break; fi
  # 构建命令先落成远端脚本，再用 setsid 启动。
  #
  # 直接 ssh "nohup docker build ... &" 会挂：即便本地 -n、远端 </dev/null，
  # 后台进程仍留在 ssh 的会话里，ssh 等不到会话结束就不返回。今天在这一处挂了
  # 两次，而且日志文件都没被创建——说明远端命令根本没跑起来。setsid 让构建
  # 脱离该会话自成进程组，ssh 于是立刻返回。
  if ssh_do "cat > $WORK/.build.sh" <<BUILDSH
cd $WORK && exec docker build -t '$TAG' \
  --build-arg VERSION='$VERSION' --build-arg COMMIT='$SHA' \
  --build-arg NODE_IMAGE=$MIRROR/node:24-alpine \
  --build-arg GOLANG_IMAGE=$MIRROR/golang:1.26.5-alpine \
  --build-arg ALPINE_IMAGE=$MIRROR/alpine:3.21 \
  --build-arg GOPROXY=https://goproxy.cn,direct --build-arg GOSUMDB=sum.golang.google.cn \
  -f deploy/Dockerfile .
BUILDSH
  then
    if ssh_spawn "setsid nohup sh $WORK/.build.sh < /dev/null > $LOG 2>&1 &" >/dev/null; then
      build_launched=1
      break
    fi
  fi
  echo "  （构建启动第 $attempt 次失败，重试）"
  sleep 5
done
if [ "$build_launched" -ne 1 ]; then
  echo "构建启动失败：ssh 连不上。这是传输问题，不是代码问题" >&2
  exit 1
fi
echo "  构建中（约 8~12 分钟）…"

# 轮询里的每一次 ssh 都可能掉线。不能让单次掉线直接结束发布：构建是 nohup
# 后台跑的，服务器侧不受影响，重试就能接上。连续多次掉线才当真故障。
consecutive_ssh_failures=0
for _ in $(seq 1 60); do
  sleep 20
  if ssh_do "grep -q 'Successfully tagged' $LOG 2>/dev/null"; then break; fi
  # 探测构建是否失败。
  #
  # 旧写法 `grep -cE ... || echo 0` 有个潜伏很久的 bug：grep -c 在零匹配时
  # **既打印 0、又返回退出码 1**，于是 `|| echo 0` 再追加一个 0，探测值变成
  # "0\n0"，随后 != "0" 就把一次完全正常的构建判成失败。只有日志已存在且内容
  # 正常时才触发——也就是构建一切顺利的时候，所以一直没被发现。
  #
  # 现在末尾追加哨兵 done：拿得到 done 说明这次 ssh 通了，第一行才是计数；
  # 拿不到 done 才是 ssh 断线，交给下面的重试逻辑。
  probe_raw=$(ssh_do "grep -cE 'returned a non-zero code|^ERROR' $LOG 2>/dev/null; echo done" 2>/dev/null) || probe_raw=""
  if [ "${probe_raw%done}" = "$probe_raw" ]; then
    probe=""
  else
    probe=$(printf '%s' "$probe_raw" | head -1)
    case "$probe" in '' | done) probe=0 ;; esac
  fi
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
