#!/usr/bin/env python3
"""Recover reproducible CI layers before low disk space stalls the shared queue."""
import datetime
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import sys
import time
from extra_runners import configured_runners

ROOT = Path('/opt/workspace-ci')
TRIGGER = 32 * 2**30
TARGET = 35 * 2**30
REPOS = {'iform', 'workspace-ai', 'workspace-backend', 'workspace-front',
         'workspace-growy-front', 'workspace-growy-backend',
         'workspace-sd-front', 'workspace-sd-backend',
         'workspace-odo-front', 'workspace-odo-backend'}
REPOS.update(configured_runners())


def candidates(rows, free):
    if free >= TRIGGER:
        return []
    names = {'buildx_buildkit_ci-' + repo + '0_state' for repo in REPOS}
    return sorted((row for row in rows if row['name'] in names),
                  key=lambda row: row['bytes'], reverse=True)


def load_policy():
    spec = importlib.util.spec_from_file_location('cache_policy', '/usr/local/sbin/workspace-ci-cache.py')
    policy = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = policy
    spec.loader.exec_module(policy)
    return policy


def recover(apply=False):
    free = shutil.disk_usage('/opt').free
    if free >= TRIGGER:
        return {'status': 'sufficient_disk', 'free_bytes': free}
    import reclaim_memory as reclaim
    with (ROOT / 'job.lock').open('r+') as lease:
        try:
            fcntl.flock(lease, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return {'status': 'skipped_busy'}
        if 'active' in reclaim.worker_states(reclaim.process_snapshot()).values():
            return {'status': 'skipped_active_worker'}
        policy = load_policy()
        assert policy.verify_ci_hooks() == len(policy.RUNNERS)
        policy.verify_managed_workers()
        containers = policy.containers()
        original = {c['Id']: (c['Image'], c['State']['Running']) for c in containers}
        images = set(policy.command(['docker', 'image', 'ls', '-aq', '--no-trunc']).split())
        volumes = policy.command(['docker', 'volume', 'ls', '-q']).split()
        names = [v for v in volumes if v in policy.CACHE_NAMES]
        inspected = json.loads(policy.command(['docker', 'volume', 'inspect', *names])) if names else []
        rows = [row for v in inspected
                if (row := policy.cache_volume(v, policy.attached(containers), time.time()))]
        plan = candidates(rows, free)
        report = {'status': 'planned', 'free_bytes_before': free, 'builders': []}
        if not apply:
            report['candidates'] = [row['name'] for row in plan]
            return report
        backup = ROOT / 'backups' / ('cache-pressure-' + datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ'))
        backup.mkdir(mode=0o700)
        for row in plan:
            if shutil.disk_usage('/opt').free >= TARGET:
                break
            if 'active' in reclaim.worker_states(reclaim.process_snapshot()).values():
                report['interrupted_by_worker'] = True
                break
            volume = row['name']
            builder = volume.removeprefix('buildx_buildkit_').removesuffix('0_state')
            fresh = json.loads(policy.command(['docker', 'volume', 'inspect', volume]))[0]
            assert policy.cache_volume(fresh, policy.attached(policy.containers()), time.time())
            config = backup / builder
            config.mkdir(mode=0o700)
            env = dict(os.environ, DOCKER_CONFIG=str(config))
            def command(args, timeout=90):
                return subprocess.check_output(args, env=env, text=True,
                                               stderr=subprocess.STDOUT, timeout=timeout)
            buildkit = config / 'buildkitd.toml'
            buildkit.write_text('[worker.oci]\nmax-parallelism = 1\ngc = false\n')
            created = False
            try:
                command(['docker', 'buildx', 'create', '--name', builder, '--driver', 'docker-container',
                         '--driver-opt', 'memory=1g', '--driver-opt', 'memory-swap=1280m',
                         '--driver-opt', 'cpu-quota=100000', '--buildkitd-config', str(buildkit)])
                created = True
                command(['docker', 'buildx', 'inspect', '--builder', builder, '--bootstrap'], timeout=120)
                c = json.loads(command(['docker', 'inspect', 'buildx_buildkit_' + builder + '0']))[0]
                assert c['HostConfig']['Memory'] == 2**30
                assert c['HostConfig']['MemorySwap'] == 1280 * 2**20
                assert c['HostConfig']['CpuQuota'] == 100000
                assert c['Config']['Image'].startswith('moby/buildkit:')
                assert not c['HostConfig'].get('PortBindings')
                assert [m['Name'] for m in c['Mounts'] if m['Type'] == 'volume'] == [volume]
                du = ['docker', 'buildx', 'du', '--builder', builder, '--filter', 'type=exec.cachemount',
                      '--format', '{{.ID}}|{{.Type}}|{{.Size}}']
                before = command(du)
                (config / 'dependencies-before.txt').write_text(before)
                output = command(['docker', 'buildx', 'prune', '--builder', builder,
                                  '--filter', 'type=regular', '--force'], timeout=300)
                (config / 'prune.log').write_text(output)
                after = command(du)
                (config / 'dependencies-after.txt').write_text(after)
                assert set(before.splitlines()) == set(after.splitlines()), 'Dependency cache changed'
            finally:
                if created:
                    command(['docker', 'buildx', 'rm', '--keep-state', builder])
            report['builders'].append(builder)
        if not report.get('interrupted_by_worker') and shutil.disk_usage('/opt').free < TARGET:
            import workspace_cleanup
            report['workspace_installs'] = workspace_cleanup.recover(policy, target_free_bytes=TARGET, apply=True)
        assert {c['Id']: (c['Image'], c['State']['Running']) for c in policy.containers()} == original
        assert images.issubset(set(policy.command(['docker', 'image', 'ls', '-aq', '--no-trunc']).split()))
        after = shutil.disk_usage('/opt').free
        report.update(status='recovered' if after >= TRIGGER else 'insufficient_disk', free_bytes_after=after,
                      containers_unchanged=True, all_images_preserved=True,
                      dependency_caches_preserved=True, backup=str(backup))
        (backup / 'result.json').write_text(json.dumps(report, indent=2) + '\n')
        return report


if __name__ == '__main__':
    assert socket.gethostname() == 'vds38185.vpsza500.kz' and os.geteuid() == 0
    print(json.dumps(recover('--apply' in sys.argv)), flush=True)
