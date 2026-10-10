"""Run with python deploy/test_wxm_host_retention.py."""
import importlib.util
from pathlib import Path
import unittest

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
    def test_recognizes_every_manifest_format_on_the_host(self):
        digest = 'sha256:' + 'a' * 64
        refs = retention.extract_refs(
            '{"rollbackImage": "wxm-tenant-rollback:web-e578c6411e36", "images": {"web": "%s"}}\n'
            'services:\n  sub2api:\n    image: sub2api:0.1.165-wxm2-x-20261009-813cd15e1\n'
            'BUILDER_IMAGE=sha256:%s\n' % (digest, 'b' * 64))
        self.assertTrue({'wxm-tenant-rollback:web-e578c6411e36', digest,
                         'sub2api:0.1.165-wxm2-x-20261009-813cd15e1',
                         'sha256:' + 'b' * 64} <= refs)


if __name__ == '__main__':
    unittest.main()
