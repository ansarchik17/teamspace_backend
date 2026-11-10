#!/usr/bin/env python3
"""Refresh only idle additional runners from the maintained primary runner SDK."""
import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import time
import uuid

from extra_runners import configured_runners

BASE = Path('/opt/actions-runner')
LEASE = Path('/opt/workspace-ci/job.lock')


def busy(directory):
    for process in Path('/proc').glob('[0-9]*'):
        try:
            if (process / 'comm').read_text().strip() == 'Runner.Worker' and directory in (process / 'exe').resolve(strict=True).parents:
                return True
        except (FileNotFoundError, ProcessLookupError):
            continue
    return False


def sdk_version():
    version = subprocess.check_output([str(BASE / 'bin/Runner.Listener'), '--version'], text=True, timeout=15).strip()
    if not re.fullmatch(r'2\.\d+\.\d+', version):
        raise RuntimeError('Unexpected primary SDK version')
    return version


def install_sdk(directory):
    """Hard links retain the complete SDK without eight additional copies."""
    for name in ['bin', 'externals']:
        source = (BASE / name).resolve(strict=True)
        target = directory / name
        if target.exists():
            raise RuntimeError('SDK target already exists')
        subprocess.run(['cp', '-al', str(source), str(target)], check=True)
    for name in ['config.sh', 'env.sh', 'svc.sh', 'run.sh']:
        shutil.copy2(BASE / name, directory / name)


def refresh():
    runners = configured_runners()
    if not runners:
        return {}
    version = sdk_version()
    updated = []
    with LEASE.open('r+') as lease:
        try:
            fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return {'status': 'busy'}
        for repo, value in runners.items():
            directory = Path(value)
            marker = directory / '.ci-sdk-version'
            if marker.read_text().strip() == version or busy(directory):
                continue
            service = (directory / '.service').read_text().strip()
            if not service.startswith('actions.runner.west6ide-' + repo + '.') or not service.endswith('.service'):
                raise RuntimeError('Unexpected extra runner service')
            # The shared lease prevents a worker from entering build steps.
            subprocess.run(['systemctl', 'stop', service], check=True, timeout=60)
            if busy(directory):
                subprocess.run(['systemctl', 'start', service], check=True)
                continue
            staging = directory / ('.sdk-' + uuid.uuid4().hex)
            staging.mkdir(mode=0o755)
            old = directory / ('.sdk-previous-' + uuid.uuid4().hex)
            old.mkdir(mode=0o755)
            old_version = marker.read_text()
            try:
                install_sdk(staging)
                for name in ['bin', 'externals']:
                    (directory / name).rename(old / name)
                    (staging / name).rename(directory / name)
                marker.write_text(version + '\n')
                subprocess.run(['systemctl', 'start', service], check=True, timeout=30)
                subprocess.run(['systemctl', 'is-active', '--quiet', service], check=True)
                updated.append(repo)
            except BaseException:
                marker.write_text(old_version)
                for name in ['bin', 'externals']:
                    if (old / name).exists():
                        if (directory / name).exists():
                            (directory / name).rename(staging / ('failed-' + name))
                        (old / name).rename(directory / name)
                old.rmdir()
                subprocess.run(['systemctl', 'start', service], check=False)
                raise
            finally:
                shutil.rmtree(staging)
            # These are SDK hard links only; configurations/workspaces stay put.
            shutil.rmtree(old)
    return {'version': version, 'updated': updated}


if __name__ == '__main__':
    print(json.dumps(refresh()))
