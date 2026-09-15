"""Run with python deploy/test_verify_windhub_target.py."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('gate', Path(__file__).with_name('verify-windhub-target.py'))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class TargetGateTest(unittest.TestCase):
    def test_accept_and_reject_each_identity_mismatch(self):
        nginx = 'map $host $backend_url { windhub.online http://sub2api:8080; } server { server_name windhub.online; location / { proxy_pass $backend_url; } }'
        args = ['wxm-tenant-platform-hz-01', ['192.0.2.1'], '192.0.2.1',
                {'Image': 'sha256:known', 'Config': {'Image': 'sub2api:release-123456789'}},
                'sha256:known', '123456789abcd', nginx]
        self.assertTrue(all(gate.validate(*args).values()))
        for index, bad in [(0, 'legacy'), (1, ['192.0.2.2']), (4, 'sha256:other'),
                           (5, '987654321abcd'), (6, nginx.replace('sub2api:8080', 'other:8080')),
                           (6, nginx.replace('server_name windhub.online;', 'server_name other;')),
                           (6, nginx.replace('proxy_pass $backend_url;', 'proxy_pass http://other;'))]:
            values = args.copy()
            values[index] = bad
            with self.assertRaises(RuntimeError):
                gate.validate(*values)


if __name__ == '__main__':
    unittest.main()
