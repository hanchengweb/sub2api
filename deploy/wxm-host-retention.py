#!/usr/bin/env python3
"""wxm-tenant-platform-hz-01 生产机的磁盘保留策略。

默认只预演：列出将删除的镜像 / 目录 / 文件和**按层计算**的可回收空间，不动任何东西。
加 --apply 才执行。可以在每次发布成功后调用，也由 cron 定期兜底。

    python3 wxm-host-retention.py                 # 预演
    python3 wxm-host-retention.py --apply         # 执行
    python3 wxm-host-retention.py --keep 3        # 每条镜像线多留几个回滚版本

实际运行的是服务器上 /srv/wxm/platform/shared/ops/wxm-host-retention.py（hanc 的 crontab
每 4 小时的 :30 执行，wxm-release.sh 发布成功后也会调用）。源码在 sub2api 仓库
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
  5. 发布目录：被容器、回滚清单和进程引用的 + 最近 K 个保留；上传包 1 天后删；
     /tmp 本用户已完成的 wxm UI / candidate / build / release 目录 1 天后删，其它条目 7 天。
     混合所有者、不可读、只读目录和挂载树保留，不改权限。
  6. 构建缓存始终只清 24 小时未用且不被镜像引用的记录，绝不自动全清。
  7. 不碰：数据卷、数据库目录、backups 内容、日志、网关下载目录。

任何一个发布锁被占用（有发布在跑）或有 docker build 在跑，就跳过；执行期间逐项复查。
独立 GC 锁阻止清理并发，不抢发布锁。执行结果写入 ops/logs/last-run.json。
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
BACKUP_DATA_RE = re.compile(r'\.(?:dump|sql|db|sqlite3?|tar|tgz|gz|xz|bz2|zst|zip|uploading|png|jpe?g|webp|pdf)(?:$|[.-])', re.I)
REF_PATTERNS = [
    re.compile(r'sha256:[0-9a-f]{64}'),
    re.compile(r'''(?m)^\s*image:\s*["']?([^\s"'#]+)'''),
    # 不只 "image"：Codex 的 wemos-web 清单写的是 "rollbackImage"
    re.compile(r'"[A-Za-z_]*[Ii]mage"\s*:\s*"([^"]+)"'),
    re.compile(r'(?m)^[A-Z0-9_]*IMAGE=([^\s#]+)'),
]
LOG_DIR = Path('/srv/wxm/platform/shared/ops/logs')
TMP_ROOT = Path('/tmp')
BUILD_TMP_RE = re.compile(r'^wxm-[a-z0-9-]*(?:ui|candidate|build|release)-[A-Za-z0-9_-]+$')
PATH_RE = re.compile(r'''/(?:srv/wxm|tmp)/[^\s"'\[\]{},;<>]+''')


class CleanupDeferred(Exception):
    pass


def sh(*args: str, env=None) -> str:
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=60, env=env).stdout


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


def overlaps(path: Path, refs: set[str]) -> bool:
    value = path.resolve()
    return any(value.is_relative_to(Path(ref)) or Path(ref).is_relative_to(value) for ref in refs)


def process_references(path: Path, refs: set[str], commands: set[str] | None = None) -> bool:
    # cwd=/ 或 cwd=/tmp 只占用该目录本身，不代表它的每个子目录都被使用。
    # 显式命令参数可能要遍历整树（如 tar 一个父目录），继续双向保护。
    value = path.resolve()
    return any(Path(ref).is_relative_to(value) for ref in refs) or overlaps(path, commands or set())


def container_snapshot() -> list[dict]:
    ids = sh('docker', 'ps', '-aq', '--no-trunc').split()
    return json.loads(sh('docker', 'inspect', *ids)) if ids else []


def container_paths(containers: list[dict]) -> set[str]:
    paths = set()
    for container in containers:
        labels = container['Config'].get('Labels') or {}
        values = [labels.get('com.docker.compose.project.working_dir') or '',
                  *(labels.get('com.docker.compose.project.config_files') or '').split(','),
                  *(mount.get('Source', '') for mount in container.get('Mounts', []))]
        paths.update(str(Path(value).resolve()) for value in values if value.startswith('/'))
    return paths


def _user_bus_env():
    env = os.environ.copy()
    if not env.get('XDG_RUNTIME_DIR') or not env.get('DBUS_SESSION_BUS_ADDRESS'):
        uid = getattr(os, 'getuid', lambda: 0)()
        runtime = Path(f'/run/user/{uid}')
        bus = runtime / 'bus'
        try:
            directory, socket = runtime.lstat(), bus.lstat()
            if (directory.st_uid != uid or not stat.S_ISDIR(directory.st_mode)
                    or socket.st_uid != uid or not stat.S_ISSOCK(socket.st_mode)):
                return None
        except OSError:
            return None
        if not env.get('XDG_RUNTIME_DIR'):
            env['XDG_RUNTIME_DIR'] = f'/run/user/{uid}'
        if not env.get('DBUS_SESSION_BUS_ADDRESS'):
            env['DBUS_SESSION_BUS_ADDRESS'] = f'unix:path=/run/user/{uid}/bus'
    return env


def _user_fdstore_empty() -> bool:
    """每次扫描查询全部已加载 user units；只有 service 具有 FDSTORE 属性。"""
    try:
        env = _user_bus_env()
        if env is None:
            return False
        output = sh('systemctl', '--user', 'show', '*', '--property=Id,NFileDescriptorStore,FileDescriptorStoreMax', env=env)
        if not output.strip():
            return False
        for block in output.strip().split('\n\n'):
            fields = dict(line.split('=', 1) for line in block.splitlines() if line)
            if not fields.get('Id'):
                return False
            if fields['Id'].endswith('.service'):
                if any(int(fields[name]) != 0 for name in ('NFileDescriptorStore', 'FileDescriptorStoreMax')):
                    return False
        return True
    except (OSError, subprocess.SubprocessError, KeyError, ValueError):
        return False


def _process_identity(item: Path):
    ppid = re.search(r'^PPid:\s*(\d+)$', (item / 'status').read_text(), re.M)
    if not ppid:
        raise ValueError('missing PPid')
    return (item.stat().st_uid, (item / 'comm').read_text().strip(),
            [part.decode(errors='replace').strip() for part in (item / 'cmdline').read_bytes().split(b'\0') if part],
            (item / 'cgroup').read_text().strip(), ppid[1])


def _fixed_management_process(item: Path, proc: Path, uid: int, username: str | None, fdstore_empty) -> bool:
    """只认固定管理角色；名字、用户、argv、父链和 cgroup 缺一不可。"""
    try:
        identity = _process_identity(item)
        init_scope = f'0::/user.slice/user-{uid}.slice/user@{uid}.service/init.scope'
        def manager(value):
            owner, comm, argv, cgroup, ppid = value
            if not (owner == uid and comm == 'systemd' and argv == ['/usr/lib/systemd/systemd', '--user']
                    and cgroup == init_scope and ppid == '1'):
                return False
            parent = _process_identity(proc / '1')
            return (parent[0:2] == (0, 'systemd') and parent[2][0:1] == ['/usr/lib/systemd/systemd']
                    and '--system' in parent[2] and parent[3] == '0::/init.scope')
        if manager(identity):
            # user manager 可能持有 FDSTORE 业务文件，不能只因身份可信就忽略。
            return fdstore_empty()
        owner, comm, argv, cgroup, ppid = identity
        if owner == uid and comm == '(sd-pam)' and argv == ['(sd-pam)'] and cgroup == init_scope:
            return manager(_process_identity(proc / ppid)) and fdstore_empty()
        if (username and owner == uid and comm == 'sshd' and argv == [f'sshd: {username}@notty']
                and re.fullmatch(rf'0::/user.slice/user-{uid}\.slice/session-\d+\.scope', cgroup)):
            parent = _process_identity(proc / ppid)
            return (parent[0:3] == (0, 'sshd', [f'sshd: {username} [priv]']) and parent[3] == cgroup)
    except (OSError, ValueError, IndexError):
        pass
    return False


def process_paths(proc: Path = Path('/proc'), container_ids: set[str] | None = None,
                  command_refs: set[str] | None = None) -> tuple[set[str], bool]:
    """所有可读命令行；同用户进程另查 cwd/fd，未知的不可读引用保留所有候选。

    root 容器的挂载另由 docker inspect 保护，不以 root /proc 不可读作为删除许可。
    同 snapshot 的 Docker cgroup 已由全部 Mounts 保护，不把 namespace 路径当宿主路径。
    固定管理角色只有严格身份及
    user service FDSTORE 零值检查通过才豁免 EACCES；可读路径仍全部保护。
    传 command_refs 时将 argv 路径放入该 set，返回值只含 cwd/fd，方便分别保护。
    """
    refs, incomplete = set(), False
    uid = getattr(os, 'getuid', lambda: 0)()
    try:
        import pwd
        username = pwd.getpwuid(uid).pw_name
    except (ImportError, KeyError):
        username = None
    fdstore_state = []  # 只在本轮扫描内复用，不跨扫描缓存。
    def fdstore_empty():
        if not fdstore_state:
            fdstore_state.append(_user_fdstore_empty())
        return fdstore_state[0]
    for item in proc.iterdir():
        if not item.name.isdigit():
            continue
        unreadable = False
        try:
            own = item.stat().st_uid == uid
        except FileNotFoundError:
            continue
        except OSError:
            incomplete = True
            continue
        covered = False
        try:
            for cgroup in (item / 'cgroup').read_text().splitlines():
                match = re.fullmatch(r'0::/system.slice/docker-([0-9a-f]{64})\.scope', cgroup)
                covered |= bool(match and match[1] in (container_ids or set()))
        except OSError:
            pass
        if covered:
            continue
        try:
            command = (item / 'cmdline').read_bytes().decode(errors='replace').replace('\0', ' ')
            target = refs if command_refs is None else command_refs
            target.update(str(Path(p).resolve()) for p in PATH_RE.findall(command))
        except FileNotFoundError:
            continue
        except OSError:
            unreadable = True
        if not own or int(item.name) == os.getpid():
            continue
        links = [item / 'cwd']
        try:
            links.extend((item / 'fd').iterdir())
        except FileNotFoundError:
            if not item.exists():
                continue
            unreadable = True
        except OSError:
            unreadable = True
        for link in links:
            try:
                value = os.readlink(link)
                if value.startswith('/'):
                    refs.add(str(Path(value.removesuffix(' (deleted)')).resolve()))
            except FileNotFoundError:
                continue
            except OSError:
                unreadable = True
        if unreadable:
            if not _fixed_management_process(item, proc, uid, username, fdstore_empty):
                incomplete = True
    return refs, incomplete


def inspect_path(path: Path) -> tuple[tuple[int, int, int, float, int] | None, str | None]:
    """不追随链接；确认整树归本用户、可读取、目录可写且不跨挂载。"""
    try:
        mounts = set()
        if os.name == 'posix':
            for line in Path('/proc/self/mountinfo').read_text().splitlines():
                value = re.sub(r'\\([0-7]{3})', lambda match: chr(int(match[1], 8)), line.split()[4])
                mounts.add(Path(value))
        first = path.lstat()
        if path.is_symlink() or not (stat.S_ISREG(first.st_mode) or stat.S_ISDIR(first.st_mode)):
            return None, '不是普通文件或真实目录'
        newest, size, seen = first.st_mtime, 0, set()
        def entries():
            yield path
            if not stat.S_ISDIR(first.st_mode):
                return
            def walk_error(error):
                raise error
            for base, dirs, files in os.walk(path, followlinks=False, onerror=walk_error):
                for name in dirs + files:
                    yield Path(base) / name
        for entry in entries():
            current = entry.lstat()
            if current.st_uid != getattr(os, 'getuid', lambda: 0)():
                return None, '含非本用户所有的条目'
            if current.st_dev != first.st_dev or entry in mounts or os.path.ismount(entry):
                return None, '包含挂载点'
            if stat.S_ISDIR(current.st_mode) and current.st_mode & 0o700 != 0o700:
                return None, '包含不可读或只读目录'
            newest = max(newest, current.st_mtime)
            key = current.st_dev, current.st_ino
            if key not in seen:
                size += getattr(current, 'st_blocks', (current.st_size + 511) // 512) * 512
                seen.add(key)
        return (first.st_dev, first.st_ino, first.st_mtime_ns, newest, size), None
    except OSError:
        return None, '无法完整检查目录'


def acquire_run_lock(path: Path):
    import fcntl
    stream = path.open('a')
    try:
        fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        stream.close()
        return None
    return stream


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
        except FileNotFoundError:
            continue
        except OSError:
            return f'读不了发布锁：{lock}'
        if (os.major(st.st_dev), os.minor(st.st_dev), st.st_ino) in held:
            return f'发布锁被占用：{lock}'
    result = subprocess.run(['pgrep', '-af', r'docker (build|buildx|compose .*build)|buildctl build'],
                            capture_output=True, text=True)
    if result.returncode not in (0, 1):
        return '读不了构建进程状态'
    procs = result.stdout.splitlines()
    procs = [p for p in procs if 'pgrep' not in p]
    if procs:
        return '有构建在跑'
    return None


def require_quiet() -> None:
    reason = check_quiet()
    if reason:
        raise CleanupDeferred(reason)


def extract_refs(text: str) -> set[str]:
    refs = set()
    for pattern in REF_PATTERNS:
        for match in pattern.finditer(text):
            refs.add(match.group(1) if pattern.groups else match.group(0))
    # JSON 回滚清单还会用 images: {web: "repo:tag"}，值不一定是 digest。
    def walk(value, image_value=False):
        if isinstance(value, str):
            if image_value:
                refs.add(value)
        elif isinstance(value, dict):
            for key, child in value.items():
                walk(child, image_value or key.lower().endswith('image') or key.lower() == 'images')
        elif isinstance(value, list):
            for child in value:
                walk(child, image_value)
    try:
        walk(json.loads(text))
    except json.JSONDecodeError:
        pass
    return refs


def is_manifest(path: Path) -> bool:
    # before-* 也用于数据库备份；绝不把 dump/SQL/压缩包作为文本清单读取。
    return bool(MANIFEST_NAME_RE.search(path.name) and not BACKUP_DATA_RE.search(path.name))


def read_text(path: Path) -> str:
    if path.stat().st_size > 2 * 1024 * 1024:
        raise ValueError(f'清单过大，无法完整确认引用：{path}')
    return path.read_text(errors='ignore')


class Plan:
    def __init__(self, keep: int, min_age_h: float, anchor_days: float):
        self.keep, self.min_age, self.anchor_age = keep, min_age_h * 3600, anchor_days * 86400
        self.now = time.time()
        self.notes: list[str] = []
        self.delete_tags: list[tuple[str, str, str]] = []      # (ref, image id, 原因)
        self.delete_dangling: list[tuple[str, str]] = []
        self.keep_tags: list[tuple[str, str]] = []
        self.delete_paths: list[tuple[str, Path, int, str]] = []  # (类别, 路径, 体积, 原因)
        self.path_states: dict[Path, tuple] = {}
        self.protected_paths: set[str] = set()
        self.results = dict(deleted_tags=0, deleted_images=0, deleted_paths=0,
                            deleted_path_bytes=0, skipped_paths=0, failed=0)
        self.reclaim_images = 0

    # ---------- 镜像 ----------
    def plan_images(self) -> None:
        containers = container_snapshot()
        self.container_ids = {c['Id'] for c in containers}
        self.protected_paths.update(container_paths(containers))
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
            text = read_text(path)
            self.protected_paths.update(str(Path(p).resolve()) for p in PATH_RE.findall(text))
            for ref in extract_refs(text):
                iid = by_ref.get(ref) or by_ref.get(ref.split('@')[-1])
                if iid and iid not in protected:
                    protected[iid] = f'{why}: {path}'

        for f in config_files:
            protect_from(f, '当前配置引用')
        def listdir(d: Path) -> list[Path]:
            try:
                return list(d.iterdir())
            except FileNotFoundError:
                return []

        for d in sorted(live_dirs | {Path(p) for p in EXTRA_CONFIG_DIRS}):
            if not d.is_dir():
                continue
            others = [p for p in listdir(d) if p.is_file() and p not in config_files
                      and is_manifest(p)]
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
                    files = [p for p in listdir(sub) if p.is_file() and is_manifest(p)]
                elif sub.is_file() and is_manifest(sub):
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
        commands = set()
        try:
            refs, incomplete = process_paths(container_ids=getattr(self, 'container_ids', set()), command_refs=commands)
        except OSError:
            refs, incomplete = set(), True
        def referenced(path):
            return overlaps(path, self.protected_paths) or process_references(path, refs, commands)

        def queue(kind: str, path: Path, min_age: int) -> None:
            if incomplete or referenced(path):
                reason = '进程引用检查不完整' if incomplete else '容器、回滚或进程仍引用'
                self.notes.append(f'保留 {path}：{reason}')
                self.results['skipped_paths'] += 1
                return
            state, reason = inspect_path(path)
            if reason:
                self.notes.append(f'保留 {path}：{reason}')
                self.results['skipped_paths'] += 1
                return
            age = self.now - state[3]
            if age >= min_age:
                self.path_states[path] = state
                self.delete_paths.append((kind, path, state[4], f'{age / 86400:.1f} 天未改动'))

        for root in RELEASE_ROOTS:
            base = Path(root)
            if not base.is_dir() or base.is_symlink():
                continue
            subs = [p for p in base.iterdir() if p.is_dir() and not p.is_symlink()]
            protected = {p for p in subs if referenced(p)}
            rest = sorted((p for p in subs if p not in protected), key=lambda p: p.stat().st_mtime, reverse=True)
            for p in rest[self.keep:]:
                queue('发布目录', p, 86400)
        for d in INCOMING_DIRS:
            base = Path(d)
            if not base.is_dir() or base.is_symlink():
                continue
            for p in base.iterdir():
                if p.is_file() and not p.is_symlink() and ARCHIVE_RE.search(p.name):
                    queue('上传包', p, 86400)
        uid = getattr(os, 'getuid', lambda: 0)()
        for p in TMP_ROOT.iterdir():
            try:
                st = p.lstat()
            except OSError:
                continue
            if st.st_uid != uid or not (stat.S_ISREG(st.st_mode) or stat.S_ISDIR(st.st_mode)):
                continue
            min_age = 86400 if stat.S_ISDIR(st.st_mode) and BUILD_TMP_RE.fullmatch(p.name) else 7 * 86400
            queue('/tmp', p, min_age)

    # ---------- 输出 ----------
    def report(self, out) -> None:
        if self.notes:
            print('== 保守保留的文件候选 ==', file=out)
            for note in self.notes:
                print(f'  {note}', file=out)
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
    def require_unchanged_containers(self) -> set[str]:
        require_quiet()
        containers = container_snapshot()
        if {c['Id'] for c in containers} != self.container_ids:
            raise CleanupDeferred('规划后容器集合已变化')
        return container_paths(containers)

    def apply(self, out) -> int:
        self.require_unchanged_containers()
        for ref, iid, _ in self.delete_tags:
            self.require_unchanged_containers()
            r = subprocess.run(['docker', 'image', 'rm', ref], capture_output=True, text=True, timeout=60)
            if r.returncode:
                self.results['failed'] += 1
                print(f'  ! 删 {ref} 失败：{r.stderr.strip()[:200]}', file=out)
            else:
                self.results['deleted_tags'] += 1
        for iid, _ in self.delete_dangling:
            self.require_unchanged_containers()
            r = subprocess.run(['docker', 'image', 'rm', iid], capture_output=True, text=True, timeout=60)
            if r.returncode:
                self.results['failed'] += 1
                print(f'  ! 删 {iid[7:19]} 失败：{r.stderr.strip()[:200]}', file=out)
            else:
                self.results['deleted_images'] += 1
        # 经典构建器的中间镜像是链式的：删掉子镜像，父镜像才变成悬空。一轮清不干净，
        # 循环到不再冒出新的为止（2026-10-09 第一轮删完又露出 3 张、约 3GB）
        tried = {iid for iid, _ in self.delete_dangling}
        for _ in range(5):
            self.require_unchanged_containers()
            fresh = [i for i in sh('docker', 'image', 'ls', '--no-trunc', '-q', '-f', 'dangling=true').split()
                     if i not in tried and i not in self.protected]
            if not fresh:
                break
            for iid in fresh:
                self.require_unchanged_containers()
                tried.add(iid)
                born = parse_time(sh('docker', 'image', 'inspect', '--format', '{{.Created}}', iid).strip())
                if time.time() - born < self.min_age:
                    continue
                r = subprocess.run(['docker', 'image', 'rm', iid], capture_output=True, text=True, timeout=60)
                if r.returncode == 0:
                    self.results['deleted_images'] += 1
                    print(f'  删除新露出的悬空镜像 {iid[7:19]}', file=out)
                else:
                    self.results['failed'] += 1
                    print(f'  ! 删新露出镜像 {iid[7:19]} 失败：{r.stderr.strip()[:200]}', file=out)
        self.require_unchanged_containers()
        # 不带 -a 保留仍被镜像引用的基础层；磁盘压力不能自动扩大清理范围。
        r = subprocess.run(['docker', 'builder', 'prune', '-f', '--filter', 'until=24h'],
                           capture_output=True, text=True, timeout=120)
        if r.returncode:
            self.results['failed'] += 1
            print(f'  ! 构建缓存清理失败：{r.stderr.strip()[:200]}', file=out)
        else:
            print(f'  构建缓存（24 小时未用且不被镜像引用的）：{(r.stdout.strip().splitlines() or ["?"])[-1]}', file=out)
        allowed = {Path(p).absolute() for p in RELEASE_ROOTS + INCOMING_DIRS} | {TMP_ROOT.absolute()}
        for kind, p, size, _ in self.delete_paths:
            refs = self.require_unchanged_containers() | self.protected_paths
            real = p.resolve()
            if p.parent.absolute() not in allowed or real != p.absolute() or p.is_symlink():
                self.results['failed'] += 1
                print(f'  ! 跳过 {p}：不在允许的目录里', file=out)
                continue
            commands = set()
            process_refs, incomplete = process_paths(container_ids=self.container_ids, command_refs=commands)
            if incomplete or overlaps(p, refs) or process_references(p, process_refs, commands):
                self.results['skipped_paths'] += 1
                print(f'  保留 {p}：当前仍被引用或引用检查不完整', file=out)
                continue
            state, reason = inspect_path(p)
            if reason or state != self.path_states.get(p):
                self.results['skipped_paths'] += 1
                print(f'  保留 {p}：{reason or "规划后目录状态已变化"}', file=out)
                continue
            try:
                require_quiet()
                if p.is_dir():
                    shutil.rmtree(p)
                else:
                    p.unlink()
                self.results['deleted_paths'] += 1
                self.results['deleted_path_bytes'] += size
            except OSError as e:
                self.results['failed'] += 1
                print(f'  ! 删 {p} 失败：{e}', file=out)
        print(f'  失败 {self.results["failed"]} 项，保守保留 {self.results["skipped_paths"]} 项', file=out)
        return self.results['failed']


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument('--apply', action='store_true', help='真的删（默认只预演）')
    ap.add_argument('--keep', type=int, default=2, help='每条镜像线 / 发布目录额外保留的版本数')
    ap.add_argument('--min-age-hours', type=float, default=2)
    ap.add_argument('--anchor-max-days', type=float, default=14)
    args = ap.parse_args()
    if args.keep < 2 or args.min_age_hours < 0 or args.anchor_max_days < 0:
        ap.error('keep 至少为 2，时间边界不能为负数')

    stamp = dt.datetime.now().strftime('%Y%m%dT%H%M%S')
    out = Tee(sys.stdout)
    lock = None
    if args.apply:
        LOG_DIR.mkdir(parents=True, exist_ok=True)
        lock = acquire_run_lock(LOG_DIR.parent / 'retention.lock')
        if lock is None:
            print(f'# {stamp} 跳过：另一个垃圾回收进程正在运行', file=out)
            return 0
        out.files.append((LOG_DIR / f'retention-{stamp}-{os.getpid()}.log').open('w'))
    state = {'startedAt': dt.datetime.now(dt.timezone.utc).isoformat(), 'status': 'failed', 'reason': ''}
    code, plan = 0, None
    used0, free0 = disk_free()
    try:
        require_quiet()
        if args.apply:
            for old in LOG_DIR.glob('retention-*.log'):
                if time.time() - old.stat().st_mtime > 30 * 86400:
                    old.unlink(missing_ok=True)
        plan = Plan(args.keep, args.min_age_hours, args.anchor_max_days)
        plan.plan_images()
        plan.plan_files()
        print(f'# {stamp} keep={args.keep} apply={args.apply} 磁盘 已用 {human(used0)} 剩余 {human(free0)}', file=out)
        plan.report(out)
        if args.apply:
            print('== 执行 ==', file=out)
            code = 1 if plan.apply(out) else 0
        state['status'] = 'failed' if code else ('completed_with_skips' if plan.results['skipped_paths'] else 'success')
        if code:
            state['reason'] = f'清理失败 {plan.results["failed"]} 项'
    except CleanupDeferred as error:
        state.update(status='skipped', reason=str(error))
        print(f'# {stamp} 跳过：{error}', file=out)
        if plan and plan.results['failed']:
            state['status'], code = 'failed', 1
    except Exception as error:
        state.update(status='failed', reason=f'{type(error).__name__}: {error}')
        if plan:
            plan.results['failed'] += 1
        code = 1
        print(f'  ! 垃圾回收失败：{state["reason"]}', file=out)
    finally:
        _, free1 = disk_free()
        state.update(finishedAt=dt.datetime.now(dt.timezone.utc).isoformat(),
                     counts=plan.results if plan else {'failed': code}, diskFreeBeforeBytes=free0,
                     diskFreeAfterBytes=free1, diskFreeChangeBytes=free1 - free0)
        if args.apply:
            print(f'  磁盘 剩余 {human(free0)} -> {human(free1)}（变化 {human(free1 - free0)}）', file=out)
            try:
                latest = LOG_DIR / 'last-run.json'
                try:
                    previous = json.loads(latest.read_text())
                except (OSError, ValueError):
                    previous = {}
                state['runCount'] = int(previous.get('runCount', 0)) + 1
                temporary = LOG_DIR / f'.last-run-{os.getpid()}.tmp'
                temporary.write_text(json.dumps(state, ensure_ascii=False, indent=2) + '\n')
                temporary.replace(latest)
            except (OSError, ValueError) as error:
                print(f'  ! 写入回收状态失败：{error}', file=out)
                code = 1
        out.close()
        if lock:
            lock.close()
    return code


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
