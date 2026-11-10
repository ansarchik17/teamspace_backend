"""Fail closed before admitting a CI job on the shared production VPS."""
from pathlib import Path
import shutil


def admission_reason(proc=Path('/proc'), disk=Path('/opt'), minimum_mib=2560, worker_pid=None):
    # One serialized build: at most 2 GiB for the bounded builder plus
    # 512 MiB for its runner and host headroom. PSI/disk checks still apply.
    try:
        memory = dict(line.split(':', 1) for line in (proc / 'meminfo').read_text().splitlines())
        available = int(memory['MemAvailable'].split()[0]) // 1024
        reserved = 0
        if worker_pid is not None:
            worker = proc / str(worker_pid)
            if worker_pid <= 1 or (worker / 'comm').read_text().strip() != 'Runner.Worker':
                return 'CI worker identity unavailable'
            status = dict(line.split(':', 1) for line in (worker / 'status').read_text().splitlines() if ':' in line)
            rss_kib = int(status['VmRSS'].split()[0])
            if rss_kib < 0:
                return 'CI worker memory unavailable'
            # The watcher runs after Runner.Worker starts. At most 128 MiB of
            # its real RSS is already spent from this job's 2.5 GiB total budget.
            # Never count other runners, swap, or an unbounded process allowance.
            reserved = min(128, rss_kib // 1024)
        pressure = (proc / 'pressure/memory').read_text().splitlines()
        some = next(line for line in pressure if line.startswith('some '))
        avg10 = float(dict(item.split('=') for item in some.split()[1:])['avg10'])
        if available + reserved < minimum_mib:
            return f'CI memory budget {available} MiB available + {reserved} MiB already reserved < {minimum_mib} MiB'
        if avg10 >= 5:
            return f'memory pressure avg10={avg10}'
        if shutil.disk_usage(disk).free < 30 * 2**30:
            return 'disk free < 30 GiB'
    except (OSError, ValueError, KeyError, StopIteration, TypeError):
        return 'resource probes unavailable'
    return ''
