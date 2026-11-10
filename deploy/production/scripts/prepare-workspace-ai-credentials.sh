#!/usr/bin/env bash
set -Eeuo pipefail
# Move only missing speech-provider credentials to the accounted Go gateway.
# Values remain in process memory and protected local env/backup files.
python3 - "${1:-/opt/workspace/.env}" <<'PY'
import datetime
import json
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys
import tempfile

target = Path(sys.argv[1])
if target.is_symlink() or not target.is_file():
    raise SystemExit('Expected an existing regular backend env file')
original = target.read_bytes()
metadata = target.stat()
text = original.decode()
values = {}
for line in text.splitlines():
    match = re.match(r'^\s*(?:export\s+)?([A-Z0-9_]+)\s*=(.*)$', line)
    if match:
        values[match[1]] = match[2].strip().strip("\"'")
source = {}
result = subprocess.run(['docker', 'container', 'inspect', '--format', '{{json .Config.Env}}', 'alemcloud-api'], capture_output=True, text=True, timeout=15)
if result.returncode == 0:
    for item in json.loads(result.stdout):
        key, _, value = item.partition('=')
        source[key] = value
additions = {}
for destination, candidates in {
    'WORKSPACE_AI_OPENAI_API_KEY': ['MEETING_AI_CHATGPT_API_KEY', 'OPENAI_API_KEY'],
    'WORKSPACE_AI_ELEVENLABS_API_KEY': ['SMART_AGENT_ELEVENLABS_API_KEY', 'ELEVENLABS_API_KEY'],
}.items():
    if values.get(destination):
        continue
    value = next((source.get(name, '').strip() for name in candidates if source.get(name, '').strip()), '')
    if value and value != 'workspace-accounted':
        if value.lower().startswith('bearer '):
            value = value[7:].strip()
        if not re.fullmatch(r'[A-Za-z0-9._~+/=-]+', value):
            raise SystemExit('Unexpected provider credential format; configure backend env directly')
        additions[destination] = value
if not values.get('WORKSPACE_AI_JOB_SIGNING_KEY'):
    additions['WORKSPACE_AI_JOB_SIGNING_KEY'] = secrets.token_urlsafe(48)
if not additions:
    print('Workspace AI gateway credentials already configured')
    raise SystemExit(0)
stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
backup = target.with_name(target.name + '.before-workspace-ai-' + stamp)
fd = os.open(backup, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'wb') as stream:
    stream.write(original)
with tempfile.NamedTemporaryFile(dir=target.parent, prefix='.workspace-ai-env-', delete=False) as stream:
    temporary = Path(stream.name)
    try:
        os.fchmod(stream.fileno(), stat.S_IMODE(metadata.st_mode) & 0o660)
        os.fchown(stream.fileno(), metadata.st_uid, metadata.st_gid)
        stream.write(original + (b'\n' if original and not original.endswith(b'\n') else b'') + b'\n# Accounted Workspace AI gateway\n')
        for key, value in additions.items():
            stream.write((key + "='" + value + "'\n").encode())
        stream.flush()
        os.fsync(stream.fileno())
        os.replace(temporary, target)
    finally:
        temporary.unlink(missing_ok=True)
print('Workspace AI gateway prepared; provider credentials were not printed')
PY
