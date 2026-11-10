#!/usr/bin/env python3
"""Hold one host-wide CI slot for the lifetime of a GitHub Runner.Worker."""
import fcntl
import math
import os
from pathlib import Path
import select
import signal
import syslog
import subprocess
import sys
import time
from resource_gate import admission_reason

LOCK = '/opt/workspace-ci/job.lock'
# Waiting for the shared VPS is separate from the admitted job's runtime.
EXECUTION_TIMEOUT = 2 * 60 * 60


def identity(pid):
    try:
        # Field 22 is the start time; ')' may occur inside the command name.
        fields = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
        if fields[0] == 'Z':
            return None
        return fields[19]
    except (FileNotFoundError, ProcessLookupError):
        return None


def scope(pid):
    try:
        return Path(f'/proc/{pid}/cgroup').read_text()
    except (FileNotFoundError, ProcessLookupError):
        return None


def descendants(pid, expected, expected_scope, known):
    """Track only this worker's tree; retain children if they become orphans."""
    parents = {p for p, stamp in known.items() if identity(p) == stamp and scope(p) == expected_scope}
    if identity(pid) == expected and scope(pid) == expected_scope:
        parents.add(pid)
    pending = {}
    for directory in Path('/proc').glob('[0-9]*'):
        try:
            child = int(directory.name)
            fields = (directory / 'stat').read_text().rsplit(')', 1)[1].split()
            if child != os.getpid() and fields[0] != 'Z':
                pending[child] = (int(fields[1]), fields[19])
        except (FileNotFoundError, ProcessLookupError):
            continue
    changed = True
    while changed:
        changed = False
        for child, (parent, stamp) in pending.items():
            if child not in parents and parent in parents and scope(child) == expected_scope:
                known[child] = stamp
                parents.add(child)
                changed = True
    return {p: stamp for p, stamp in known.items()
            if identity(p) == stamp and scope(p) == expected_scope}


def signal_same(pid, stamp, expected_scope, sig):
    """A pidfd plus a second identity check prevents signalling a reused PID."""
    if identity(pid) != stamp or scope(pid) != expected_scope:
        return
    try:
        fd = os.pidfd_open(pid)
    except ProcessLookupError:
        return
    try:
        if identity(pid) == stamp and scope(pid) == expected_scope:
            signal.pidfd_send_signal(fd, sig)
    except ProcessLookupError:
        pass
    finally:
        os.close(fd)


def terminate_tree(pid, expected, expected_scope, known):
    # Freeze the parent before enumeration so it cannot fork during cleanup.
    # Freeze descendants before killing any parent so none can disappear from
    # the ancestry walk by being reparented while still spawning new children.
    stopped = {}
    try:
        signal_same(pid, expected, expected_scope, signal.SIGSTOP)
        while True:
            tracked = descendants(pid, expected, expected_scope, known)
            fresh = {p: stamp for p, stamp in tracked.items() if stopped.get(p) != stamp}
            if not fresh:
                break
            for child, stamp in fresh.items():
                signal_same(child, stamp, expected_scope, signal.SIGSTOP)
                stopped[child] = stamp
        for child, stamp in stopped.items():
            signal_same(child, stamp, expected_scope, signal.SIGKILL)
        signal_same(pid, expected, expected_scope, signal.SIGKILL)
    finally:
        # Failed cleanup must not leave a live process suspended indefinitely.
        for child, stamp in {pid: expected, **stopped}.items():
            signal_same(child, stamp, expected_scope, signal.SIGCONT)
    while identity(pid) == expected or descendants(pid, expected, expected_scope, known):
        time.sleep(0.2)


def worker_ancestor():
    pid = os.getppid()
    while pid > 1:
        command = Path(f'/proc/{pid}/cmdline').read_bytes().split(b'\0')[0]
        if Path(os.fsdecode(command)).name == 'Runner.Worker':
            return pid
        fields = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
        pid = int(fields[1])
    raise RuntimeError('Runner.Worker ancestor not found; refusing an unlocked job')


def watch(pid, expected, lock, notify_fd, deadline, execution_timeout=EXECUTION_TIMEOUT):
    with open(lock, 'r+') as stream:
        while identity(pid) == expected:
            if time.monotonic() > deadline:
                raise RuntimeError('Timed out waiting for safe host resources or the shared CI slot')
            # Resource recovery also needs this lock. A queued worker must not
            # prevent cache maintenance from restoring the disk admission floor.
            if admission_reason(worker_pid=pid):
                time.sleep(0.2)
                continue
            try:
                fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
                # Another job or maintenance may have changed resources while
                # this worker was waiting for the lock. Recheck before admission.
                if admission_reason(worker_pid=pid):
                    fcntl.flock(stream, fcntl.LOCK_UN)
                    time.sleep(0.2)
                    continue
                break
            except BlockingIOError:
                if time.monotonic() > deadline:
                    raise RuntimeError('Timed out waiting for the shared CI slot')
                time.sleep(0.2)
        else:
            return
        if identity(pid) != expected:
            return
        os.write(notify_fd, b'ready\n')
        os.close(notify_fd)
        active_deadline = time.monotonic() + execution_timeout
        expected_scope = scope(pid)
        known = {}
        next_sample = 0
        while identity(pid) == expected:
            if time.monotonic() >= next_sample:
                known.update(descendants(pid, expected, expected_scope, known))
                next_sample = time.monotonic() + 1
            if time.monotonic() >= active_deadline:
                syslog.syslog(syslog.LOG_ERR, f'CI worker {pid} exceeded {execution_timeout}s after admission; terminating its process tree')
                # Retain the lease through cleanup. Never admit a second build
                # while an over-budget worker or one of its children is alive.
                while True:
                    try:
                        terminate_tree(pid, expected, expected_scope, known)
                        break
                    except (OSError, RuntimeError):
                        time.sleep(1)  # fail closed: do not drop the lease
                break
            time.sleep(0.2)
        # A cancelled worker can leave children behind. Retain the slot until
        # the last child observed in its verified process tree has exited.
        while descendants(pid, expected, expected_scope, known):
            try:
                terminate_tree(pid, expected, expected_scope, known)
            except (OSError, RuntimeError):
                time.sleep(1)
        # Closing the fd releases flock only after cleanup.


def wait_for_admission(reader, pid, timeout, report_interval=30):
    """Report resource/slot waiting without inheriting job output in the watcher."""
    deadline = time.monotonic() + timeout
    reason = ''
    while True:
        ready, _, _ = select.select([reader], [], [], 0)
        if ready:
            if os.read(reader, 64) == b'ready\n':
                return
            raise RuntimeError('CI admission watcher exited before admission: ' + reason)
        remaining = deadline - time.monotonic()
        reason = admission_reason(worker_pid=pid) or 'shared CI slot is occupied'
        if remaining <= 0:
            raise RuntimeError('Timed out waiting for CI admission: ' + reason)
        print(f'Waiting for CI admission: {reason}; {remaining:.0f}s remaining', flush=True)
        ready, _, _ = select.select([reader], [], [], min(report_interval, remaining))
        if ready:
            if os.read(reader, 64) == b'ready\n':
                return
            raise RuntimeError('CI admission watcher exited before admission: ' + reason)


def acquire(pid, lock=LOCK, timeout=21600, execution_timeout=EXECUTION_TIMEOUT):
    if pid <= 1 or not math.isfinite(execution_timeout) or not 0 < execution_timeout <= EXECUTION_TIMEOUT:
        raise ValueError('Invalid worker or active execution timeout')
    expected = identity(pid)
    if expected is None:
        raise RuntimeError('Worker already exited')
    reader, writer = os.pipe()
    env = dict(os.environ)
    env.pop('RUNNER_TRACKING_ID', None)
    process = subprocess.Popen(
        [sys.executable, str(Path(__file__).resolve()), '--watch', str(pid), expected,
         lock, str(writer), str(time.monotonic() + timeout), str(execution_timeout)],
        pass_fds=(writer,), stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL, env=env, start_new_session=True)
    os.close(writer)
    try:
        wait_for_admission(reader, pid, timeout + 5)
    except BaseException:
        process.terminate()
        process.wait(timeout=5)
        raise
    finally:
        os.close(reader)
    print(f'Shared CI slot acquired; active execution limit {execution_timeout}s (queue waiting excluded)', flush=True)
    return process.pid


if __name__ == '__main__':
    if len(sys.argv) > 1 and sys.argv[1] == '--watch':
        watch(int(sys.argv[2]), sys.argv[3], sys.argv[4], int(sys.argv[5]), float(sys.argv[6]), float(sys.argv[7]) if len(sys.argv) > 7 else EXECUTION_TIMEOUT)
    else:
        limit = float(os.environ.get('WORKSPACE_CI_EXECUTION_TIMEOUT_SECONDS', EXECUTION_TIMEOUT))
        acquire(worker_ancestor(), execution_timeout=limit)
