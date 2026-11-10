#!/usr/bin/env python3
"""Maintain reproducible CI caches on one known VPS, only while CI is idle."""
import argparse
import datetime as dt
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import re
import socket
import stat
import sys
import subprocess
import time

GIB = 2 ** 30
STATE = Path('/var/lib/workspace-ci-cache')
REPOS = {'iform', 'workspace-ai', 'workspace-backend', 'workspace-front',
         'workspace-growy-front', 'workspace-growy-backend',
         'workspace-sd-front', 'workspace-sd-backend',
         'workspace-odo-front', 'workspace-odo-backend'}
CACHE_NAMES = {'buildx_buildkit_ci-' + repo + '0_state' for repo in REPOS}
CACHE_NAMES.add('buildx_buildkit_iform-alem-537fbd70_state')
ORPHAN_BUDGET = 8 * GIB
GO_BUDGET = 2 * GIB
GO_CACHE = Path('/home/actions/.cache/go-build')
RUNNERS = [Path('/opt/actions-runner' + suffix) for suffix in (
    '', '-frontend', '-workspace-ai', '-growy-frontend', '-growy-backend',
    '-iform', '-sd-frontend', '-sd-backend')]
RUNNERS += [Path('/home/github-runner/actions-runner-odo-' + part)
            for part in ('frontend', 'backend')]

# The ten original runners stay mandatory. Extras are admitted only from an
# operator-owned registry with validated repository registrations and paths.
if Path('/opt/workspace-ci/extra-runners.json').exists():
    sys.path.insert(0, '/opt/workspace-ci')
    from extra_runners import configured_runners
    extra = configured_runners()
    REPOS.update(extra)
    RUNNERS.extend(Path(value) for value in extra.values())
    CACHE_NAMES.update('buildx_buildkit_ci-' + name + '0_state' for name in extra)


def command(argv, timeout=180):
    result = subprocess.run(argv, capture_output=True, text=True, timeout=timeout, cwd='/')
    if result.returncode:
        raise RuntimeError('Command failed: ' + ' '.join(argv[:4]) + ': ' + result.stderr[:300])
    return result.stdout.strip()


def workers():
    result = subprocess.run(['pgrep', '-x', 'Runner.Worker'], capture_output=True)
    if result.returncode not in (0, 1):
        raise RuntimeError('Cannot determine CI worker state')
    return result.returncode == 0


def acquire_ci_slot(stream, wait_seconds):
    """Wait a bounded time for CI, without stopping or interrupting a job."""
    deadline = time.monotonic() + wait_seconds
    while True:
        try:
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
            return True
        except BlockingIOError:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return False
            time.sleep(min(1, remaining))


def verify_managed_workers(proc_root=Path('/proc')):
    # With job.lock held and all started hooks verified, known workers can only
    # be waiting in their hook. Any other runner must block cache maintenance.
    for proc in proc_root.glob('[0-9]*'):
        try:
            if (proc / 'comm').read_text().strip() != 'Runner.Worker':
                continue
            executable = (proc / 'exe').resolve(strict=True)
            if not any(runner in executable.parents for runner in RUNNERS):
                raise RuntimeError('Unmanaged CI worker detected; refusing maintenance')
        except (FileNotFoundError, ProcessLookupError):
            continue


def containers():
    ids = command(['docker', 'ps', '-aq']).split()
    return json.loads(command(['docker', 'inspect', *ids])) if ids else []


def attached(cs):
    return {m['Name'] for c in cs for m in c['Mounts'] if m['Type'] == 'volume'}


def usage(path):
    return int(command(['nice', '-n', '19', 'du', '-x', '-s', '-B1', str(path)]).split()[0])


def cache_volume(v, used, now):
    """Fail closed on unknown names, mounts, symlinks or non-BuildKit data."""
    name = v['Name']
    if name not in CACHE_NAMES or name in used:
        return None
    if v['Driver'] != 'local' or v.get('Options') or v.get('Scope') != 'local':
        return None
    p = Path(v['Mountpoint'])
    expected = Path('/var/lib/docker/volumes') / name / '_data'
    if p != expected or p.resolve() != expected or p.is_symlink():
        return None
    for entry in ('cache.db', 'history.db', 'runc-overlayfs'):
        item = p / entry
        if item.is_symlink() or not item.exists():
            return None
    if not (p / 'cache.db').is_file() or not (p / 'history.db').is_file():
        return None
    if not (p / 'runc-overlayfs').is_dir():
        return None
    # Top-level database mtime alone does not prove that nested snapshots are old.
    latest = p.stat().st_mtime
    device = p.stat().st_dev
    for root, dirs, files in os.walk(p, followlinks=False):
        for name2 in dirs + files:
            s = (Path(root) / name2).lstat()
            if s.st_dev != device:
                return None
            latest = max(latest, s.st_mtime)
    return {'name': name, 'bytes': usage(p), 'last_write': latest,
            'age_hours': round((now - latest) / 3600, 2)}


def orphan_plan(rows, now, budget=ORPHAN_BUDGET):
    total = sum(x['bytes'] for x in rows)
    selected = []
    for row in sorted(rows, key=lambda x: x['last_write']):
        age = now - row['last_write']
        if age >= 7 * 86400 or (total > budget and age >= 86400):
            selected.append(row)
            total -= row['bytes']
    return selected


def files_to_expire(folder, days, now):
    """Only ordinary files inside fixed diagnostic/temp directories."""
    if folder.is_symlink() or not folder.is_dir() or folder.resolve() != folder:
        return []
    selected = []
    device = folder.stat().st_dev
    for root, dirs, files in os.walk(folder, followlinks=False):
        dirs[:] = [d for d in dirs if not (Path(root) / d).is_symlink()
                   and (Path(root) / d).stat().st_dev == device]
        for name in files:
            p = Path(root) / name
            s = p.lstat()
            if stat.S_ISREG(s.st_mode) and s.st_dev == device and now - s.st_mtime > days * 86400:
                if folder.name != '_diag' or p.suffix == '.log':
                    selected.append(p)
    return selected


def executable_as_actions(path):
    return subprocess.run(['runuser', '-u', 'actions', '--', 'test', '-x', str(path)],
                          capture_output=True).returncode == 0


def go_binary():
    found = []
    for runner in RUNNERS:
        for p in (runner / '_work/_tool/go').glob('*/x64/bin/go'):
            version = p.parents[2].name
            if (re.fullmatch(r'\d+\.\d+\.\d+', version) and p.is_file()
                    and executable_as_actions(p)):
                found.append((tuple(map(int, version.split('.'))), p))
    return str(max(found)[1]) if found else None


def trim_go_cache(folder, total_bytes, budget=GO_BUDGET):
    """Evict the oldest Go cache entries instead of discarding every hot build."""
    if folder.is_symlink() or folder.resolve() != folder:
        raise RuntimeError('Unexpected Go cache path')
    rows = []
    for directory in folder.iterdir():
        if not re.fullmatch(r'[0-9a-f]{2}', directory.name) or directory.is_symlink() or not directory.is_dir():
            continue
        for path in directory.iterdir():
            info = path.lstat()
            if (not stat.S_ISREG(info.st_mode) or path.is_symlink()
                    or not re.fullmatch(r'[0-9a-f]{64}-[ad]', path.name)
                    or not path.name.startswith(directory.name)):
                continue
            rows.append((info.st_mtime_ns, path, info))
    removed = 0
    for _, path, before in sorted(rows, key=lambda row: row[0]):
        if total_bytes <= budget:
            break
        try:
            after = path.lstat()
        except FileNotFoundError:
            continue
        if (after.st_ino, after.st_dev, after.st_size, after.st_mtime_ns) != (
                before.st_ino, before.st_dev, before.st_size, before.st_mtime_ns):
            continue
        if path.is_symlink() or not stat.S_ISREG(after.st_mode):
            continue
        path.unlink()
        total_bytes -= after.st_blocks * 512
        removed += 1
    return removed


def verify_ci_hooks():
    # New workers may queue while maintenance owns job.lock. Their started
    # hook must wait for this same lock before executing any workflow steps.
    for runner in RUNNERS:
        data = json.loads((runner / '.runner').read_text(encoding='utf-8-sig'))
        if data.get('gitHubUrl', '').rstrip('/') not in {'https://github.com/west6ide/' + r for r in REPOS}:
            raise RuntimeError('Unexpected runner registration: ' + str(runner))
        hooks = [line.strip() for line in (runner / '.env').read_text().splitlines()
                 if line.startswith('ACTIONS_RUNNER_HOOK_JOB_STARTED=')]
        if hooks != ['ACTIONS_RUNNER_HOOK_JOB_STARTED=/opt/workspace-ci/job-started.sh']:
            raise RuntimeError('Runner does not use the shared CI lock: ' + str(runner))
    return len(RUNNERS)


def protected_images():
    spec = importlib.util.spec_from_file_location('retention', '/usr/local/sbin/docker-image-retention.py')
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    cfg = json.loads(Path('/etc/docker-image-retention.json').read_text())
    images, _, pinned, _, _, missing = mod.snapshot(Path(cfg['root']))
    if missing:
        # Cache maintenance never deletes application images. If an old release
        # image is already absent, protect every remaining image and continue
        # reclaiming reproducible build caches. Image retention keeps its own
        # stricter missing-live-rollback check.
        return {image['Id'] for image in images}
    _, protected = mod.select(images, pinned, set(cfg['repositories']), cfg['keep_latest'],
                              cfg['min_age_hours'], dt.datetime.now(dt.timezone.utc))
    return protected


def save(report):
    temp = STATE / ('last-run.' + str(os.getpid()) + '.tmp')
    temp.write_text(json.dumps(report, indent=2) + '\n')
    temp.replace(STATE / 'last-run.json')


def maintain(apply, report):
    if apply:
        report['ci_hooks_verified'] = verify_ci_hooks()
    now = time.time()
    cs = containers()
    original = {c['Id']: (c['Image'], c['State']['Running']) for c in cs}
    protected = protected_images()
    volumes = command(['docker', 'volume', 'ls', '-q']).split()
    known = [v for v in volumes if v in CACHE_NAMES]
    inspected = json.loads(command(['docker', 'volume', 'inspect', *known])) if known else []
    rows = [row for v in inspected if (row := cache_volume(v, attached(cs), now))]
    planned = orphan_plan(rows, now)
    report.update(orphan_cache_inventory=rows, orphan_cache_candidates=planned,
                  removed_cache_volumes=[], builders_pruned=[], expired_files=0,
                  go_cache_bytes_before=usage(GO_CACHE) if GO_CACHE.is_dir() else 0,
                  free_bytes_before=os.statvfs('/opt').f_bavail * os.statvfs('/opt').f_frsize)
    expiring = []
    binds = [Path(m['Source']) for c in cs for m in c['Mounts'] if m['Type'] == 'bind']
    for runner in RUNNERS:
        if runner.is_symlink() or runner.resolve() != runner or not (runner / '.runner').is_file():
            continue
        data = json.loads((runner / '.runner').read_text(encoding='utf-8-sig'))
        if data.get('gitHubUrl', '').rstrip('/') not in {'https://github.com/west6ide/' + r for r in REPOS}:
            continue
        for folder, days in ((runner / '_diag', 14), (runner / '_work/_temp', 7)):
            if any(p == folder or folder in p.parents or p in folder.parents for p in binds):
                continue
            expiring += files_to_expire(folder, days, now)
    report['expired_file_candidates'] = len(expiring)
    if not apply:
        report['status'] = 'planned'
        return
    for row in planned:
        fresh_used = attached(containers())
        v = json.loads(command(['docker', 'volume', 'inspect', row['name']]))[0]
        fresh = cache_volume(v, fresh_used, time.time())
        if fresh is None or fresh['last_write'] != row['last_write']:
            continue
        # Docker's unforced removal also rejects any newly attached volume.
        command(['docker', 'volume', 'rm', row['name']])
        report['removed_cache_volumes'].append(row['name'])
        save(report)
    for c in cs:
        name = c['Name'].lstrip('/')
        if name + '_state' not in CACHE_NAMES or not c['State']['Running']:
            continue
        help_text = command(['docker', 'exec', c['Id'], 'buildctl', 'prune', '--help'])
        if '--keep-storage' not in help_text or '--keep-duration' not in help_text:
            raise RuntimeError('Unsupported BuildKit prune version')
        command(['docker', 'exec', c['Id'], 'buildctl', 'prune', '--keep-duration', '24h',
                 '--keep-storage', '3000'], timeout=300)
        report['builders_pruned'].append(name)
        save(report)
    help_text = command(['docker', 'buildx', 'prune', '--help'])
    if '--max-used-space' not in help_text:
        raise RuntimeError('Unsupported default-builder prune version')
    command(['docker', 'buildx', 'prune', '--builder', 'default', '--force',
             '--filter', 'until=24h', '--max-used-space', '3GB'], timeout=300)
    report['default_builder_pruned'] = True
    if report['go_cache_bytes_before'] > GO_BUDGET:
        report['go_cache_entries_evicted'] = trim_go_cache(GO_CACHE, report['go_cache_bytes_before'])
        report['go_cache_cleared'] = False
    for p in expiring:
        # Revalidate type and age; never traverse a replaced symlink ancestor.
        if any(parent.is_symlink() for parent in p.parents):
            continue
        try:
            s = p.lstat()
            days = 14 if any(parent.name == '_diag' for parent in p.parents) else 7
            if stat.S_ISREG(s.st_mode) and time.time() - s.st_mtime > days * 86400:
                p.unlink()
                report['expired_files'] += 1
        except FileNotFoundError:
            continue
    after = {c['Id']: (c['Image'], c['State']['Running']) for c in containers()}
    ids = set(command(['docker', 'image', 'ls', '-aq', '--no-trunc']).split())
    if original != after or not protected.issubset(ids):
        raise RuntimeError('Post-maintenance protection verification failed')
    report.update(status='ok', containers_unchanged=True, protected_images_present=len(protected),
                  go_cache_bytes_after=usage(GO_CACHE) if GO_CACHE.is_dir() else 0,
                  free_bytes_after=os.statvfs('/opt').f_bavail * os.statvfs('/opt').f_frsize)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--wait-for-ci-seconds', type=int, default=0,
                        help='Bounded wait for the shared CI slot (apply only)')
    args = parser.parse_args()
    if not 0 <= args.wait_for_ci_seconds <= 900 or (args.wait_for_ci_seconds and not args.apply):
        parser.error('CI wait must be 0..900 seconds and requires --apply')
    if socket.gethostname() != 'vds38185.vpsza500.kz' or os.geteuid() != 0:
        raise RuntimeError('Expected root on the approved VPS')
    os.umask(0o027)
    STATE.mkdir(mode=0o750, exist_ok=True)
    report = {'time_utc': dt.datetime.now(dt.timezone.utc).isoformat(),
              'mode': 'apply' if args.apply else 'plan', 'status': 'started'}
    result = 0
    try:
        if args.wait_for_ci_seconds:
            verify_ci_hooks()
        with open('/opt/workspace-ci/job.lock', 'r+') as lock:
            started_waiting = time.monotonic()
            acquired = acquire_ci_slot(lock, args.wait_for_ci_seconds)
            report['waited_for_ci_seconds'] = round(time.monotonic() - started_waiting, 2)
            if not acquired:
                report['status'] = 'skipped_busy'
            else:
                if args.wait_for_ci_seconds:
                    report['ci_hooks_verified'] = verify_ci_hooks()
                    verify_managed_workers()
                    maintain(args.apply, report)
                elif workers():
                    report['status'] = 'skipped_workers_present'
                else:
                    maintain(args.apply, report)
    except Exception as exc:
        report.update(status='failed', error=str(exc))
        result = 1
    finally:
        save(report)
        print(json.dumps(report, indent=2), flush=True)
    return result


if __name__ == '__main__':
    raise SystemExit(main())
