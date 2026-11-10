#!/usr/bin/env python3
"""Recover idle CI memory each minute; retain the hourly disk-cache cadence."""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import time
import cache_pressure
import refresh_extra_sdk

ROOT = Path('/opt/workspace-ci')
STATE = ROOT / 'last-cache-sweep.json'
CACHE_PROGRAM = '/usr/local/sbin/workspace-ci-cache.py'
CACHE_REPORT = Path('/var/lib/workspace-ci-cache/last-run.json')


def tick(state, now, recover, sweep):
    report = {'memory': recover(), 'cache': 'not_due'}
    last = 0
    if state.exists():
        try:
            last = float(json.loads(state.read_text())['last_success'])
            if not 0 <= last <= now + 300:
                raise ValueError('Invalid cache timestamp')
        except (ValueError, TypeError, KeyError):
            report['cache'] = 'skipped_invalid_timestamp'
            return report
    if now - last < 3600:
        return report
    report['cache'] = sweep()
    if report['cache'] == 'ok':
        temp = state.with_suffix('.tmp')
        temp.write_text(json.dumps({'last_success': now}) + '\n')
        temp.replace(state)
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    if socket.gethostname() != 'vds38185.vpsza500.kz' or os.geteuid() != 0:
        raise RuntimeError('Expected root on the approved VPS')

    def recover():
        # Do not force-kill a child while it is restoring a cgroup soft limit.
        cmd = [sys.executable, str(ROOT / 'reclaim_memory.py')]
        if args.apply:
            cmd.append('--apply')
        return json.loads(subprocess.check_output(cmd, text=True))

    def sweep():
        if not args.apply:
            return 'planned'
        # The existing cache tool verifies all registered hooks and workers when
        # waiting for the lock. Queued jobs may exist; executing jobs retain it.
        subprocess.run([sys.executable, CACHE_PROGRAM, '--apply', '--wait-for-ci-seconds', '1'], check=True)
        return json.loads(CACHE_REPORT.read_text())['status']

    pressure = cache_pressure.recover(args.apply)
    report = tick(STATE, time.time(), recover, sweep)
    report['disk_pressure'] = pressure
    report['runner_sdk'] = refresh_extra_sdk.refresh() if args.apply else {'status': 'planned'}
    print(json.dumps(report), flush=True)


if __name__ == '__main__':
    main()
