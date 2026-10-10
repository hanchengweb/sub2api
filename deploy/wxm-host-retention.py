#!/usr/bin/env python3
"""wxm-tenant-platform-hz-01 生产机的磁盘保留策略。

默认只预演：列出将删除的镜像 / 目录 / 文件和**按层计算**的可回收空间，不动任何东西。
加 --apply 才执行。可以在每次发布成功后调用，也由 cron 每天兜底跑一次。

    python3 wxm-host-retention.py                 # 预演
    python3 wxm-host-retention.py --apply         # 执行
    python3 wxm-host-retention.py --keep 3        # 每条镜像线多留几个回滚版本

实际运行的是服务器上 /srv/wxm/platform/shared/ops/wxm-host-retention.py（hanc 的 crontab
每天 04:30 执行，wxm-release.sh 发布成功后也会调用）。源码在 sub2api 仓库
deploy/wxm-host-retention.py，改完要同步上传并核对哈希：
    ssh wxm-tenant-platform-hz-01 'cat > /srv/wxm/platform/shared/ops/wxm-host-retention.py \
      && sha256sum /srv/wxm/platform/shared/ops/wxm-host-retention.py' < deploy/wxm-host-retention.py

为什么要有它（2026-10-09）：这台机器上有 WindHub、租户、运营、college、identity
五套栈，至少四种发布流程（wxm-release.sh、Codex 的 deploy-*.py / media-deploy 等），
每次发布都会留下回滚镜像、发布目录和上传包，**没有一套流程会删旧的**。
三周内 276 个镜像 tag、157 GB 镜像存储，磁盘到 99%。

保留规则（全部满足才删，宁可多留）：
  1. 任何容器（含已停止的）在用的镜像，永远保留。
  2. 被下列清单引用的镜像永远保留：容器 compose 标签指向的配置文件；这些配置所在
     目录里最新的 K 份其它配置/回滚清单；各 backups 根目录下最新的 K 份备份；
     sub2api 最新的 K 份 docker-compose.yml.bak-*。
  3. 每条镜像线（仓库；rollback 仓库按服务细分）再保留最近 K 个**内容不同**的版本
     作为回滚锚点，超过 --anchor-max-days 天的不算锚点。
     注意：内容相同按层链判断，不按 tag。2026-10-09 实测同一天的 6 个租户 rollback
     tag 层链完全一致，`docker system df -v` 却给每个报 706MB 独占——containerd
     镜像存储下那一列不可信，删它们一个字节也腾不出来。
  4. 带镜像站前缀的基础镜像（repo 里含 /）不动；--min-age-hours 内新建的不动。
  5. 发布目录：被容器引用的 + 最近 K 个保留；上传包 1 天后删；/tmp 下本用户的
     条目 7 天后删。
  6. 构建缓存日常**不带 -a**，磁盘 ≥80% 才全清。带 -a 会连在用镜像的基础层缓存一起删，
     下一次发布全冷构建（原因见 Plan.apply）。
  7. 不碰：数据卷、数据库目录、backups 内容、日志、网关下载目录。

任何一个发布锁被占用（有发布在跑）或有 docker build 在跑，就整次跳过。
本脚本不启动任何容器：Codex 的发布脚本会在发布前后比对「所有运行中的容器」，
临时容器在它发布期间退出会被判成发布失败并触发回滚。
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import time
from pathlib import Path

LOCKS = [
    '/srv/wxm/tenant/shared/deploy.lock',
    '/srv/wxm/tenant/portal-release.lock',
    '/srv/wxm/platform/shared/deploy.lock',
    '/srv/wxm/platform/portal-release.lock',
    '/srv/wxm/platform/shared/sub2api/media-deploy.lock',
]
# 每个根目录下按修改时间取最新 K 个子目录，其中的回滚清单引用的镜像受保护
BACKUP_ROOTS = [
    '/srv/wxm/tenant/backups',
    '/srv/wxm/platform/backups',
    '/srv/wxm/platform/shared/sub2api/backups',
]
# wxm-release.sh 的回滚靠这些文件：rollback 子命令取最新一份
COMPOSE_BAK_GLOB = ('/srv/wxm/platform/shared/sub2api', 'docker-compose.yml.bak-*')
# 容器不引用但同样需要保护的配置目录（构建服务的 service.before-*.env 记着上一版镜像）
EXTRA_CONFIG_DIRS = ['/srv/wxm/platform/build-service/config']
RELEASE_ROOTS = [
    '/srv/wxm/tenant/releases',
    '/srv/wxm/platform/releases',
    '/srv/wxm/platform/shared/sub2api/releases',
    '/srv/wxm/identity/releases',
]
INCOMING_DIRS = [
    '/srv/wxm/tenant/incoming',
    '/srv/wxm/tenant/shared',
    '/srv/wxm/tenant/shared/incoming',
    '/srv/wxm/platform/incoming',
    '/srv/wxm/platform/shared/incoming',
    '/srv/wxm/platform/shared/sub2api/incoming',
]
ARCHIVE_RE = re.compile(r'\.(tar\.gz|tgz|tar|tar\.zst|tar\.uploading)$')
# rollback 仓库里 tag 形如 <服务>-<12 位 sha>，按服务各算一条镜像线
PER_SERVICE_REPOS = re.compile(r'^wxm-[a-z]+-rollback$')
MANIFEST_NAME_RE = re.compile(r'(\.json|\.ya?ml|\.env)$|^\.env|compose|rollback|runtime-before|before-')
REF_PATTERNS = [
    re.compile(r'sha256:[0-9a-f]{64}'),
    re.compile(r'''(?m)^\s*image:\s*["']?([^\s"'#]+)'''),
    # 不只 "image"：Codex 的 wemos-web 清单写的是 "rollbackImage"
    re.compile(r'"[A-Za-z_]*[Ii]mage"\s*:\s*"([^"]+)"'),
    re.compile(r'(?m)^[A-Z0-9_]*IMAGE=([^\s#]+)'),
]
LOG_DIR = Path('/srv/wxm/platform/shared/ops/logs')


def sh(*args: str) -> str:
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout


def human(n: float) -> str:
    for unit in ('B', 'KB', 'MB', 'GB', 'TB'):
        if abs(n) < 1024 or unit == 'TB':
            return f'{n:.1f}{unit}' if unit != 'B' else f'{int(n)}B'
        n /= 1024
    return str(n)


def parse_time(value: str) -> float:
    # docker 的时间带纳秒，Python 只认微秒
    value = re.sub(r'(\.\d{6})\d+', r'\1', value.replace('Z', '+00:00'))
    return dt.datetime.fromisoformat(value).timestamp()


def disk_free() -> tuple[int, int]:
    usage = shutil.disk_usage('/')
    return usage.used, usage.free


def tree_size(path: Path) -> int:
    total, seen = 0, set()
    for root, dirs, files in os.walk(path, onerror=lambda e: None):
        for name in dirs + files:
            try:
                st = os.lstat(os.path.join(root, name))
            except OSError:
                continue
            if (st.st_dev, st.st_ino) in seen:
                continue
            seen.add((st.st_dev, st.st_ino))
            total += st.st_blocks * 512
    try:
        total += os.lstat(path).st_blocks * 512
    except OSError:
        pass
    return total


def remove_tree(path: Path) -> None:
    # Go 模块缓存之类的目录是只读的，删前先把父目录改成可写
    def onexc(func, p, exc):
        try:
            os.chmod(os.path.dirname(p), stat.S_IRWXU)
            if os.path.isdir(p) and not os.path.islink(p):
                os.chmod(p, stat.S_IRWXU)
            func(p)
        except OSError:
            raise exc
    shutil.rmtree(path, onexc=onexc)


def lineage_key(repo: str, tag: str) -> str | None:
    """镜像线：同一仓库算一条，rollback 仓库按服务各算一条；基础镜像和悬空镜像不归线。"""
    if repo == '<none>' or '/' in repo or tag == '<none>':
        return None
    if PER_SERVICE_REPOS.match(repo):
        return repo + ':' + re.sub(r'-[0-9a-f]{7,40}$', '', tag)
    return repo


def classify_tags(images, created, top, protected, in_use, keep, min_age, anchor_age, now):
    """决定每个应用镜像 tag 的去留，返回 [(ref, 镜像 ID, 是否删除, 原因)]。

    纯函数，不碰 docker，方便单测。images 是 {镜像 ID: [(repo, tag)]}，created 是创建
    时间戳，top 是层链末端（两张镜像末端相同即内容相同），protected 是 {镜像 ID: 保护原因}，
    in_use 是容器在用的镜像 ID。
    """
    in_use_tops = {top[i] for i in in_use if i in top}
    lineages: dict[str, list[tuple[str, str, str]]] = {}
    for iid, tags in images.items():
        for repo, tag in tags:
            key = lineage_key(repo, tag)
            if key:
                lineages.setdefault(key, []).append((f'{repo}:{tag}', iid, tag))
    result = []
    for _, entries in sorted(lineages.items()):
        entries.sort(key=lambda e: (created[e[1]], e[2]), reverse=True)
        anchors, anchor_tops = 0, set()
        for ref, iid, _ in entries:
            age = now - created[iid]
            if iid in protected:
                # 清单保护的版本也算回滚锚点；但和在用镜像内容相同的不算，
                # 否则真正的上一版会被它们挤掉（租户每次发布都会产生这种同层 tag）
                if top[iid] not in in_use_tops and top[iid] not in anchor_tops:
                    anchors += 1
                anchor_tops.add(top[iid])
                result.append((ref, iid, False, protected[iid]))
            elif age < min_age:
                result.append((ref, iid, False, '新建不足 min-age'))
            elif top[iid] in anchor_tops:
                result.append((ref, iid, False, '与保留版本内容相同'))
            elif top[iid] in in_use_tops:
                result.append((ref, iid, True, '与在用镜像层链相同（冗余 tag，不占空间）'))
            elif anchors < keep and age <= anchor_age:
                anchors += 1
                anchor_tops.add(top[iid])
                result.append((ref, iid, False, f'回滚锚点 #{anchors}'))
            else:
                why = f'超出最近 {keep} 个版本' if age <= anchor_age else f'{age / 86400:.0f} 天前，超出锚点期限'
                result.append((ref, iid, True, why))
    return result


def held_locks(text: str) -> set[tuple[int, int, int]]:
    """解析 /proc/locks，返回被持有（或有人在等）的锁所在文件的 (主设备号, 次设备号, inode)。

    每行形如 `1: FLOCK  ADVISORY  WRITE 3801770 fd:03:2753597 0 EOF`，等待者行多一个 `->`。
    """
    held = set()
    for line in text.splitlines():
        for part in line.split():
            bits = part.split(':')
            if len(bits) == 3 and bits[2].isdigit():
                try:
                    held.add((int(bits[0], 16), int(bits[1], 16), int(bits[2])))
                except ValueError:
                    pass
                break
    return held


def check_quiet() -> str | None:
    """有发布在跑就返回原因；空表示可以动手。

    只读 /proc/locks 判断锁有没有被持有，**不去抢锁**：哪怕只抢一微秒，撞上发布脚本启动时的
    flock(LOCK_EX | LOCK_NB) 也会让它直接报错退出——Codex 的 deploy-*.py 就是这么拿锁的。
    """
    try:
        held = held_locks(Path('/proc/locks').read_text())
    except OSError:
        return '读不了 /proc/locks，保守起见跳过'
    for lock in LOCKS:
        try:
            st = os.stat(lock)
        except OSError:
            continue
        if (os.major(st.st_dev), os.minor(st.st_dev), st.st_ino) in held:
            return f'发布锁被占用：{lock}'
    procs = subprocess.run(['pgrep', '-af', r'docker (build|buildx|compose .*build)|buildctl build'],
                           capture_output=True, text=True).stdout.splitlines()
    procs = [p for p in procs if 'pgrep' not in p]
    if procs:
        return f'有构建在跑：{procs[0][:120]}'
    return None


def extract_refs(text: str) -> set[str]:
    refs = set()
    for pattern in REF_PATTERNS:
        for match in pattern.finditer(text):
            refs.add(match.group(1) if pattern.groups else match.group(0))
    return refs


def read_text(path: Path) -> str:
    try:
        if path.stat().st_size > 2 * 1024 * 1024:
            return ''
        return path.read_text(errors='ignore')
    except OSError:
        return ''


class Plan:
    def __init__(self, keep: int, min_age_h: float, anchor_days: float):
        self.keep, self.min_age, self.anchor_age = keep, min_age_h * 3600, anchor_days * 86400
        self.now = time.time()
        self.notes: list[str] = []
        self.delete_tags: list[tuple[str, str, str]] = []      # (ref, image id, 原因)
        self.delete_dangling: list[tuple[str, str]] = []
        self.keep_tags: list[tuple[str, str]] = []
        self.delete_paths: list[tuple[str, Path, int, str]] = []  # (类别, 路径, 体积, 原因)
        self.reclaim_images = 0

    # ---------- 镜像 ----------
    def plan_images(self) -> None:
        containers = json.loads(sh('docker', 'inspect', *sh('docker', 'ps', '-aq', '--no-trunc').split()))
        in_use = {c['Image'] for c in containers}
        config_files, live_dirs = set(), set()
        for c in containers:
            labels = c['Config'].get('Labels') or {}
            for f in (labels.get('com.docker.compose.project.config_files') or '').split(','):
                if f:
                    config_files.add(Path(f))
                    live_dirs.add(Path(f).parent)
        self.live_release_dirs = set()
        for c in containers:
            labels = c['Config'].get('Labels') or {}
            for value in [labels.get('com.docker.compose.project.working_dir') or '',
                          *(labels.get('com.docker.compose.project.config_files') or '').split(',')]:
                if value:
                    self.live_release_dirs.add(value)

        images = {}
        for line in sh('docker', 'image', 'ls', '--no-trunc', '--format', '{{.ID}}\t{{.Repository}}\t{{.Tag}}').splitlines():
            iid, repo, tag = line.split('\t')
            images.setdefault(iid, []).append((repo, tag))
        # containerd 镜像存储下 `docker image ls` 默认不列悬空镜像，得单独取
        # （2026-10-09 漏过一次：9 张、约 13GB 的构建中间镜像没进清单）
        for iid in sh('docker', 'image', 'ls', '--no-trunc', '-q', '-f', 'dangling=true').split():
            images.setdefault(iid, [('<none>', '<none>')])
        details = {d['Id']: d for d in json.loads(sh('docker', 'image', 'inspect', *images))}

        # 层链：两张镜像只有全部层一致才算同一个版本
        chains, top = {}, {}
        for iid, d in details.items():
            h, keys = hashlib.sha256(), []
            for layer in d['RootFS'].get('Layers') or []:
                h.update(layer.encode())
                keys.append(h.hexdigest())
            chains[iid], top[iid] = keys, (keys[-1] if keys else iid)
        created = {iid: parse_time(d['Created']) for iid, d in details.items()}

        # 把清单里的引用解析成镜像 ID
        by_ref = {}
        for iid, tags in images.items():
            by_ref[iid] = iid
            by_ref[iid.split(':', 1)[1][:12]] = iid
            for repo, tag in tags:
                if tag != '<none>':
                    by_ref[f'{repo}:{tag}'] = iid
                    if tag == 'latest':
                        by_ref[repo] = iid
            for digest in details[iid].get('RepoDigests') or []:
                by_ref[digest] = iid

        protected: dict[str, str] = {iid: '容器在用' for iid in in_use}

        def protect_from(path: Path, why: str) -> None:
            for ref in extract_refs(read_text(path)):
                iid = by_ref.get(ref) or by_ref.get(ref.split('@')[-1])
                if iid and iid not in protected:
                    protected[iid] = f'{why}: {path}'

        for f in config_files:
            protect_from(f, '当前配置引用')
        def listdir(d: Path) -> list[Path]:
            # 别的用户建的目录可能读不了；读不了就当没有，不能让整次运行中断
            try:
                return list(d.iterdir())
            except OSError:
                return []

        for d in sorted(live_dirs | {Path(p) for p in EXTRA_CONFIG_DIRS}):
            if not d.is_dir():
                continue
            others = [p for p in listdir(d) if p.is_file() and p not in config_files
                      and MANIFEST_NAME_RE.search(p.name)]
            others.sort(key=lambda p: p.stat().st_mtime, reverse=True)
            for p in others[:self.keep]:
                protect_from(p, '同目录最新回滚清单')
            if str(d) in EXTRA_CONFIG_DIRS:
                for p in others:
                    protect_from(p, '构建服务配置')
        for root in BACKUP_ROOTS:
            base = Path(root)
            if not base.is_dir():
                continue
            # 一份备份可能是一个目录（deploy-*.py 流程），也可能是根下的一个清单文件
            # （wemos-web 流程的 wemos-web-<时间>.json），两种都算
            units = []
            for sub in listdir(base):
                if sub.is_dir():
                    files = [p for p in listdir(sub) if p.is_file() and MANIFEST_NAME_RE.search(p.name)]
                elif sub.is_file() and MANIFEST_NAME_RE.search(sub.name):
                    files = [sub]
                else:
                    continue
                if any(extract_refs(read_text(p)) for p in files):
                    units.append((sub.stat().st_mtime, files))
            for _, files in sorted(units, key=lambda t: t[0], reverse=True)[:self.keep]:
                for p in files:
                    protect_from(p, '最新备份')
        bak_dir, pattern = COMPOSE_BAK_GLOB
        baks = sorted(Path(bak_dir).glob(pattern), key=lambda p: p.stat().st_mtime, reverse=True)
        for p in baks[:self.keep]:
            protect_from(p, 'wxm-release.sh 回滚备份')
        self.protected = protected

        doomed_tags: dict[str, list[str]] = {}
        for ref, iid, drop, why in classify_tags(images, created, top, protected, in_use,
                                                 self.keep, self.min_age, self.anchor_age, self.now):
            if drop:
                doomed_tags.setdefault(iid, []).append(ref)
                self.delete_tags.append((ref, iid, why))
            else:
                self.keep_tags.append((ref, why))

        dangling = [iid for iid, tags in images.items() if all(r == '<none>' for r, _ in tags)]
        for iid in dangling:
            if iid in protected:
                continue
            if self.now - created[iid] < self.min_age:
                continue
            self.delete_dangling.append((iid, f'悬空镜像，{(self.now - created[iid]) / 3600:.0f} 小时前'))

        # 一张镜像的所有 tag 都要删才真正删掉镜像
        gone = {iid for iid, refs in doomed_tags.items()
                if len(refs) == sum(1 for _, t in images[iid] if t != '<none>')}
        gone |= {iid for iid, _ in self.delete_dangling}
        kept_chain_keys = set()
        for iid in details:
            if iid not in gone:
                kept_chain_keys.update(chains[iid])
        sizes = {}
        for iid in gone:
            history = [int(s or 0) for s in sh('docker', 'history', '--no-trunc', '--human=false',
                                                '--format', '{{.Size}}', iid).split()]
            history.reverse()
            layer_sizes = [s for s in history if s > 0]
            keys = chains[iid]
            # 层数对不齐（有 0 字节的真层）时从顶层对齐：独有的大多是上面几层
            pad = len(keys) - len(layer_sizes)
            layer_sizes = [0] * max(pad, 0) + layer_sizes[max(-pad, 0):]
            for key, size in zip(keys, layer_sizes):
                if key not in kept_chain_keys:
                    sizes[key] = size
        self.reclaim_images = sum(sizes.values())
        self.gone_images = gone

    # ---------- 文件 ----------
    def plan_files(self) -> None:
        live = self.live_release_dirs
        for root in RELEASE_ROOTS:
            base = Path(root)
            if not base.is_dir():
                continue
            subs = [p for p in base.iterdir() if p.is_dir() and not p.is_symlink()]
            referenced = {p for p in subs if any(v == str(p) or v.startswith(str(p) + '/') for v in live)}
            rest = sorted((p for p in subs if p not in referenced), key=lambda p: p.stat().st_mtime, reverse=True)
            for p in rest[self.keep:]:
                age = self.now - p.stat().st_mtime
                if age < 86400:
                    continue
                self.delete_paths.append(('发布目录', p, tree_size(p), f'未被容器引用，{age / 86400:.0f} 天前'))
        for d in INCOMING_DIRS:
            base = Path(d)
            if not base.is_dir():
                continue
            for p in base.iterdir():
                if p.is_file() and not p.is_symlink() and ARCHIVE_RE.search(p.name):
                    age = self.now - p.stat().st_mtime
                    if age >= 86400:
                        self.delete_paths.append(('上传包', p, p.stat().st_blocks * 512, f'{age / 86400:.0f} 天前'))
        uid = os.getuid()
        for p in Path('/tmp').iterdir():
            try:
                st = p.lstat()
            except OSError:
                continue
            if st.st_uid != uid or not (stat.S_ISREG(st.st_mode) or stat.S_ISDIR(st.st_mode)):
                continue
            # 目录看最近一次改动（含子项），避免删掉正在用的工作目录
            newest = st.st_mtime
            if stat.S_ISDIR(st.st_mode):
                for root, dirs, files in os.walk(p, onerror=lambda e: None):
                    for name in dirs + files:
                        try:
                            newest = max(newest, os.lstat(os.path.join(root, name)).st_mtime)
                        except OSError:
                            pass
            age = self.now - newest
            if age >= 7 * 86400:
                size = tree_size(p) if stat.S_ISDIR(st.st_mode) else st.st_blocks * 512
                self.delete_paths.append(('/tmp', p, size, f'{age / 86400:.0f} 天未改动'))

    # ---------- 输出 ----------
    def report(self, out) -> None:
        print('== 镜像：删除 tag ==', file=out)
        for ref, iid, why in sorted(self.delete_tags):
            mark = '' if iid in self.gone_images else '（仅去 tag，镜像仍被其它 tag 引用）'
            print(f'  {ref:<72} {why}{mark}', file=out)
        print('== 镜像：删除悬空 ==', file=out)
        for iid, why in self.delete_dangling:
            print(f'  {iid[7:19]}  {why}', file=out)
        groups: dict[str, list[tuple[Path, int, str]]] = {}
        for kind, p, size, why in self.delete_paths:
            groups.setdefault(kind, []).append((p, size, why))
        for kind, items in groups.items():
            print(f'== {kind}：删除 {len(items)} 项，{human(sum(s for _, s, _ in items))} ==', file=out)
            for p, size, why in sorted(items, key=lambda t: -t[1]):
                print(f'  {human(size):>8}  {p}  {why}', file=out)
        print('== 保留的应用镜像 tag ==', file=out)
        for ref, why in sorted(self.keep_tags):
            print(f'  {ref:<72} {why}', file=out)
        files_total = sum(s for _, _, s, _ in self.delete_paths)
        print('== 汇总 ==', file=out)
        print(f'  删 tag {len(self.delete_tags)} 个、悬空镜像 {len(self.delete_dangling)} 个，'
              f'实际删掉镜像 {len(self.gone_images)} 张；按层计算可回收约 {human(self.reclaim_images)}'
              '（构建缓存还占着同一批层，清完缓存才落到磁盘上）', file=out)
        print(f'  删文件/目录 {len(self.delete_paths)} 项，约 {human(files_total)}', file=out)
        print(f'  保留应用镜像 tag {len(self.keep_tags)} 个', file=out)

    # ---------- 执行 ----------
    def apply(self, out) -> None:
        failures = 0
        for ref, iid, _ in self.delete_tags:
            r = subprocess.run(['docker', 'image', 'rm', ref], capture_output=True, text=True)
            if r.returncode:
                failures += 1
                print(f'  ! 删 {ref} 失败：{r.stderr.strip()[:200]}', file=out)
        for iid, _ in self.delete_dangling:
            r = subprocess.run(['docker', 'image', 'rm', iid], capture_output=True, text=True)
            if r.returncode:
                failures += 1
                print(f'  ! 删 {iid[7:19]} 失败：{r.stderr.strip()[:200]}', file=out)
        # 经典构建器的中间镜像是链式的：删掉子镜像，父镜像才变成悬空。一轮清不干净，
        # 循环到不再冒出新的为止（2026-10-09 第一轮删完又露出 3 张、约 3GB）
        tried = {iid for iid, _ in self.delete_dangling}
        for _ in range(5):
            fresh = [i for i in sh('docker', 'image', 'ls', '--no-trunc', '-q', '-f', 'dangling=true').split()
                     if i not in tried and i not in self.protected]
            if not fresh:
                break
            for iid in fresh:
                tried.add(iid)
                born = parse_time(sh('docker', 'image', 'inspect', '--format', '{{.Created}}', iid).strip())
                if time.time() - born < self.min_age:
                    continue
                r = subprocess.run(['docker', 'image', 'rm', iid], capture_output=True, text=True)
                if r.returncode == 0:
                    print(f'  删除新露出的悬空镜像 {iid[7:19]}', file=out)
        used, free = disk_free()
        pct = used * 100 // (used + free)
        # 删掉的镜像，它们的层还被构建缓存记录引用着，要清缓存才真正落到磁盘上。
        #
        # 日常**不带 -a**：不带 -a 时 BuildKit 不动「层还被现有镜像引用」的记录，在用镜像的
        # 基础层（apt / pip / npm）缓存因此保得住；被删镜像的层已不再被引用，照样会清掉。
        # 2026-10-09 带过 -a --filter until=24h：清出 66GB，但租户后端的 apt/pip 缓存也没了。
        # 18:20 那次构建还命中过这些缓存，2.5 小时后却被当成「24 小时没用过」删掉——只作为
        # 父层被命中的记录看来不刷新最近使用时间。下一次发布全冷构建，pip 走 aliyun 约
        # 50KB/s，构建被取消、发布失败。所以只有磁盘 ≥80% 才全清，宁可冷构建也不能写满。
        if pct >= 80:
            args, label = ['docker', 'builder', 'prune', '-af'], '全清，磁盘 ≥80%'
        else:
            args, label = ['docker', 'builder', 'prune', '-f', '--filter', 'until=24h'], '24 小时未用且不被镜像引用的'
        r = subprocess.run(args, capture_output=True, text=True)
        print(f'  构建缓存（{label}）：{(r.stdout.strip().splitlines() or ["?"])[-1]}', file=out)
        allowed = [Path(p) for p in RELEASE_ROOTS + INCOMING_DIRS + ['/tmp']]
        for kind, p, _, _ in self.delete_paths:
            real = Path(os.path.realpath(p))
            if not any(real.parent == a for a in allowed):
                failures += 1
                print(f'  ! 跳过 {p}：不在允许的目录里', file=out)
                continue
            try:
                if p.is_dir() and not p.is_symlink():
                    remove_tree(p)
                else:
                    p.unlink()
            except OSError as e:
                failures += 1
                print(f'  ! 删 {p} 失败：{e}', file=out)
        print(f'  失败 {failures} 项', file=out)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument('--apply', action='store_true', help='真的删（默认只预演）')
    ap.add_argument('--keep', type=int, default=2, help='每条镜像线 / 发布目录额外保留的版本数')
    ap.add_argument('--min-age-hours', type=float, default=2)
    ap.add_argument('--anchor-max-days', type=float, default=14)
    args = ap.parse_args()

    stamp = dt.datetime.now().strftime('%Y%m%dT%H%M%S')
    out = Tee(sys.stdout)
    if args.apply:
        LOG_DIR.mkdir(parents=True, exist_ok=True)
        out.files.append((LOG_DIR / f'retention-{stamp}.log').open('w'))
        # 本脚本自己的日志也只留 30 天
        for old in LOG_DIR.glob('retention-*.log'):
            if time.time() - old.stat().st_mtime > 30 * 86400:
                old.unlink(missing_ok=True)
    reason = check_quiet()
    if reason:
        print(f'# {stamp} 跳过：{reason}', file=out)
        out.close()
        return 0
    plan = Plan(args.keep, args.min_age_hours, args.anchor_max_days)
    plan.plan_images()
    plan.plan_files()

    used0, free0 = disk_free()
    print(f'# {stamp} keep={args.keep} apply={args.apply} 磁盘 已用 {human(used0)} 剩余 {human(free0)}', file=out)
    plan.report(out)
    if args.apply:
        reason = check_quiet()
        if reason:
            print(f'中止：规划期间开始了发布（{reason}）', file=out)
            return 0
        print('== 执行 ==', file=out)
        plan.apply(out)
        _, free1 = disk_free()
        print(f'  磁盘 剩余 {human(free0)} -> {human(free1)}（回收 {human(free1 - free0)}）', file=out)
    out.close()
    return 0


class Tee:
    def __init__(self, *files):
        self.files = list(files)

    def write(self, text):
        for f in self.files:
            f.write(text)
            f.flush()

    def flush(self):
        pass

    def close(self):
        for f in self.files[1:]:
            f.close()


if __name__ == '__main__':
    sys.exit(main())
