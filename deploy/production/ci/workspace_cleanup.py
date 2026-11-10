"""Reclaim disposable npm-ci installs while the caller holds the shared CI lease."""
import json
from pathlib import Path
import shutil

import reclaim_memory as reclaim

GIB = 2**30
TARGETS = {
    'workspace-front': ('/opt/actions-runner-frontend', ('node_modules', 'Mail/node_modules')),
    'workspace-growy-front': ('/opt/actions-runner-growy-frontend', ('node_modules',)),
    'workspace-sd-front': ('/opt/actions-runner-sd-frontend', ('node_modules',)),
    'workspace-odo-front': ('/home/github-runner/actions-runner-odo-frontend', ('node_modules',)),
    'workspace-mail': ('/opt/actions-runner-ci-workspace-mail', ('node_modules',)),
}


def overlaps(left, right):
    return left == right or left in right.parents or right in left.parents


def validate(folder, protected):
    if (not folder.is_dir() or folder.is_symlink() or folder.resolve() != folder
            or any(parent.is_symlink() for parent in folder.parents)):
        raise RuntimeError('Unexpected CI install path: ' + str(folder))
    if any(overlaps(folder, path) for path in protected):
        raise RuntimeError('CI install overlaps a mounted path: ' + str(folder))
    if not (folder.parent / 'package-lock.json').is_file():
        raise RuntimeError('CI install has no reproducible npm lockfile')


def protected_paths(containers, mountinfo=Path('/proc/self/mountinfo')):
    paths = [Path(m['Source']) for c in containers for m in c['Mounts']
             if m['Type'] == 'bind']
    # Refuse mountpoints inside the install, as well as any enclosing bind mount.
    # The filesystem root is an ordinary enclosing mount, not an install bind.
    for line in mountinfo.read_text().splitlines():
        text = line.split()[4]
        for escaped, character in [(r'\040', ' '), (r'\011', '\t'),
                                   (r'\012', '\n'), (r'\134', '\\')]:
            text = text.replace(escaped, character)
        if text != '/':
            paths.append(Path(text))
    return paths


def recover(policy, *, target_free_bytes=35*GIB, apply=False):
    """Never remove download caches, source files, images or mounted app data."""
    if 'active' in reclaim.worker_states(reclaim.process_snapshot()).values():
        return {'status': 'skipped_active_worker'}
    if not shutil.rmtree.avoids_symlink_attacks:
        raise RuntimeError('A symlink-safe rmtree implementation is required')
    containers = policy.containers()
    before = {c['Id']: (c['Image'], c['State']['Running']) for c in containers}
    protected = protected_paths(containers)
    candidates = []
    for repo, (base, relatives) in TARGETS.items():
        runner = Path(base)
        data = json.loads((runner / '.runner').read_text(encoding='utf-8-sig'))
        if data['gitHubUrl'].rstrip('/') != 'https://github.com/west6ide/' + repo:
            raise RuntimeError('Unexpected CI registration: ' + repo)
        for relative in relatives:
            folder = runner / '_work' / repo / repo / relative
            if not folder.exists() and not folder.is_symlink():
                continue
            validate(folder, protected)
            candidates.append(folder)
    report = {'status': 'planned', 'candidates': [str(p) for p in candidates], 'removed': []}
    if not apply:
        return report
    for folder in candidates:
        if shutil.disk_usage('/opt').free >= target_free_bytes:
            break
        if 'active' in reclaim.worker_states(reclaim.process_snapshot()).values():
            report['interrupted_by_worker'] = True
            break
        validate(folder, protected_paths(policy.containers()))
        shutil.rmtree(folder)
        report['removed'].append(str(folder))
    after = {c['Id']: (c['Image'], c['State']['Running']) for c in policy.containers()}
    if before != after:
        raise RuntimeError('Container state changed during CI install cleanup')
    report.update(status='completed', npm_download_cache_preserved=True, containers_unchanged=True)
    return report
