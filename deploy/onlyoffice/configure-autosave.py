#!/usr/bin/env python3
"""Merge the DOC-006 settings into an existing DocumentServer local.json."""
import json
import os
from pathlib import Path
import stat
import sys
import tempfile


def configure(path):
    path = Path(path)
    if path.is_symlink():
        raise ValueError("use the real configuration file, not a symlink")
    attributes = path.stat()
    data = json.loads(path.read_text())
    coauthoring = data.setdefault("services", {}).setdefault("CoAuthoring", {})
    coauthoring.setdefault("autoAssembly", {}).update(
        enable=True, interval="5m", step="1m"
    )
    backoff = coauthoring.setdefault("callbackBackoffOptions", {})
    backoff.update(retries=8, httpStatus="429,500-599")
    backoff.setdefault("timeout", {}).update(
        minTimeout=1000, maxTimeout=30000, factor=2, randomize=True
    )
    fd, temporary = tempfile.mkstemp(prefix=".autosave-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as output:
            os.fchmod(output.fileno(), stat.S_IMODE(attributes.st_mode))
            if os.geteuid() == 0:
                os.fchown(output.fileno(), attributes.st_uid, attributes.st_gid)
            json.dump(data, output, indent=2)
            output.write("\n")
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


if __name__ == "__main__":
    configure(sys.argv[1] if len(sys.argv) > 1 else "/etc/onlyoffice/documentserver/local.json")
    print("Autosave and callback retries configured. Restart DocumentServer to apply.")
