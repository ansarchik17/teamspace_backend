#!/usr/bin/env python3
"""Install hooks only for this VPS's known repository runners."""
import datetime
import json
from pathlib import Path
import shutil
import socket
import subprocess
from extra_runners import ALLOWED

ROOT = Path('/opt/workspace-ci')
REPOSITORIES = {'iform', 'workspace-backend', 'workspace-front', 'workspace-ai',
                'workspace-growy-backend', 'workspace-growy-front',
                'workspace-sd-backend', 'workspace-sd-front',
                'workspace-odo-backend', 'workspace-odo-front', 'workspace-mail'}
REPOSITORIES.update(ALLOWED)


def main():
    if socket.gethostname() != 'vds38185.vpsza500.kz':
        raise RuntimeError('Unexpected server; refusing changes')
    worker_probe = subprocess.run(['pgrep', '-x', 'Runner.Worker'], stdout=subprocess.DEVNULL).returncode
    if worker_probe == 0:
        raise RuntimeError('A job is running; wait for it to finish before installing hooks')
    if worker_probe != 1:
        raise RuntimeError('Unable to determine CI worker state; refusing changes')
    source = Path(__file__).resolve().parent
    names = ('job-lock.py', 'job-started.sh', 'resource_gate.py', 'test_job_lock.py',
             'test_resource_gate.py', 'reclaim_memory.py', 'test_reclaim_memory.py',
             'cache_pressure.py', 'test_cache_pressure.py',
             'maintenance_tick.py', 'test_maintenance_tick.py', 'install.py', 'test_install.py',
             'maintenance-service.conf', 'maintenance-timer.conf',
             'extra_runners.py', 'test_extra_runners.py', 'refresh_extra_sdk.py',
             'test_refresh_extra_sdk.py')
    if not all((source / name).is_file() for name in names):
        raise RuntimeError('Incomplete CI source package')
    maintenance_units = [Path('/etc/systemd/system') / ('workspace-ci-cache.' + suffix)
                         for suffix in ('service', 'timer')]
    if any(path.exists() for path in maintenance_units):
        if not all(path.is_file() and not path.is_symlink() and (source / ('maintenance-' + path.suffix[1:] + '.conf')).is_file()
                   for path in maintenance_units):
            raise RuntimeError('Incomplete existing CI maintenance service')
        props = dict(line.split('=', 1) for line in subprocess.check_output([
            'systemctl', 'show', 'workspace-ci-cache.service', '-p', 'User', '-p', 'ExecStart'
        ], text=True).splitlines())
        if props['User'] not in ('', 'root') or not any(program in props['ExecStart'] for program in (
                '/usr/local/sbin/workspace-ci-cache.py', '/opt/workspace-ci/maintenance_tick.py')):
            raise RuntimeError('Unrecognized existing CI maintenance service')
        for path in maintenance_units:
            target = path.parent / (path.name + '.d') / 'ci-memory.conf'
            content = source / ('maintenance-' + path.suffix[1:] + '.conf')
            if target.is_symlink() or target.parent.is_symlink() or (target.exists() and target.read_text() != content.read_text()):
                raise RuntimeError('Unrecognized existing CI memory drop-in')
    runners = []
    for base in ('/opt', '/home'):
        for p in Path(base).glob('**/.runner'):
            if '_work' in p.parts:
                continue
            data = json.loads(p.read_text(encoding='utf-8-sig'))
            url = data.get('gitHubUrl', '').rstrip('/')
            if url not in {'https://github.com/west6ide/' + repo for repo in REPOSITORIES}:
                continue
            env_path = p.parent / '.env'
            old = env_path.read_text() if env_path.exists() else ''
            lines = old.splitlines()
            for line in lines:
                if line.startswith('ACTIONS_RUNNER_HOOK_JOB_STARTED=') and line != 'ACTIONS_RUNNER_HOOK_JOB_STARTED=/opt/workspace-ci/job-started.sh':
                    raise RuntimeError('Existing custom job hook requires manual reconciliation: ' + str(p.parent))
            service = (p.parent / '.service').read_text().strip()
            if not service.startswith('actions.runner.west6ide-') or not service.endswith('.service'):
                raise RuntimeError('Unexpected service name')
            runners.append((p.parent, env_path, old, lines, service))
    if len(runners) < 10:
        raise RuntimeError('Expected at least ten known runners; nothing was changed')
    ROOT.mkdir(mode=0o755, exist_ok=True)
    backup = ROOT / 'backups' / datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    backup.mkdir(parents=True, mode=0o700)
    (ROOT / 'backups').chmod(0o700)
    for name in names:
        target = ROOT / name
        if target.exists():
            shutil.copy2(target, backup / name)
        if source / name != target:
            shutil.copy2(source / name, target)
        target.chmod(0o755 if name.endswith('.sh') else 0o644)
    lock = ROOT / 'job.lock'; lock.touch(exist_ok=True); lock.chmod(0o666)
    if all(path.exists() for path in maintenance_units):
        for path in maintenance_units:
            shutil.copy2(path, backup / path.name)
            target = path.parent / (path.name + '.d') / 'ci-memory.conf'
            target.parent.mkdir(exist_ok=True)
            if target.exists():
                shutil.copy2(target, backup / (path.name + '.ci-memory.conf'))
            shutil.copy2(source / ('maintenance-' + path.suffix[1:] + '.conf'), target)
            target.chmod(0o644)
        subprocess.run(['systemctl', 'daemon-reload'], check=True)
        subprocess.run(['systemctl', 'try-restart', 'workspace-ci-cache.timer'], check=True)
    for directory, env_path, old, lines, service in runners:
        (backup / (directory.name + '.env')).write_text(old)
        lines = [line for line in lines if not line.startswith('ACTIONS_RUNNER_HOOK_JOB_STARTED=')]
        lines.append('ACTIONS_RUNNER_HOOK_JOB_STARTED=/opt/workspace-ci/job-started.sh')
        env_path.write_text('\n'.join(lines) + '\n')
        subprocess.run(['systemctl', 'restart', service], check=True)
        subprocess.run(['systemctl', 'is-active', '--quiet', service], check=True)
        print('CI hook active:', directory.name, flush=True)


if __name__ == '__main__':
    main()
