from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import resource_gate


class AdmissionTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.proc = Path(self.directory.name)
        (self.proc / 'pressure').mkdir()
        (self.proc / 'pressure/memory').write_text('some avg10=0.00 avg60=0.00 avg300=0.00 total=0\n')
        (self.proc / '42').mkdir()
        (self.proc / '42/comm').write_text('Runner.Worker\n')
        self.memory(2432)
        self.rss(128)
        disk = patch.object(resource_gate.shutil, 'disk_usage', return_value=type('Disk', (), {'free': 40 * 2**30})())
        disk.start(); self.addCleanup(disk.stop)

    def memory(self, mib):
        (self.proc / 'meminfo').write_text(f'MemAvailable: {mib * 1024} kB\n')

    def rss(self, mib):
        (self.proc / '42/status').write_text(f'VmRSS: {mib * 1024} kB\n')

    def check(self, worker=42):
        return resource_gate.admission_reason(proc=self.proc, worker_pid=worker)

    def test_exact_total_budget(self):
        self.assertEqual(self.check(), '')
        self.memory(2431)
        self.assertIn('memory budget', self.check())

    def test_credit_never_exceeds_128_mib(self):
        self.rss(1024)
        self.memory(2431)
        self.assertIn('memory budget', self.check())

    def test_credit_cannot_exceed_real_rss(self):
        self.rss(32)
        self.assertIn('memory budget', self.check())
        self.memory(2528)
        self.assertEqual(self.check(), '')

    def test_no_worker_no_credit(self):
        self.assertIn('memory budget', self.check(None))
        self.memory(2560)
        self.assertEqual(self.check(None), '')

    def test_identity_and_missing_probes_fail_closed(self):
        (self.proc / '42/comm').write_text('unrelated\n')
        self.assertIn('identity unavailable', self.check())
        (self.proc / '42/comm').unlink()
        self.assertIn('probes unavailable', self.check())

    def test_invalid_rss_fails_closed(self):
        self.rss(-1)
        self.assertIn('memory unavailable', self.check())
        (self.proc / '42/status').write_text('VmRSS: invalid kB\n')
        self.assertIn('probes unavailable', self.check())

    def test_pressure_still_blocks(self):
        (self.proc / 'pressure/memory').write_text('some avg10=5.00 total=0\n')
        self.assertIn('pressure', self.check())

    def test_disk_still_blocks(self):
        with patch.object(resource_gate.shutil, 'disk_usage', return_value=type('Disk', (), {'free': 29 * 2**30})()):
            self.assertIn('disk free', self.check())

    def test_swap_cannot_substitute_ram(self):
        self.memory(2431)
        with (self.proc / 'meminfo').open('a') as stream: stream.write('SwapFree: 999999999 kB\n')
        self.assertIn('memory budget', self.check())


class ExplicitBudgetTests(unittest.TestCase):
    def test_a_larger_explicit_job_budget_is_still_enforced(self):
        with tempfile.TemporaryDirectory() as folder:
            proc = Path(folder)
            (proc / 'pressure').mkdir()
            (proc / 'pressure/memory').write_text('some avg10=0.00 total=0\n')
            (proc / 'meminfo').write_text('MemAvailable: 2867200 kB\n')
            with patch.object(resource_gate.shutil, 'disk_usage', return_value=type('Disk', (), {'free': 40 * 2**30})()):
                self.assertEqual(resource_gate.admission_reason(proc=proc), '')
                self.assertIn('memory budget', resource_gate.admission_reason(proc=proc, minimum_mib=3072))
