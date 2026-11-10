import concurrent.futures
import contextlib
import io
import threading
import fcntl
import importlib.util
import os
import signal
import sys
from unittest.mock import patch
from pathlib import Path
import subprocess
import tempfile
import time
import unittest

@unittest.skipUnless(Path('/proc/self/stat').exists(), 'Linux process identities required')
class JobLockTests(unittest.TestCase):
    def setUp(self):
        # Test process lifetime/locking independently of host pressure and the
        # production Runner.Worker identity check. Resource admission has its own
        # fail-closed tests; only this disposable copy uses a deterministic gate.
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        source = Path(directory.name) / 'job-lock.py'
        self.source = source
        source.write_bytes(Path(__file__).with_name('job-lock.py').read_bytes())
        (source.parent / 'resource_gate.py').write_text(
            'def admission_reason(*args, **kwargs):\n    return ""\n')
        spec = importlib.util.spec_from_file_location('isolated_job_lock', source)
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)

    def test_serializes_jobs_and_releases_after_cancel(self):
        with tempfile.TemporaryDirectory() as directory:
            lock = Path(directory) / 'job.lock'
            lock.touch()
            first = subprocess.Popen(['sleep', '30'])
            second = subprocess.Popen(['sleep', '30'])
            try:
                self.module.acquire(first.pid, str(lock), timeout=5)
                with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
                    waiting = pool.submit(self.module.acquire, second.pid, str(lock), 5)
                    time.sleep(0.4)
                    self.assertFalse(waiting.done(), 'Second job started before first ended')
                    first.terminate(); first.wait(timeout=3)
                    waiting.result(timeout=3)
                second.kill(); second.wait(timeout=3)
                time.sleep(0.5)
                with lock.open('r+') as stream:
                    fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
            finally:
                for process in (first, second):
                    if process.poll() is None: process.kill(); process.wait()

    def test_dead_worker_does_not_hold_slot(self):
        process = subprocess.Popen(['true']); process.wait()
        with self.assertRaises(RuntimeError): self.module.acquire(process.pid)

    def test_wait_timeout_releases_watcher(self):
        with tempfile.TemporaryDirectory() as directory:
            lock = Path(directory) / 'job.lock'; lock.touch()
            worker = subprocess.Popen(['sleep', '30'])
            try:
                with lock.open('r+') as stream:
                    fcntl.flock(stream, fcntl.LOCK_EX)
                    with self.assertRaises(RuntimeError): self.module.acquire(worker.pid, str(lock), 0.2)
                with lock.open('r+') as stream:
                    fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
            finally:
                worker.kill(); worker.wait()

    def exercise_resource_wait(self, pass_first_probe=False):
        flag = self.source.parent / 'resource-available'
        count = self.source.parent / 'probes'
        self.source.with_name('resource_gate.py').write_text(
            'from pathlib import Path\n'
            'def admission_reason(*args, **kwargs):\n'
            f'    count = Path({str(count)!r})\n'
            '    n = int(count.read_text()) + 1 if count.exists() else 1\n'
            '    count.write_text(str(n))\n'
            f'    if {pass_first_probe!r} and n == 1: return ""\n'
            f'    return "" if Path({str(flag)!r}).exists() else "disk free < 30 GiB"\n')
        lock = self.source.parent / 'job.lock'
        lock.touch()
        worker = subprocess.Popen(['sleep', '30'])
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
                waiting = pool.submit(self.module.acquire, worker.pid, str(lock), 6)
                deadline = time.monotonic() + 2
                minimum_probes = 2 if pass_first_probe else 1
                while (not count.exists() or int(count.read_text() or '0') < minimum_probes):
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(0.02)
                time.sleep(0.1)
                self.assertFalse(waiting.done(), 'A resource-blocked job was admitted')
                with lock.open('r+') as maintenance:
                    fcntl.flock(maintenance, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    # Recovery makes resources sufficient, but the job must still
                    # wait for maintenance to finish before launching steps.
                    flag.touch()
                    time.sleep(0.3)
                    self.assertFalse(waiting.done(), 'Job raced cache maintenance')
                waiting.result(timeout=3)
                with lock.open('r+') as other:
                    with self.assertRaises(BlockingIOError):
                        fcntl.flock(other, fcntl.LOCK_EX | fcntl.LOCK_NB)
            worker.terminate(); worker.wait(timeout=3)
            time.sleep(0.4)
            with lock.open('r+') as maintenance:
                fcntl.flock(maintenance, fcntl.LOCK_EX | fcntl.LOCK_NB)
        finally:
            if worker.poll() is None: worker.kill(); worker.wait()

    def test_resource_blocked_job_leaves_slot_available_for_recovery(self):
        self.exercise_resource_wait()

    def test_resources_are_rechecked_under_lock_and_failed_admission_yields(self):
        self.exercise_resource_wait(pass_first_probe=True)

    def test_resource_wait_timeout_leaves_slot_available(self):
        self.source.with_name('resource_gate.py').write_text(
            'def admission_reason(*args, **kwargs): return "disk free < 30 GiB"\n')
        lock = self.source.parent / 'job.lock'; lock.touch()
        worker = subprocess.Popen(['sleep', '30'])
        try:
            with self.assertRaises(RuntimeError):
                self.module.acquire(worker.pid, str(lock), 0.2)
            with lock.open('r+') as maintenance:
                fcntl.flock(maintenance, fcntl.LOCK_EX | fcntl.LOCK_NB)
        finally:
            worker.kill(); worker.wait()

    def test_execution_deadline_kills_worker_and_children_before_unlocking(self):
        child_file = self.source.parent / 'child.pid'
        lock = self.source.parent / 'job.lock'; lock.touch()
        code = ('import pathlib,subprocess,time; '
                'child=subprocess.Popen(["sleep","30"]); '
                f'pathlib.Path({str(child_file)!r}).write_text(str(child.pid)); '
                'time.sleep(30)')
        worker = subprocess.Popen([sys.executable, '-c', code])
        unrelated = subprocess.Popen(['sleep', '30'])
        try:
            deadline = time.monotonic() + 3
            while not child_file.exists():
                self.assertLess(time.monotonic(), deadline)
                time.sleep(.02)
            child = int(child_file.read_text())
            self.module.acquire(worker.pid, str(lock), 3, execution_timeout=.7)
            worker.wait(timeout=4)
            self.assertEqual(worker.returncode, -signal.SIGKILL)
            deadline = time.monotonic() + 3
            while self.module.identity(child) is not None:
                self.assertLess(time.monotonic(), deadline)
                time.sleep(.02)
            self.assertIsNone(unrelated.poll(), 'Cleanup killed a process outside the worker tree')
            deadline = time.monotonic() + 3
            while True:
                try:
                    with lock.open('r+') as stream:
                        fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(.02)
        finally:
            for process in (worker, unrelated):
                if process.poll() is None: process.kill(); process.wait()

    def test_execution_deadline_excludes_queue_wait(self):
        lock = self.source.parent / 'job.lock'; lock.touch()
        worker = subprocess.Popen(['sleep', '30'])
        try:
            with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
                with lock.open('r+') as stream:
                    fcntl.flock(stream, fcntl.LOCK_EX)
                    waiting = pool.submit(self.module.acquire, worker.pid, str(lock), 4, .7)
                    time.sleep(1)
                    self.assertIsNone(worker.poll())
                    self.assertFalse(waiting.done())
                waiting.result(timeout=2)
                self.assertIsNone(worker.poll(), 'Queue wait consumed the execution allowance')
                worker.wait(timeout=3)
                self.assertEqual(worker.returncode, -signal.SIGKILL)
        finally:
            if worker.poll() is None: worker.kill(); worker.wait()

    def test_pid_reuse_and_scope_change_are_never_signalled(self):
        worker = subprocess.Popen(['sleep', '30'])
        try:
            stamp = self.module.identity(worker.pid)
            group = self.module.scope(worker.pid)
            with patch.object(self.module.os, 'pidfd_open') as open_fd:
                self.module.signal_same(worker.pid, stamp + 'x', group, signal.SIGKILL)
                self.module.signal_same(worker.pid, stamp, ('wrong-group', 99999), signal.SIGKILL)
                open_fd.assert_not_called()
            with patch.object(self.module, 'identity', side_effect=[stamp, 'reused']), \
                 patch.object(self.module.os, 'pidfd_open', return_value=99), \
                 patch.object(self.module.os, 'close') as close, \
                 patch.object(self.module.signal, 'pidfd_send_signal') as send:
                self.module.signal_same(worker.pid, stamp, group, signal.SIGKILL)
                send.assert_not_called()
                close.assert_called_once_with(99)
            self.assertIsNone(worker.poll())
        finally:
            worker.kill(); worker.wait()

    def test_invalid_active_limits_fail_before_spawning_a_watcher(self):
        for limit in (0, -1, float('nan'), float('inf'), self.module.EXECUTION_TIMEOUT + 1):
            with self.assertRaises(ValueError), patch.object(self.module.subprocess, 'Popen') as spawn:
                self.module.acquire(os.getpid(), execution_timeout=limit)
            spawn.assert_not_called()

    def test_previous_watcher_cli_remains_compatible_during_atomic_update(self):
        lock = self.source.parent / 'job.lock'; lock.touch()
        worker = subprocess.Popen(['sleep', '30'])
        reader, writer = os.pipe()
        watcher = subprocess.Popen([sys.executable, str(self.source), '--watch', str(worker.pid),
                                    self.module.identity(worker.pid), str(lock), str(writer),
                                    str(time.monotonic() + 3)], pass_fds=(writer,))
        os.close(writer)
        try:
            self.assertEqual(os.read(reader, 64), b'ready\n')
            worker.terminate(); worker.wait(timeout=3)
            self.assertEqual(watcher.wait(timeout=3), 0)
        finally:
            os.close(reader)
            for process in (worker, watcher):
                if process.poll() is None: process.kill(); process.wait()


class AdmissionLogTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location('admission_log_test', Path(__file__).with_name('job-lock.py'))
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)
        self.reader, self.writer = os.pipe()
        self.addCleanup(os.close, self.reader)
        self.addCleanup(os.close, self.writer)

    def test_already_ready_does_not_report_false_wait(self):
        os.write(self.writer, b'ready\n')
        output = io.StringIO()
        with contextlib.redirect_stdout(output), patch.object(self.module, 'admission_reason') as gate:
            self.module.wait_for_admission(self.reader, 42, .5)
        gate.assert_not_called()
        self.assertEqual(output.getvalue(), '')

    def test_wait_reason_repeats_and_success_still_requires_ready(self):
        timer = threading.Timer(.08, lambda: os.write(self.writer, b'ready\n'))
        timer.start()
        self.addCleanup(timer.join)
        output = io.StringIO()
        with contextlib.redirect_stdout(output), patch.object(self.module, 'admission_reason', return_value='CI memory budget 2300 MiB < 2560 MiB'):
            self.module.wait_for_admission(self.reader, 42, 1, report_interval=.02)
        self.assertGreaterEqual(output.getvalue().count('CI memory budget'), 2)
        self.assertIn('remaining', output.getvalue())

    def test_timeout_reports_memory_reason(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output), patch.object(self.module, 'admission_reason', return_value='CI memory budget insufficient'):
            with self.assertRaisesRegex(RuntimeError, 'memory budget'):
                self.module.wait_for_admission(self.reader, 42, .03, report_interval=.02)
        self.assertIn('memory budget', output.getvalue())

    def test_slot_wait_is_distinct_from_resource_wait(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output), patch.object(self.module, 'admission_reason', return_value=''):
            with self.assertRaisesRegex(RuntimeError, 'shared CI slot'):
                self.module.wait_for_admission(self.reader, 42, .02)
        self.assertIn('shared CI slot is occupied', output.getvalue())

    def test_unexpected_message_never_grants_admission(self):
        os.write(self.writer, b'not-ready\n')
        with self.assertRaisesRegex(RuntimeError, 'before admission'):
            self.module.wait_for_admission(self.reader, 42, .1)
