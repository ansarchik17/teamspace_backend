#!/usr/bin/env python3
"""Reuse the verified binary of the exact MinIO commit used by integration tests."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

COMMIT = '0d7408fc9969caf07de6a8c3a84f9fbb10a6739e'


def checksum(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def valid(binary, manifest, inputs):
    try:
        if binary.is_symlink() or manifest.is_symlink() or not os.access(binary, os.X_OK):
            return False
        return json.loads(manifest.read_text()) == {**inputs, 'sha256': checksum(binary)}
    except (OSError, ValueError):
        return False


def compile_binary(output):
    with tempfile.TemporaryDirectory(prefix='minio-source-', dir=output.parent) as temporary:
        source = Path(temporary)
        for args in [
            ['git', 'init', str(source)],
            ['git', '-C', str(source), 'remote', 'add', 'origin', 'https://github.com/minio/minio.git'],
            ['git', '-C', str(source), 'fetch', '--depth=1', 'origin', COMMIT],
            ['git', '-C', str(source), 'checkout', '--detach', 'FETCH_HEAD'],
        ]:
            subprocess.run(args, check=True, timeout=180)
        actual = subprocess.check_output(['git', '-C', str(source), 'rev-parse', 'HEAD'], text=True).strip()
        if actual != COMMIT:
            raise RuntimeError('MinIO checkout does not match the pinned commit')
        subprocess.run(['go', 'build', '-trimpath', '-o', str(output), '.'],
                       cwd=source, env={**os.environ, 'CGO_ENABLED': '0'}, check=True, timeout=900)


def resolve(root, inputs, build=compile_binary):
    key = hashlib.sha256(json.dumps(inputs, sort_keys=True).encode()).hexdigest()
    directory = root / key
    directory.mkdir(parents=True, exist_ok=True)
    binary, manifest = directory / 'minio', directory / 'manifest.json'
    with (directory / 'build.lock').open('a+') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if not valid(binary, manifest, inputs):
            with tempfile.TemporaryDirectory(prefix='build-', dir=directory) as temporary:
                output = Path(temporary) / 'minio'
                build(output)
                output.chmod(0o755)
                record = Path(temporary) / 'manifest.json'
                record.write_text(json.dumps({**inputs, 'sha256': checksum(output)}, sort_keys=True))
                output.replace(binary)
                record.replace(manifest)
    return binary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cache', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    inputs = {name: subprocess.check_output(['go', 'env', name], text=True).strip()
              for name in ['GOVERSION', 'GOOS', 'GOARCH', 'GOFLAGS']}
    inputs.update(commit=COMMIT, cgo='0', trimpath=True)
    binary = resolve(args.cache.resolve(), inputs)
    shutil.copy2(binary, args.output)
    print('Pinned MinIO binary verified and ready for integration tests')


if __name__ == '__main__':
    main()
