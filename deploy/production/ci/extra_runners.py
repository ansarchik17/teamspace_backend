"""Operator-owned registry of additional, resource-bounded repository runners."""
import json
import os
from pathlib import Path
import stat

REGISTRY = Path('/opt/workspace-ci/extra-runners.json')
ALLOWED = {'workspace-mail'}


def configured_runners(path=REGISTRY, trusted_uid=0):
    if not path.exists():
        return {}
    info = path.lstat()
    if path.is_symlink() or not stat.S_ISREG(info.st_mode) or info.st_uid != trusted_uid or info.st_mode & 0o022:
        raise RuntimeError('Untrusted extra runner registry')
    data = json.loads(path.read_text())
    if not isinstance(data, dict) or not set(data).issubset(ALLOWED):
        raise RuntimeError('Unexpected extra runner repositories')
    result = {}
    for repo, value in data.items():
        directory = Path('/opt/actions-runner-ci-' + repo)
        if value != str(directory) or directory.is_symlink():
            raise RuntimeError('Unexpected extra runner path')
        registration = json.loads((directory / '.runner').read_text(encoding='utf-8-sig'))
        if registration['gitHubUrl'].rstrip('/') != 'https://github.com/west6ide/' + repo:
            raise RuntimeError('Unexpected extra runner registration')
        result[repo] = value
    return result
