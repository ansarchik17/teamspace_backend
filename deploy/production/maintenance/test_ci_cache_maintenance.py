import importlib.util
from pathlib import Path
import os
import json
import tempfile
import fcntl
import threading
import time
import unittest
import types
from unittest.mock import patch

source = Path(__file__).with_name('ci-cache-maintenance.py')
if not source.exists():
    source = Path('/usr/local/sbin/workspace-ci-cache.py')
spec = importlib.util.spec_from_file_location('cache', source)
cache = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cache)


class PolicyTests(unittest.TestCase):
    def test_missing_release_protects_every_existing_image_during_cache_only_cleanup(self):
        images = [{'Id': 'current'}, {'Id': 'rollback'}, {'Id': 'untagged'}]
        retention = types.SimpleNamespace(snapshot=lambda root: (images, [], {'current'}, [], set(), ['absent-old-rollback']))
        spec = types.SimpleNamespace(loader=types.SimpleNamespace(exec_module=lambda module: None))
        with patch.object(cache.importlib.util, 'spec_from_file_location', return_value=spec), \
             patch.object(cache.importlib.util, 'module_from_spec', return_value=retention), \
             patch.object(cache.Path, 'read_text', return_value=json.dumps({'root': '/opt'})):
            self.assertEqual(cache.protected_images(), {'current', 'rollback', 'untagged'})

    def test_busy_slot_is_never_stolen(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'job.lock'
            with path.open('w+') as job, path.open('r+') as maintenance:
                fcntl.flock(job, fcntl.LOCK_EX)
                self.assertFalse(cache.acquire_ci_slot(maintenance, 0))
                self.assertFalse(cache.acquire_ci_slot(maintenance, 0.02))
                fcntl.flock(job, fcntl.LOCK_UN)
                self.assertTrue(cache.acquire_ci_slot(maintenance, 0))

    def test_waiting_maintenance_can_run_after_job_releases_slot(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'job.lock'
            with path.open('w+') as job, path.open('r+') as maintenance:
                fcntl.flock(job, fcntl.LOCK_EX)
                def finish_job():
                    time.sleep(0.02)
                    fcntl.flock(job, fcntl.LOCK_UN)
                thread = threading.Thread(target=finish_job)
                thread.start()
                try:
                    self.assertTrue(cache.acquire_ci_slot(maintenance, 0.1))
                finally:
                    thread.join()

    def test_unknown_worker_blocks_maintenance_with_queued_workers(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            runner = root / 'runner'
            binary = runner / 'bin' / 'Runner.Worker'
            binary.parent.mkdir(parents=True)
            binary.touch()
            proc = root / 'proc' / '123'
            proc.mkdir(parents=True)
            (proc / 'comm').write_text('Runner.Worker\n')
            (proc / 'exe').symlink_to(binary)
            with patch.object(cache, 'RUNNERS', [runner]):
                cache.verify_managed_workers(root / 'proc')
                (proc / 'exe').unlink()
                outside = root / 'unknown-worker'
                outside.touch()
                (proc / 'exe').symlink_to(outside)
                with self.assertRaises(RuntimeError):
                    cache.verify_managed_workers(root / 'proc')

    def test_budget_removes_oldest_but_protects_recent_cache(self):
        now = 20 * 86400
        rows = [{'name': str(days), 'bytes': 4, 'last_write': now - days * 86400}
                for days in (0.5, 2, 4)]
        self.assertEqual([x['name'] for x in cache.orphan_plan(rows, now, budget=8)], ['4'])
        self.assertEqual([x['name'] for x in cache.orphan_plan(rows[:1], now, budget=0)], [])

    def test_week_old_orphans_expire_even_below_budget(self):
        rows = [{'name': 'old', 'bytes': 1, 'last_write': 0}]
        self.assertEqual(cache.orphan_plan(rows, 8 * 86400, budget=8), rows)

    def test_attached_and_unknown_volumes_are_never_candidates(self):
        known = next(iter(cache.CACHE_NAMES))
        self.assertIsNone(cache.cache_volume({'Name': known}, {known}, 0))
        self.assertIsNone(cache.cache_volume({'Name': 'shared_infra_postgres_data'}, set(), 0))

    def test_remote_and_custom_storage_is_excluded(self):
        known = next(iter(cache.CACHE_NAMES))
        for properties in ({'Driver': 'nfs'}, {'Driver': 'local', 'Options': {'device': '/data'}}):
            self.assertIsNone(cache.cache_volume({'Name': known, **properties}, set(), 0))

    def test_temp_cleanup_does_not_follow_symlink_or_remove_recent_file(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            temp = root / '_temp'
            temp.mkdir()
            old, recent, outside = temp / 'old', temp / 'recent', root / 'production'
            for p in (old, recent, outside):
                p.write_text('keep')
            os.utime(old, (0, 0))
            os.utime(outside, (0, 0))
            (temp / 'link').symlink_to(outside)
            result = cache.files_to_expire(temp, 7, 10 * 86400)
            self.assertEqual(result, [old])
            (root / 'linked_temp').symlink_to(temp, target_is_directory=True)
            self.assertEqual(cache.files_to_expire(root / 'linked_temp', 7, 10 * 86400), [])

    def test_only_old_log_files_expire_in_diag(self):
        with tempfile.TemporaryDirectory() as directory:
            folder = Path(directory).resolve() / '_diag'
            folder.mkdir()
            for name in ('Worker.log', 'settings.json'):
                p = folder / name
                p.write_text('data')
                os.utime(p, (0, 0))
            self.assertEqual(cache.files_to_expire(folder, 14, 20 * 86400), [folder / 'Worker.log'])

    def test_go_toolchain_selection_uses_numeric_version(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for version in ('1.9.1', '1.24.7', '1.25.7'):
                binary = root / '_work/_tool/go' / version / 'x64/bin/go'
                binary.parent.mkdir(parents=True)
                binary.touch()
            with patch.object(cache, 'RUNNERS', [root]), patch.object(
                    cache, 'executable_as_actions', side_effect=lambda p: '1.25.7' not in p.parts):
                self.assertEqual(cache.go_binary(), str(root / '_work/_tool/go/1.24.7/x64/bin/go'))

    def test_unlocked_runner_blocks_cache_mutations(self):
        with tempfile.TemporaryDirectory() as directory:
            runner = Path(directory)
            (runner / '.runner').write_text(json.dumps({'gitHubUrl': 'https://github.com/west6ide/iform'}))
            (runner / '.env').write_text('ACTIONS_RUNNER_HOOK_JOB_STARTED=/opt/workspace-ci/job-started.sh\n')
            with patch.object(cache, 'RUNNERS', [runner]):
                self.assertEqual(cache.verify_ci_hooks(), 1)
                (runner / '.env').write_text('')
                with self.assertRaises(RuntimeError):
                    cache.verify_ci_hooks()

    def test_go_cache_keeps_recent_entries_and_unknown_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            entries = []
            for prefix, age in [('aa', 1), ('bb', 2), ('cc', 3)]:
                folder = root / prefix
                folder.mkdir()
                p = folder / (prefix * 32 + '-d')
                p.write_bytes(b'compiled')
                os.utime(p, (age, age))
                entries.append(p)
            unknown = root / 'aa' / 'application.txt'
            unknown.write_text('preserve')
            outside = root / 'outside'
            outside.write_text('preserve')
            (root / 'aa' / ('ad' * 32 + '-d')).symlink_to(outside)
            size = sum(p.stat().st_blocks * 512 for p in entries)
            removed = cache.trim_go_cache(root, size, budget=size - entries[0].stat().st_blocks * 512)
            self.assertEqual(removed, 1)
            self.assertFalse(entries[0].exists())
            self.assertTrue(all(p.exists() for p in entries[1:] + [unknown, outside]))

    def test_go_cache_below_budget_is_unchanged(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            self.assertEqual(cache.trim_go_cache(root, 1, budget=2), 0)

    def test_go_cache_rejects_symlink_root(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            (root / 'link').symlink_to(root, target_is_directory=True)
            with self.assertRaises(RuntimeError):
                cache.trim_go_cache(root / 'link', 100, budget=0)


if __name__ == '__main__':
    unittest.main()
