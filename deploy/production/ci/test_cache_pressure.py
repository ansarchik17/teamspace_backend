import tempfile
import unittest
from pathlib import Path
from unittest.mock import Mock, patch
import cache_pressure as pressure
import reclaim_memory as reclaim
import workspace_cleanup


class DiskPressureTests(unittest.TestCase):
    def test_low_disk_with_no_reclaimable_layers_recovers_disposable_installs(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'job.lock').touch()
            (root / 'backups').mkdir()
            policy = Mock()
            policy.RUNNERS = {'known': 'runner'}
            policy.verify_ci_hooks.return_value = 1
            policy.containers.return_value = []
            policy.command.return_value = ''
            with patch.object(pressure, 'ROOT', root), \
                 patch.object(pressure.shutil, 'disk_usage', return_value=Mock(free=0)), \
                 patch.object(pressure, 'load_policy', return_value=policy), \
                 patch.object(pressure, 'candidates', return_value=[]), \
                 patch.object(reclaim, 'process_snapshot', return_value={}), \
                 patch.object(reclaim, 'worker_states', return_value={}), \
                 patch.object(workspace_cleanup, 'recover', return_value={'status': 'completed'}) as cleanup:
                result = pressure.recover(True)
            cleanup.assert_called_once_with(policy, target_free_bytes=pressure.TARGET, apply=True)
            self.assertEqual(result['workspace_installs']['status'], 'completed')

    def test_healthy_disk_does_not_inspect_or_mutate_caches(self):
        with patch.object(pressure.shutil, 'disk_usage', return_value=Mock(free=pressure.TRIGGER)), \
             patch.object(pressure, 'load_policy') as policy:
            self.assertEqual(pressure.recover(True)['status'], 'sufficient_disk')
            policy.assert_not_called()

    def test_only_known_reproducible_caches_are_candidates(self):
        rows = [{'name': 'production-postgres', 'bytes': 1000},
                {'name': 'buildx_buildkit_ci-workspace-backend0_state', 'bytes': 20},
                {'name': 'buildx_buildkit_ci-iform0_state', 'bytes': 10},
                {'name': 'buildx_buildkit_ci-unknown0_state', 'bytes': 2000}]
        self.assertEqual([r['bytes'] for r in pressure.candidates(rows, 0)], [20, 10])

    def test_healthy_disk_never_has_a_prune_plan(self):
        rows = [{'name': 'buildx_buildkit_ci-iform0_state', 'bytes': 1000}]
        self.assertEqual(pressure.candidates(rows, pressure.TRIGGER), [])

    def test_busy_lease_prevents_cache_access(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'job.lock').touch()
            with patch.object(pressure, 'ROOT', root), \
                 patch.object(pressure.shutil, 'disk_usage', return_value=Mock(free=0)), \
                 patch.object(pressure.fcntl, 'flock', side_effect=BlockingIOError), \
                 patch.object(pressure, 'load_policy') as policy:
                self.assertEqual(pressure.recover(True)['status'], 'skipped_busy')
                policy.assert_not_called()

    def test_active_worker_prevents_cache_access(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'job.lock').touch()
            with patch.object(pressure, 'ROOT', root), \
                 patch.object(pressure.shutil, 'disk_usage', return_value=Mock(free=0)), \
                 patch.object(reclaim, 'process_snapshot', return_value={}), \
                 patch.object(reclaim, 'worker_states', return_value={10: 'active'}), \
                 patch.object(pressure, 'load_policy') as policy:
                self.assertEqual(pressure.recover(True)['status'], 'skipped_active_worker')
                policy.assert_not_called()

    def test_new_worker_during_plan_skips_pruning_without_failure(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / 'job.lock').touch()
            (root / 'backups').mkdir()
            policy = Mock()
            policy.RUNNERS = {'known': 'runner'}
            policy.verify_ci_hooks.return_value = 1
            policy.containers.return_value = []
            policy.command.return_value = ''
            with patch.object(pressure, 'ROOT', root), \
                 patch.object(pressure.shutil, 'disk_usage', return_value=Mock(free=0)), \
                 patch.object(pressure, 'load_policy', return_value=policy), \
                 patch.object(pressure, 'candidates', return_value=[{'name': 'unused'}]), \
                 patch.object(reclaim, 'process_snapshot', return_value={}), \
                 patch.object(reclaim, 'worker_states', side_effect=[{}, {10: 'active'}]):
                result = pressure.recover(True)
            self.assertTrue(result['interrupted_by_worker'])
            self.assertEqual(result['builders'], [])
            self.assertTrue(result['containers_unchanged'])


if __name__ == '__main__':
    unittest.main()
