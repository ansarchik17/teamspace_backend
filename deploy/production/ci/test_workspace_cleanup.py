import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import workspace_cleanup as cleanup


class CleanupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        self.runner = self.base / 'runner'
        self.runner.mkdir()
        (self.runner / '.runner').write_text(json.dumps({'gitHubUrl':
            'https://github.com/west6ide/workspace-front'}))
        self.install = self.runner / '_work/workspace-front/workspace-front/node_modules'
        self.install.mkdir(parents=True)
        (self.install.parent / 'package-lock.json').write_text('{}')
        (self.install / 'dependency.js').write_text('dependency')
        self.source = self.install.parent / 'app.js'
        self.source.write_text('source')
        self.cache = self.base / '.npm'
        self.cache.mkdir()
        (self.cache / 'download').write_text('cached archive')
        self.policy = unittest.mock.Mock()
        self.policy.containers.return_value = []
        for patcher in [patch.object(cleanup, 'TARGETS', {'workspace-front':
                           (str(self.runner), ('node_modules',))}),
                        patch.object(cleanup, 'protected_paths', return_value=[]),
                        patch.object(cleanup.reclaim, 'process_snapshot', return_value={}),
                        patch.object(cleanup.reclaim, 'worker_states', return_value={}),
                        patch.object(cleanup.shutil, 'disk_usage', return_value=
                                     unittest.mock.Mock(free=0))]:
            patcher.start()
            self.addCleanup(patcher.stop)

    def test_reclaims_only_reproducible_install_and_preserves_source_and_downloads(self):
        result = cleanup.recover(self.policy, apply=True)
        self.assertEqual(result['removed'], [str(self.install)])
        self.assertFalse(self.install.exists())
        self.assertEqual(self.source.read_text(), 'source')
        self.assertEqual((self.cache / 'download').read_text(), 'cached archive')

    def test_plan_does_not_remove_files(self):
        result = cleanup.recover(self.policy)
        self.assertEqual(result['candidates'], [str(self.install)])
        self.assertTrue(self.install.exists())

    def test_rejects_symlink_and_mounted_data(self):
        with self.assertRaises(RuntimeError):
            cleanup.validate(self.install, [self.install.parent])
        link = self.install.parent / 'linked'
        link.symlink_to(self.install, target_is_directory=True)
        with self.assertRaises(RuntimeError):
            cleanup.validate(link, [])

    def test_rejects_wrong_registration(self):
        (self.runner / '.runner').write_text(json.dumps({'gitHubUrl': 'https://example.com/other'}))
        with self.assertRaises(RuntimeError):
            cleanup.recover(self.policy, apply=True)
        self.assertTrue(self.install.exists())

    def test_rejects_install_without_lockfile(self):
        (self.install.parent / 'package-lock.json').unlink()
        with self.assertRaises(RuntimeError):
            cleanup.recover(self.policy, apply=True)

    def test_active_worker_prevents_cleanup(self):
        with patch.object(cleanup.reclaim, 'worker_states', return_value={1: 'active'}):
            result = cleanup.recover(self.policy, apply=True)
        self.assertEqual(result['status'], 'skipped_active_worker')
        self.assertTrue(self.install.exists())

    def test_worker_arriving_after_plan_prevents_deletion(self):
        with patch.object(cleanup.reclaim, 'worker_states', side_effect=[{}, {1: 'active'}]):
            result = cleanup.recover(self.policy, apply=True)
        self.assertTrue(result['interrupted_by_worker'])
        self.assertTrue(self.install.exists())


if __name__ == '__main__':
    unittest.main()
