"""Run with python deploy/test_wxm_host_retention.py."""
import importlib.util
import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch, Mock

spec = importlib.util.spec_from_file_location('retention', Path(__file__).with_name('wxm-host-retention.py'))
retention = importlib.util.module_from_spec(spec)
spec.loader.exec_module(retention)

DAY = 86400
NOW = 1000 * DAY


def decide(rows, protected=(), in_use=('live',), keep=2):
    """rows: [(镜像 ID, repo, tag, 几天前创建, 层链末端)] -> {ref: 是否删除}"""
    images, created, top = {}, {}, {}
    for iid, repo, tag, days, chain in rows:
        images.setdefault(iid, []).append((repo, tag))
        created[iid], top[iid] = NOW - days * DAY, chain
    guard = {iid: '容器在用' for iid in in_use}
    guard.update({iid: '最新备份' for iid in protected})
    result = retention.classify_tags(images, created, top, guard, set(in_use),
                                     keep, 2 * 3600, 14 * DAY, NOW)
    return {ref: drop for ref, _, drop, _ in result}


class ClassifyTagsTest(unittest.TestCase):
    def test_tags_identical_to_running_image_do_not_use_up_rollback_slots(self):
        # 2026-10-09 租户：后端没变的几次发布产生了与线上层链一模一样的 rollback tag。
        # 它们不能占掉名额，真正不同的上一版必须留下
        got = decide([
            ('live', 'wxm-tenant-prod-backend', 'latest', 1, 'L'),
            ('same1', 'wxm-tenant-rollback', 'backend-aaaaaaaaaaaa', 1, 'L'),
            ('same2', 'wxm-tenant-rollback', 'backend-bbbbbbbbbbbb', 1, 'L'),
            ('prev1', 'wxm-tenant-rollback', 'backend-cccccccccccc', 2, 'P1'),
            ('prev2', 'wxm-tenant-rollback', 'backend-dddddddddddd', 3, 'P2'),
            ('prev3', 'wxm-tenant-rollback', 'backend-eeeeeeeeeeee', 4, 'P3'),
        ], protected={'same1'})
        self.assertFalse(got['wxm-tenant-prod-backend:latest'])
        self.assertFalse(got['wxm-tenant-rollback:backend-aaaaaaaaaaaa'])
        self.assertTrue(got['wxm-tenant-rollback:backend-bbbbbbbbbbbb'])
        self.assertFalse(got['wxm-tenant-rollback:backend-cccccccccccc'])
        self.assertFalse(got['wxm-tenant-rollback:backend-dddddddddddd'])
        self.assertTrue(got['wxm-tenant-rollback:backend-eeeeeeeeeeee'])

    def test_versions_kept_by_backups_count_toward_the_limit(self):
        got = decide([
            ('live', 'sub2api', 'v5', 0.5, 'L'),
            ('b1', 'sub2api', 'v4', 1, 'B1'),
            ('b2', 'sub2api', 'v3', 2, 'B2'),
            ('x3', 'sub2api', 'v2', 3, 'X3'),
        ], protected={'b1', 'b2'})
        self.assertEqual(got, {'sub2api:v5': False, 'sub2api:v4': False,
                               'sub2api:v3': False, 'sub2api:v2': True})

    def test_old_and_fresh_boundaries(self):
        got = decide([
            ('live', 'wxm-identity-prod-api', 'latest', 1, 'L'),
            ('fresh', 'wxm-identity-prod-api', 'pre-a', 1 / 24, 'F'),
            ('old', 'wxm-identity-prod-api', 'pre-b', 20, 'O'),
        ])
        self.assertFalse(got['wxm-identity-prod-api:pre-a'])  # 不到 2 小时，可能是正在发布的
        self.assertTrue(got['wxm-identity-prod-api:pre-b'])   # 超过 14 天不再算回滚锚点

    def test_rollback_repos_are_counted_per_service_and_base_images_skipped(self):
        got = decide([
            ('live', 'wxm-platform-prod-api', 'latest', 1, 'L'),
            ('a1', 'wxm-platform-rollback', 'api-111111111111', 2, 'A1'),
            ('a2', 'wxm-platform-rollback', 'api-222222222222', 3, 'A2'),
            ('w1', 'wxm-platform-rollback', 'web-111111111111', 4, 'W1'),
            ('w2', 'wxm-platform-rollback', 'web-222222222222', 5, 'W2'),
            ('base', 'docker.m.daocloud.io/library/alpine', '3.21', 300, 'BASE'),
        ])
        # api 和 web 各自留两个，不会因为 api 版本较新就把 web 挤掉
        self.assertFalse(any(got[r] for r in [
            'wxm-platform-rollback:api-111111111111', 'wxm-platform-rollback:api-222222222222',
            'wxm-platform-rollback:web-111111111111', 'wxm-platform-rollback:web-222222222222']))
        self.assertNotIn('docker.m.daocloud.io/library/alpine:3.21', got)


class HeldLocksTest(unittest.TestCase):
    def test_reads_holders_and_waiters_from_proc_locks(self):
        text = ('1: FLOCK  ADVISORY  WRITE 3801770 fd:03:2753597 0 EOF\n'
                '2: POSIX  ADVISORY  READ 1234 00:1a:99 0 EOF\n'
                '2: -> FLOCK  ADVISORY  WRITE 3801999 fd:03:2753597 0 EOF\n')
        self.assertEqual(retention.held_locks(text), {(0xfd, 3, 2753597), (0, 0x1a, 99)})
        self.assertEqual(retention.held_locks(''), set())


class ExtractRefsTest(unittest.TestCase):
    def test_json_images_maps_and_nested_lists_protect_tag_strings(self):
        refs = retention.extract_refs(json.dumps({'images': {'web': 'wxm-web:rollback-old',
              'nested': [{'api': 'wxm-api:rollback-old'}]},
              'details': {'rollbackImage': 'wxm-worker:rollback-old'}}))
        self.assertTrue({'wxm-web:rollback-old', 'wxm-api:rollback-old', 'wxm-worker:rollback-old'} <= refs)

    def test_database_backup_names_are_not_text_manifests(self):
        for name in ['tenant-before-261896f5e6e16d255539dad0e7dc16882f6bcdf5-20260930T134540Z.dump',
                     'tenant-before-sha.sql.gz', 'runtime-before.tar.uploading', 'rollback.zip']:
            self.assertFalse(retention.is_manifest(Path(name)), name)
        for name in ['runtime-before.json', '.env.production', 'docker-compose.yml.bak-release', 'rollback.yaml']:
            self.assertTrue(retention.is_manifest(Path(name)), name)

    def test_recognizes_every_manifest_format_on_the_host(self):
        digest = 'sha256:' + 'a' * 64
        refs = retention.extract_refs(
            '{"rollbackImage": "wxm-tenant-rollback:web-e578c6411e36", "images": {"web": "%s"}}\n'
            'services:\n  sub2api:\n    image: sub2api:0.1.165-wxm2-x-20261009-813cd15e1\n'
            'BUILDER_IMAGE=sha256:%s\n' % (digest, 'b' * 64))
        self.assertTrue({'wxm-tenant-rollback:web-e578c6411e36', digest,
                         'sub2api:0.1.165-wxm2-x-20261009-813cd15e1',
                         'sha256:' + 'b' * 64} <= refs)


class FilePlanTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.releases, self.incoming, self.tmp = [self.root / name for name in ('releases', 'incoming', 'tmp')]
        for directory in (self.releases, self.incoming, self.tmp):
            directory.mkdir()
        for name, value in [('RELEASE_ROOTS', [str(self.releases)]), ('INCOMING_DIRS', [str(self.incoming)]),
                            ('TMP_ROOT', self.tmp)]:
            mock = patch.object(retention, name, value)
            mock.start()
            self.addCleanup(mock.stop)
        self.processes = patch.object(retention, 'process_paths', return_value=(set(), False)).start()
        self.addCleanup(patch.stopall)

    def folder(self, parent, name, days):
        folder = parent / name
        folder.mkdir()
        content = folder / 'data'
        content.write_text('fixture')
        stamp = time.time() - days * DAY
        os.utime(content, (stamp, stamp))
        os.utime(folder, (stamp, stamp))
        return folder

    def planned(self, protected=()):
        plan = retention.Plan(2, 2, 14)
        plan.protected_paths = {str(Path(path).resolve()) for path in protected}
        plan.plan_files()
        return plan, {path for _, path, _, _ in plan.delete_paths}

    def test_known_completed_builds_use_one_day_and_other_temp_files_use_seven(self):
        build = self.folder(self.tmp, 'wxm-account-security-ui-abcdef-fixture', 2)
        ordinary = self.folder(self.tmp, 'other-work', 2)
        old = self.folder(self.tmp, 'old-work', 8)
        fresh = self.folder(self.tmp, 'wxm-tenant-candidate-abcdef-fixture', 0.5)
        _, paths = self.planned()
        self.assertEqual(paths, {build, old})
        self.assertTrue(ordinary.exists() and fresh.exists())

    def test_nested_new_file_prevents_old_directory_being_deleted(self):
        build = self.folder(self.tmp, 'wxm-tenant-build-abcdef-fixture', 2)
        (build / 'data').write_text('still building')
        self.assertNotIn(build, self.planned()[1])

    def test_process_parent_cwd_does_not_protect_all_descendants_but_open_child_does(self):
        build = self.folder(self.tmp, 'wxm-tenant-build-fixture', 2)
        self.processes.return_value = ({str(self.tmp.resolve()), str(Path('/').resolve())}, False)
        self.assertIn(build, self.planned()[1])
        self.processes.return_value = ({str((build / 'data').resolve())}, False)
        self.assertNotIn(build, self.planned()[1])
        self.processes.return_value = (set(), False)
        self.assertNotIn(build, self.planned(protected={self.tmp})[1])

    def test_explicit_tar_parent_argument_protects_child_archive(self):
        archive = self.incoming / 'fixture.tar.gz'
        archive.write_text('fixture')
        os.utime(archive, (time.time() - 2 * DAY,) * 2)
        def processes(**kwargs):
            kwargs['command_refs'].add(str(self.incoming.resolve()))
            return {str(Path('/').resolve())}, False
        self.processes.side_effect = processes
        self.assertNotIn(archive, self.planned()[1])

    def test_mount_and_process_references_and_incomplete_scan_preserve_candidates(self):
        build = self.folder(self.tmp, 'wxm-tenant-build-abcdef-fixture', 2)
        self.assertNotIn(build, self.planned([build / 'data'])[1])
        self.processes.return_value = ({str((build / 'data').resolve())}, False)
        self.assertNotIn(build, self.planned()[1])
        self.processes.return_value = (set(), True)
        plan, paths = self.planned()
        self.assertNotIn(build, paths)
        self.assertTrue(any('引用检查不完整' in note for note in plan.notes))

    def test_release_reference_kept_separately_from_two_recent_rollback_directories(self):
        newest = self.folder(self.releases, 'newest', 2)
        previous = self.folder(self.releases, 'previous', 3)
        old = self.folder(self.releases, 'old', 4)
        mounted = self.folder(self.releases, 'mounted', 5)
        self.assertEqual(self.planned([mounted / 'data'])[1], {old})
        self.assertTrue(newest.exists() and previous.exists() and mounted.exists())

    def test_readonly_directory_and_mixed_owner_are_preserved_without_permission_changes(self):
        build = self.folder(self.tmp, 'wxm-tenant-build-abcdef-fixture', 2)
        actual = Path.lstat
        def mixed(path):
            result = actual(path)
            if path == build / 'data':
                return SimpleNamespace(st_uid=result.st_uid + 1)
            return result
        with patch.object(Path, 'lstat', mixed):
            plan, paths = self.planned()
            self.assertNotIn(build, paths)
            self.assertTrue(any('非本用户' in note for note in plan.notes))
        if os.name == 'posix':
            build.chmod(0o500)
            try:
                self.assertIn('只读', retention.inspect_path(build)[1])
            finally:
                build.chmod(0o700)

    def test_mount_point_is_not_traversed(self):
        build = self.folder(self.tmp, 'wxm-tenant-build-abcdef-fixture', 2)
        with patch.object(retention.os.path, 'ismount', side_effect=lambda path: path == build / 'data'):
            self.assertEqual(retention.inspect_path(build)[1], '包含挂载点')


class ContainerReferencesTest(unittest.TestCase):
    def test_only_inspected_full_docker_cgroup_ids_cover_hidden_references(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            item = root / '123456789'
            item.mkdir()
            iid = 'a' * 64
            (item / 'cmdline').write_bytes(b'fixture\0/tmp/wxm-tenant-build-held\0')
            (item / 'cgroup').write_text(f'0::/system.slice/docker-{iid}.scope\n')
            (item / 'fd').mkdir()
            with patch.object(retention.os, 'readlink', side_effect=PermissionError('hidden')):
                refs, incomplete = retention.process_paths(root, {iid})
                self.assertFalse(incomplete)
                self.assertEqual(refs, set())  # 容器 namespace 路径不能作为宿主绝对路径。
                self.assertTrue(retention.process_paths(root, {'b' * 64})[1])

    def test_cron_bus_defaults_require_current_uid_owned_runtime_and_socket(self):
        uid = getattr(os, 'getuid', lambda: 0)()
        def info(path):
            return SimpleNamespace(st_uid=uid, st_mode=(0o140000 if path.name == 'bus' else 0o040000))
        with patch.dict(os.environ, {}, clear=True), patch.object(Path, 'lstat', info):
            env = retention._user_bus_env()
            self.assertEqual(env['XDG_RUNTIME_DIR'], f'/run/user/{uid}')
            self.assertEqual(env['DBUS_SESSION_BUS_ADDRESS'], f'unix:path=/run/user/{uid}/bus')
        for mode, owner in [(0o140000, uid + 1), (0o120000, uid), (0o100000, uid)]:
            with patch.dict(os.environ, {}, clear=True), patch.object(Path, 'lstat', return_value=SimpleNamespace(st_uid=owner, st_mode=mode)):
                self.assertIsNone(retention._user_bus_env())

    def test_management_exceptions_require_exact_identity_parent_and_fdstore(self):
        identities = {
            '1': (0, 'systemd', ['/usr/lib/systemd/systemd', '--system'], '0::/init.scope', '0'),
            '10': (1000, 'systemd', ['/usr/lib/systemd/systemd', '--user'],
                   '0::/user.slice/user-1000.slice/user@1000.service/init.scope', '1'),
            '11': (1000, '(sd-pam)', ['(sd-pam)'],
                   '0::/user.slice/user-1000.slice/user@1000.service/init.scope', '10'),
            '20': (0, 'sshd', ['sshd: fixture [priv]'],
                   '0::/user.slice/user-1000.slice/session-123.scope', '1'),
            '21': (1000, 'sshd', ['sshd: fixture@notty'],
                   '0::/user.slice/user-1000.slice/session-123.scope', '20')}
        with patch.object(retention, '_process_identity', side_effect=lambda item: identities[item.name]):
            for pid in ['10', '11', '21']:
                self.assertTrue(retention._fixed_management_process(Path(pid), Path('.'), 1000, 'fixture', lambda: True))
            for pid in ['10', '11']:
                self.assertFalse(retention._fixed_management_process(Path(pid), Path('.'), 1000, 'fixture', lambda: False))
            for replacement in [
                    (1000, 'sshd', ['sshd: fixture@internal-sftp'], identities['21'][3], '20'),
                    (1000, 'sshd', ['sshd: fixture@pts/0'], identities['21'][3], '20'),
                    (1000, 'sshd', ['sshd: fixture@notty payload'], identities['21'][3], '20'),
                    (1000, 'sshd', ['sshd: fixture@notty'], identities['21'][3], '10'),
                    (1000, 'sshd', ['sshd: fixture@notty'], identities['21'][3].replace('123', '999'), '20')]:
                identities['21'] = replacement
                self.assertFalse(retention._fixed_management_process(Path('21'), Path('.'), 1000, 'fixture', lambda: True))

    def test_fdstore_query_requires_complete_zero_fields_for_every_loaded_service(self):
        for text, result in [
                ('Id=fixture.mount\n\nId=fixture.device\n', True),
                ('Id=fixture.service\nNFileDescriptorStore=0\nFileDescriptorStoreMax=0\n', True),
                ('Id=fixture.service\nNFileDescriptorStore=1\nFileDescriptorStoreMax=1\n', False),
                ('Id=fixture.service\nFileDescriptorStoreMax=0\n', False),
                ('Id=fixture.service\nNFileDescriptorStore=unknown\nFileDescriptorStoreMax=0\n', False),
                ('', False)]:
            with self.subTest(text=text), patch.object(retention, '_user_bus_env', return_value={}), patch.object(retention, 'sh', return_value=text):
                self.assertEqual(retention._user_fdstore_empty(), result)
        with patch.object(retention, '_user_bus_env', return_value={}), patch.object(retention, 'sh', side_effect=subprocess.CalledProcessError(1, 'systemctl')):
            self.assertFalse(retention._user_fdstore_empty())

    def test_management_exception_keeps_all_readable_process_paths(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            item = root / '123456789'
            item.mkdir()
            (item / 'cmdline').write_bytes(b'fixture\0/tmp/wxm-tenant-build-held\0')
            (item / 'fd').mkdir()
            (item / 'fd' / '1').touch()
            def link(path):
                if path.name == 'cwd':
                    raise PermissionError('hidden cwd')
                return '/tmp/wxm-tenant-build-open-fd'
            with patch.object(retention.os, 'readlink', side_effect=link), \
                 patch.object(retention, '_fixed_management_process', return_value=True):
                commands = set()
                refs, incomplete = retention.process_paths(root, command_refs=commands)
            self.assertFalse(incomplete)
            self.assertTrue(retention.overlaps(Path('/tmp/wxm-tenant-build-open-fd'), refs))
            self.assertTrue(retention.overlaps(Path('/tmp/wxm-tenant-build-held'), commands))
            self.assertFalse(retention.overlaps(Path('/tmp/wxm-tenant-build-held'), refs))

    def test_stopped_containers_also_protect_bind_mounts_and_compose_paths(self):
        containers = [{'State': {'Status': 'exited'}, 'Config': {'Labels': {
            'com.docker.compose.project.working_dir': '/srv/wxm/tenant/releases/old',
            'com.docker.compose.project.config_files': '/srv/wxm/tenant/releases/old/compose.yml'}},
            'Mounts': [{'Source': '/tmp/wxm-tenant-build-fixture/data'}]}]
        refs = retention.container_paths(containers)
        self.assertTrue(retention.overlaps(Path('/srv/wxm/tenant/releases/old'), refs))
        self.assertTrue(retention.overlaps(Path('/tmp/wxm-tenant-build-fixture'), refs))

    def test_unreadable_same_user_process_marks_reference_scan_incomplete(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            process = root / '123456789'
            process.mkdir()
            (process / 'cmdline').write_bytes(b'fixture')
            with patch.object(Path, 'read_bytes', side_effect=PermissionError('fixture permission')):
                refs, incomplete = retention.process_paths(root)
            self.assertEqual(refs, set())
            self.assertTrue(incomplete)


class ApplyTest(unittest.TestCase):
    def setUp(self):
        self.plan = retention.Plan(2, 2, 14)
        self.plan.container_ids = set()
        self.plan.protected = {}
        self.quiet = patch.object(retention, 'require_quiet').start()
        self.snapshot = patch.object(retention, 'container_snapshot', return_value=[]).start()
        self.sh = patch.object(retention, 'sh', return_value='').start()
        self.run = patch.object(retention.subprocess, 'run', return_value=SimpleNamespace(
            returncode=0, stdout='Total reclaimed space: 0B\n', stderr='')).start()
        patch.object(retention, 'process_paths', return_value=(set(), False)).start()
        self.addCleanup(patch.stopall)
        self.output = io.StringIO()

    def test_builder_never_uses_all_flag_even_when_disk_is_full(self):
        with patch.object(retention, 'disk_free', return_value=(99, 1)):
            self.assertEqual(self.plan.apply(self.output), 0)
        self.assertEqual(self.run.call_args.args[0], ['docker', 'builder', 'prune', '-f', '--filter', 'until=24h'])

    def test_builder_failure_is_counted(self):
        self.run.return_value = SimpleNamespace(returncode=9, stdout='', stderr='builder unavailable')
        self.assertEqual(self.plan.apply(self.output), 1)
        self.assertIn('builder unavailable', self.output.getvalue())

    def test_fresh_dangling_failure_is_not_silently_ignored(self):
        self.sh.side_effect = lambda *args: ('2000-01-01T00:00:00Z' if args[:3] == ('docker', 'image', 'inspect') else 'sha256:fresh')
        self.run.side_effect = lambda args, **kwargs: SimpleNamespace(
            returncode=8 if args[:3] == ['docker', 'image', 'rm'] else 0,
            stdout='', stderr='fresh removal failed')
        self.assertEqual(self.plan.apply(self.output), 1)
        self.assertIn('fresh removal failed', self.output.getvalue())

    def test_new_deployment_stops_before_delete(self):
        self.plan.delete_tags = [('wxm-app:old', 'old', 'expired')]
        self.quiet.side_effect = [None, retention.CleanupDeferred('deployment started')]
        with self.assertRaises(retention.CleanupDeferred):
            self.plan.apply(self.output)
        self.run.assert_not_called()

    def test_new_container_stops_cleanup(self):
        self.snapshot.return_value = [{'Id': 'new'}]
        with self.assertRaises(retention.CleanupDeferred):
            self.plan.apply(self.output)
        self.run.assert_not_called()

    def test_container_snapshot_is_checked_before_each_tag_and_dangling_delete(self):
        for tags, dangling, fresh in [
                ([('wxm-app:old', 'old', 'expired')], [], ''),
                ([], [('sha256:old', 'expired')], ''),
                ([], [], 'sha256:fresh')]:
            with self.subTest(tags=tags, dangling=dangling, fresh=fresh):
                self.plan.delete_tags, self.plan.delete_dangling = tags, dangling
                # 规划时和 apply 开头相同，下一次逐项删除前发现新容器。
                self.snapshot.side_effect = ([[], [], [{'Id': 'new'}]] if fresh else [[], [{'Id': 'new'}]])
                self.run.reset_mock()
                self.sh.return_value = fresh
                with self.assertRaises(retention.CleanupDeferred):
                    self.plan.apply(self.output)
                self.run.assert_not_called()

    def test_second_tag_is_preserved_when_container_changes_after_first_delete(self):
        self.plan.delete_tags = [('wxm-app:one', 'one', 'expired'), ('wxm-app:two', 'two', 'expired')]
        self.snapshot.side_effect = [[], [], [{'Id': 'new'}]]
        with self.assertRaises(retention.CleanupDeferred):
            self.plan.apply(self.output)
        self.assertEqual(self.run.call_count, 1)
        self.assertEqual(self.run.call_args.args[0], ['docker', 'image', 'rm', 'wxm-app:one'])

    def test_outside_allowed_directory_is_not_deleted(self):
        with tempfile.TemporaryDirectory() as directory:
            outside = Path(directory) / 'important'
            outside.write_text('keep')
            self.plan.delete_paths = [('fixture', outside, 4, 'old')]
            self.assertEqual(self.plan.apply(self.output), 1)
            self.assertTrue(outside.exists())

    def test_changed_file_is_preserved_and_unchanged_file_is_deleted(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(retention, 'TMP_ROOT', Path(directory)):
            path = Path(directory) / 'fixture'
            path.write_text('before')
            self.plan.path_states[path] = retention.inspect_path(path)[0]
            self.plan.delete_paths = [('fixture', path, 512, 'old')]
            path.write_text('changed fixture content')
            self.assertEqual(self.plan.apply(self.output), 0)
            self.assertTrue(path.exists())
            self.assertEqual(self.plan.results['skipped_paths'], 1)
            self.plan.path_states[path] = retention.inspect_path(path)[0]
            self.assertEqual(self.plan.apply(self.output), 0)
            self.assertFalse(path.exists())
            self.assertEqual(self.plan.results['deleted_paths'], 1)


class RunStatusTest(unittest.TestCase):
    def test_failed_apply_returns_nonzero_and_writes_atomic_state_and_run_count(self):
        with tempfile.TemporaryDirectory() as directory:
            plan = Mock()
            plan.results = dict(failed=1, skipped_paths=0, deleted_paths=0)
            plan.apply.return_value = 1
            with patch.object(retention, 'LOG_DIR', Path(directory)), patch.object(retention, 'Plan', return_value=plan), \
                 patch.object(retention, 'acquire_run_lock', side_effect=lambda path: io.StringIO()), \
                 patch.object(retention, 'require_quiet'), patch.object(retention, 'disk_free', return_value=(90, 10)), \
                 patch.object(sys, 'argv', ['retention', '--apply']), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(retention.main(), 1)
                self.assertEqual(retention.main(), 1)
            state = json.loads((Path(directory) / 'last-run.json').read_text())
            self.assertEqual((state['status'], state['runCount'], state['counts']['failed']), ('failed', 2, 1))
            self.assertEqual(list(Path(directory).glob('.last-run-*.tmp')), [])

    def test_deployment_skip_is_recorded_without_claiming_success(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(retention, 'LOG_DIR', Path(directory)), \
             patch.object(retention, 'acquire_run_lock', return_value=io.StringIO()), \
             patch.object(retention, 'require_quiet', side_effect=retention.CleanupDeferred('deployment active')), \
             patch.object(retention, 'disk_free', return_value=(90, 10)), \
             patch.object(sys, 'argv', ['retention', '--apply']), contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(retention.main(), 0)
            state = json.loads((Path(directory) / 'last-run.json').read_text())
            self.assertEqual((state['status'], state['reason']), ('skipped', 'deployment active'))


@unittest.skipUnless(os.name == 'posix', 'Linux flock/proc/shell acceptance')
class LinuxSafetyTest(unittest.TestCase):
    def test_selected_backup_manifest_protects_its_release_path(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            backup = root / 'backups' / 'latest'
            backup.mkdir(parents=True)
            release = root / 'wxm-tenant-release-old'
            release.mkdir()
            image = 'sha256:' + 'a' * 64
            (backup / 'rollback.json').write_text(json.dumps({'image': image, 'source': str(release)}))
            dump = root / 'backups' / 'tenant-before-261896f5e6e16d255539dad0e7dc16882f6bcdf5-20260930T134540Z.dump'
            dump.write_bytes(b'\0' * (2 * 1024 * 1024 + 1))
            def docker(*args):
                if args[:3] == ('docker', 'image', 'ls'):
                    return '' if 'dangling=true' in args else f'{image}\twxm-fixture\tlatest'
                if args[:3] == ('docker', 'image', 'inspect'):
                    return json.dumps([{'Id': image, 'RootFS': {'Layers': ['layer']},
                                        'Created': '2000-01-01T00:00:00Z', 'RepoDigests': []}])
                self.fail(f'unexpected docker command {args}')
            with patch.object(retention, 'container_snapshot', return_value=[{
                'Id': 'fixture', 'Image': image, 'Config': {'Labels': {}}, 'Mounts': []}]), \
                 patch.object(retention, 'sh', side_effect=docker), \
                 patch.object(retention, 'BACKUP_ROOTS', [str(root / 'backups')]), \
                 patch.object(retention, 'EXTRA_CONFIG_DIRS', []), \
                 patch.object(retention, 'COMPOSE_BAK_GLOB', (str(root), 'absent-*')):
                plan = retention.Plan(2, 2, 14)
                plan.plan_images()
            self.assertTrue(retention.overlaps(release, plan.protected_paths))
            self.assertTrue(dump.exists())

    def test_release_lock_covers_failed_health_and_restore_and_is_released_before_gc(self):
        source = Path(__file__).with_name('wxm-release.sh').read_text()
        helper = source[source.index('with_deploy_lock() {'):source.index('\n# 回滚候选')]
        switch = source.split("<<'SWITCH'\n", 1)[1].split('\nSWITCH', 1)[0]
        rollback = source.split("<<'ROLLBACK'\n", 1)[1].split('\nROLLBACK', 1)[0]
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'docker-compose.yml').write_text('services:\n  app:\n    image: sub2api:old\n')
            shell = '''set -euo pipefail
STACK=$1
export STACK
ssh_do() { bash -c "$1"; }
sleep() { :; }
docker() {
  if flock -n "$STACK/media-deploy.lock" true; then
    echo LOCK_MISSING >>"$STACK/events"; return 90
  fi
  printf '%s\\n' "$*" >>"$STACK/events"
  case "$*" in
    'compose up -d')
      count=$(cat "$STACK/up-count" 2>/dev/null || echo 0)
      echo $((count+1)) >"$STACK/up-count";;
    'inspect -f '*)
      count=$(cat "$STACK/up-count")
      if [ "$count" -eq 1 ]; then echo running/unhealthy; else echo running/healthy; fi;;
    'image inspect '*) return 0;;
    'ps '*) return 0;;
    *) return 91;;
  esac
}
export -f docker sleep
''' + helper + '''
set +e
with_deploy_lock "$STACK" sub2api:new docker-compose.yml.bak-fixture <<'SWITCH'
''' + switch + '''
SWITCH
result=$?
set -e
[ "$result" -ne 0 ]
grep -q 'image: sub2api:old' "$STACK/docker-compose.yml"
[ "$(cat "$STACK/up-count")" -eq 2 ]
flock -n "$STACK/media-deploy.lock" true
echo gc-after-unlock >>"$STACK/events"
printf 'services:\\n  app:\\n    image: sub2api:rollback\\n' >"$STACK/docker-compose.yml.bak-rollback"
with_deploy_lock "$STACK" docker-compose.yml.bak-rollback 'docker-compose.yml.bak-* backups/*/docker-compose.yml' <<'ROLLBACK'
''' + rollback + '''
ROLLBACK
flock -n "$STACK/media-deploy.lock" true
grep -q 'image: sub2api:rollback' "$STACK/docker-compose.yml"
'''
            result = subprocess.run(['bash', '-c', shell, 'fixture', str(root)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            events = (root / 'events').read_text()
            self.assertNotIn('LOCK_MISSING', events)
            self.assertEqual(events.count('compose up -d'), 3)
            self.assertIn('gc-after-unlock', events)
            self.assertIn('自动回滚', result.stderr)
            self.assertLess(source.index('\nSWITCH\n'), source.index('\nprune_build_cache\n'))

    def test_inner_symlink_is_not_followed_or_counted_as_target_data(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = root / 'keep'
            target.mkdir()
            (target / 'data').write_bytes(b'x' * 1024 * 1024)
            candidate = root / 'wxm-tenant-build-fixture'
            candidate.mkdir()
            (candidate / 'link').symlink_to(target, target_is_directory=True)
            state, reason = retention.inspect_path(candidate)
            self.assertIsNone(reason)
            self.assertLess(state[4], 1024 * 1024)
            self.assertTrue((target / 'data').exists())

    def test_real_concurrent_process_cannot_acquire_gc_lock(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'retention.lock'
            first = retention.acquire_run_lock(path)
            try:
                child = subprocess.run([sys.executable, '-c',
                    'import fcntl,sys; f=open(sys.argv[1],"a"); fcntl.flock(f,fcntl.LOCK_EX|fcntl.LOCK_NB)', str(path)],
                    capture_output=True, text=True)
                self.assertNotEqual(child.returncode, 0)
                self.assertIn('BlockingIOError', child.stderr)
            finally:
                first.close()
            with retention.acquire_run_lock(path):
                pass

    def test_real_process_cwd_and_open_fd_are_preserved(self):
        with tempfile.TemporaryDirectory(prefix='wxm-test-candidate-') as directory:
            path = Path(directory)
            child = subprocess.Popen([sys.executable, '-c',
                'import os,sys,time; f=open("held","w"); print("ready",flush=True); time.sleep(30)'],
                cwd=path, stdout=subprocess.PIPE, text=True)
            try:
                self.assertEqual(child.stdout.readline().strip(), 'ready')
                refs, _ = retention.process_paths()
                self.assertTrue(retention.overlaps(path, refs))
                self.assertIn(str((path / 'held').resolve()), refs)
            finally:
                child.terminate()
                child.wait(timeout=5)
                child.stdout.close()

    def test_release_retention_failure_remains_visible_and_no_unprotected_fallback_prune(self):
        source = Path(__file__).with_name('wxm-release.sh').read_text()
        body = source[source.index('prune_build_cache() {'):source.index('\ncurrent_image()')]
        shell = '''set -euo pipefail
RETENTION=/fixture/retention.py
disk_used_percent() { echo 99; }
ssh_do() { case "$*" in
  "test -f "*) return 0;;
  "python3 "*) echo 'fixture retention failed'; return 17;;
  *) return 0;;
esac; }
'''+body+'\nprune_build_cache\n'
        result = subprocess.run(['bash', '-c', shell], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0)
        self.assertIn('fixture retention failed', result.stdout)
        self.assertIn('保留脚本执行失败', result.stdout)
        self.assertNotIn('docker builder prune -a', source)


if __name__ == '__main__':
    unittest.main()
