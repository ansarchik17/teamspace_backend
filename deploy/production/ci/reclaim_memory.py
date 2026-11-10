#!/usr/bin/env python3
"""Recover cold CI memory without reducing the build admission reserve."""
import argparse
from dataclasses import dataclass
import fcntl
import json
import math
import os
from pathlib import Path
import signal
import socket
import subprocess
import time
from extra_runners import configured_runners

MIB = 2**20
ROOT = Path('/opt/workspace-ci')
HOOK = str(ROOT / 'job-lock.py')
RUNNERS = {
    'workspace-backend': '/opt/actions-runner',
    'workspace-front': '/opt/actions-runner-frontend',
    'workspace-ai': '/opt/actions-runner-workspace-ai',
    'iform': '/opt/actions-runner-iform',
    'workspace-growy-front': '/opt/actions-runner-growy-frontend',
    'workspace-growy-backend': '/opt/actions-runner-growy-backend',
    'workspace-sd-front': '/opt/actions-runner-sd-frontend',
    'workspace-sd-backend': '/opt/actions-runner-sd-backend',
    'workspace-odo-front': '/home/github-runner/actions-runner-odo-frontend',
    'workspace-odo-backend': '/home/github-runner/actions-runner-odo-backend',
}
RUNNERS.update(configured_runners())


@dataclass(frozen=True)
class Process:
    pid: int
    ppid: int
    comm: str
    group: str
    args: tuple = ()
    state: str = 'S'


def process_snapshot(proc=Path('/proc')):
    result = {}
    for directory in proc.glob('[0-9]*'):
        try:
            fields = (directory / 'stat').read_text().rsplit(')', 1)[1].split()
            comm = (directory / 'comm').read_text().strip()
            group = next((line[3:] for line in (directory / 'cgroup').read_text().splitlines()
                          if line.startswith('0::')), '')
            args = ()
            if comm.startswith('python') and group.startswith('/system.slice/actions.runner.west6ide-'):
                args = tuple(os.fsdecode(s) for s in (directory / 'cmdline').read_bytes().split(b'\0') if s)
            pid = int(directory.name)
            result[pid] = Process(pid, int(fields[1]), comm, group, args, fields[0])
        except (FileNotFoundError, ProcessLookupError):
            continue
    return result


def ancestry(pid, processes):
    chain = []
    seen = set()
    while pid in processes and pid not in seen:
        seen.add(pid)
        chain.append(pid)
        pid = processes[pid].ppid
    return chain


def worker_states(processes):
    """Only an initial hook with no executing step counts as waiting."""
    states = {}
    for worker in processes.values():
        if worker.comm != 'Runner.Worker' or worker.state == 'Z':
            continue
        hooks = [p for p in processes.values()
                 if p.group == worker.group and p.state != 'Z' and len(p.args) == 2
                 and p.args[1] == HOOK and worker.pid in ancestry(p.pid, processes)[1:]]
        allowed = set()
        for hook in hooks:
            allowed.update(ancestry(hook.pid, processes))
            for p in processes.values():
                if p.group == worker.group and len(p.args) > 2 and p.args[1:3] == (HOOK, '--watch'):
                    if hook.pid in ancestry(p.pid, processes)[1:]:
                        allowed.add(p.pid)
        descendants = {p.pid for p in processes.values() if p.state != 'Z'
                       and worker.pid in ancestry(p.pid, processes)[1:]}
        states[worker.pid] = 'waiting' if hooks and descendants.issubset(allowed) else 'active'
    return states


def host_resources(proc=Path('/proc')):
    info = dict(line.split(':', 1) for line in (proc / 'meminfo').read_text().splitlines())
    pressure = next(l for l in (proc / 'pressure/memory').read_text().splitlines() if l.startswith('some '))
    avg10 = float(dict(x.split('=') for x in pressure.split()[1:])['avg10'])
    available = int(info['MemAvailable'].split()[0]) * 1024
    swap_free = int(info['SwapFree'].split()[0]) * 1024
    if available < 0 or swap_free < 0 or not math.isfinite(avg10) or avg10 < 0:
        raise ValueError('Invalid host resource probes')
    return available, swap_free, avg10


def events(path):
    return dict((k, int(v)) for k, v in (line.split() for line in path.read_text().splitlines()))


def reclaim_group(group, target, *, apply=False, still_safe=lambda: True, settle=lambda: time.sleep(1)):
    """Use a temporary soft limit; preserve hard limits and restore on errors."""
    high = group / 'memory.high'
    old_high = high.read_text()
    current = int((group / 'memory.current').read_text())
    swap = int((group / 'memory.swap.current').read_text())
    maximum = (group / 'memory.max').read_text()
    swap_maximum = (group / 'memory.swap.max').read_text()
    if old_high.strip() != 'max' and int(old_high) < target:
        return {'status': 'skipped_already_tight_soft_limit'}
    if not 0 < int(maximum) <= 1536 * MIB or not 128 * MIB <= int(swap_maximum) <= 256 * MIB:
        return {'status': 'skipped_unknown_caps'}
    if current <= target:
        return {'status': 'skipped_swap_budget'}
    # A large file cache need not fit into swap all at once. Reclaim a bounded
    # portion now, then remeasure on the next tick instead of skipping forever.
    budget = int(swap_maximum) - swap - 32 * MIB
    if budget < 16 * MIB:
        return {'status': 'skipped_swap_budget'}
    target = max(target, current - budget)
    if old_high.strip() != 'max' and int(old_high) < target:
        return {'status': 'skipped_already_tight_soft_limit'}
    before_events = events(group / 'memory.events')
    if not still_safe():
        return {'status': 'skipped_process_changed'}
    result = {'status': 'planned', 'before_bytes': current, 'target_bytes': target}
    if not apply:
        return result
    try:
        high.write_text(str(target))
        settle()
    finally:
        high.write_text(old_high)
    assert high.read_text() == old_high, 'Soft limit restoration failed'
    assert (group / 'memory.max').read_text() == maximum, 'Hard memory limit changed'
    assert (group / 'memory.swap.max').read_text() == swap_maximum, 'Hard swap limit changed'
    after_events = events(group / 'memory.events')
    assert all(after_events.get(k, 0) == before_events.get(k, 0) for k in ('oom', 'oom_kill')), 'Unexpected OOM event'
    result.update(status='reclaimed', after_bytes=int((group / 'memory.current').read_text()),
                  swap_bytes=int((group / 'memory.swap.current').read_text()), soft_limit_restored=True)
    return result


def managed_groups():
    groups = {}
    for repo, directory in RUNNERS.items():
        path = Path(directory)
        data = json.loads((path / '.runner').read_text(encoding='utf-8-sig'))
        if data['gitHubUrl'].rstrip('/') != 'https://github.com/west6ide/' + repo:
            raise RuntimeError('Unexpected registration: ' + repo)
        unit = (path / '.service').read_text().strip()
        if not unit.startswith('actions.runner.west6ide-' + repo + '.') or not unit.endswith('.service'):
            raise RuntimeError('Unexpected runner service: ' + repo)
        text = subprocess.check_output(['systemctl', 'show', unit, '-p', 'ActiveState', '-p', 'ControlGroup'], text=True, timeout=10)
        props = dict(line.split('=', 1) for line in text.splitlines())
        if props['ActiveState'] != 'active':
            continue
        if props['ControlGroup'] != '/system.slice/' + unit:
            raise RuntimeError('Unexpected control group: ' + repo)
        group = Path('/sys/fs/cgroup') / props['ControlGroup'].lstrip('/')
        if group.is_symlink() or group.resolve() != group:
            raise RuntimeError('Unexpected control group path')
        groups[repo] = group
    return groups


def recover_builders(apply=False):
    """Stop only idle, known CI builders; retain every cache volume and image."""
    result = {}
    with (ROOT / 'job.lock').open('r+') as lease:
        try:
            fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return {'status': 'skipped_busy_slot'}
        if 'active' in worker_states(process_snapshot()).values():
            return {'status': 'skipped_active_worker'}
        names = {'buildx_buildkit_ci-' + repo + '0' for repo in RUNNERS}
        text = subprocess.check_output(['docker', 'ps', '--format', '{{.Names}}'], text=True, timeout=10)
        for name in sorted(names.intersection(text.splitlines())):
            if host_resources()[0] >= 3328 * MIB:
                break
            data = json.loads(subprocess.check_output(['docker', 'inspect', name], text=True, timeout=10))[0]
            expected_volume = name + '_state'
            mounts = data.get('Mounts', [])
            if (data.get('Name') != '/' + name or not data.get('State', {}).get('Running')
                    or not data.get('Config', {}).get('Image', '').startswith('moby/buildkit:')
                    or len(mounts) != 1 or mounts[0].get('Type') != 'volume'
                    or mounts[0].get('Name') != expected_volume
                    or mounts[0].get('Destination') != '/var/lib/buildkit'
                    or data.get('HostConfig', {}).get('PortBindings')):
                result[name] = 'skipped_unknown_configuration'
                continue
            if 'active' in worker_states(process_snapshot()).values():
                result[name] = 'skipped_worker_admitted'
                break
            if apply:
                subprocess.run(['docker', 'stop', '--time', '10', data['Id']], check=True,
                               stdout=subprocess.DEVNULL, timeout=20)
            result[name] = 'stopped_cache_retained' if apply else 'planned_stop'
    return result


def recover(apply=False):
    available, swap_free, pressure = host_resources()
    report = {'status': 'skipped_sufficient_memory', 'before_available_bytes': available, 'entries': {}}
    if available >= 3328 * MIB:
        return report
    if swap_free < 1024 * MIB or pressure >= 5:
        report['status'] = 'skipped_host_pressure_or_swap'
        return report
    if 'active' in worker_states(process_snapshot()).values():
        report['status'] = 'skipped_active_worker'
        return report
    # A cancelled job can leave its BuildKit daemon alive outside the runner's
    # cgroup. The shared lease prevents this recovery racing any admitted job.
    report['builders'] = recover_builders(apply)
    for repo, group in managed_groups().items():
        processes = process_snapshot()
        states = worker_states(processes)
        if 'active' in states.values():
            report['status'] = 'skipped_active_worker' if not report['entries'] else 'stopped_worker_admitted'
            break
        available, swap_free, pressure = host_resources()
        if available >= 3328 * MIB or swap_free < 1024 * MIB or pressure >= 5:
            break
        group_name = '/' + str(group.relative_to('/sys/fs/cgroup'))
        waiting = any(p.group == group_name and p.pid in states for p in processes.values())
        target = (64 if waiting else 16) * MIB

        def still_safe():
            fresh = process_snapshot()
            if 'active' in worker_states(fresh).values():
                return False
            # An idle listener becoming a worker must get the larger budget.
            fresh_waiting = any(p.comm == 'Runner.Worker' and p.state != 'Z' and p.group == group_name for p in fresh.values())
            return waiting == fresh_waiting

        report['entries'][repo] = reclaim_group(group, target, apply=apply, still_safe=still_safe)
        report['status'] = 'reclaimed' if apply else 'planned'
    report['after_available_bytes'] = host_resources()[0]
    return report


def interrupted(*_):
    raise InterruptedError('Maintenance interrupted; restoring the soft limit')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    if socket.gethostname() != 'vds38185.vpsza500.kz' or os.geteuid() != 0:
        raise RuntimeError('Expected root on the approved VPS')
    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, interrupted)
    with (ROOT / 'reclaim-memory.lock').open('a+') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print(json.dumps({'status': 'skipped_recovery_already_running'}))
            return
        report = recover(args.apply)
    report['time_utc'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
    if args.apply:
        path = ROOT / 'reclaim-memory-last.json'
        temp = path.with_suffix('.tmp')
        temp.write_text(json.dumps(report, indent=2) + '\n')
        temp.replace(path)
    print(json.dumps(report), flush=True)


if __name__ == '__main__':
    main()
