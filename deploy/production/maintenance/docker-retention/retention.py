#!/usr/bin/env python3
"""Selective local image retention; never touches containers, volumes or registries."""
import argparse
import contextlib
import datetime as dt
import fcntl
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys

UTC = dt.timezone.utc
COMMIT = re.compile(r"(?:sha-)?(?:[0-9a-f]{40}|[0-9a-f]{64})")


def docker(*args):
    result = subprocess.run(["docker", *args], capture_output=True, text=True, timeout=180)
    if result.returncode:
        raise RuntimeError("Docker command failed: " + " ".join(args[:3]))
    return result.stdout.strip()


def inspected(kind, refs):
    result = []
    for offset in range(0, len(refs), 50):
        result.extend(json.loads(docker(kind, "inspect", *refs[offset:offset + 50])))
    return result


def created(image):
    # Docker may emit nanoseconds; Python 3.10 accepts at most microseconds.
    value = image["Created"].replace("Z", "+00:00")
    value = re.sub(r"(\.\d{6})\d+", r"\1", value)
    return dt.datetime.fromisoformat(value)


def reference_files(root):
    return sorted({path for depth in (1, 2, 3)
                   for name in ("images.env", "images.env.previous")
                   for path in root.glob("*/" * depth + "releases/" + name)})


def pins(root, files=None):
    refs = set()
    files = reference_files(root) if files is None else files
    if not files:
        raise RuntimeError("No release image files found; refusing cleanup")
    for path in files:
        for line in path.read_text().splitlines():
            match = re.fullmatch(r"\s*(?:export\s+)?([A-Z0-9_]*IMAGE)\s*=\s*(.*?)\s*", line)
            if match:
                value = match[2].strip("\"'")
                if not value or "$" in value or re.search(r"\s", value):
                    raise RuntimeError("Invalid release image reference in " + str(path))
                refs.add(value)
    return files, refs


def snapshot(root):
    # Container-pinned intermediate images can be omitted by Docker's default listing.
    ids = sorted(set(docker("image", "ls", "--all", "--quiet", "--no-trunc").splitlines()))
    images = inspected("image", ids)
    containers = inspected("container", docker("container", "ls", "--all", "--quiet").splitlines())
    files, refs = pins(root)
    lookup = {im["Id"]: im["Id"] for im in images}
    for im in images:
        for ref in (im.get("RepoTags") or []) + (im.get("RepoDigests") or []):
            lookup[ref] = im["Id"]
    # Historical backup manifests still protect images that exist locally.
    # Only live project release/rollback manifests require every image to exist:
    # an already absent historical image must not disable all maintenance.
    active_files = [path for path in files if 'backups' not in path.relative_to(root).parts]
    _, required_refs = pins(root, active_files)
    missing = sorted(ref for ref in required_refs if ref not in lookup)
    protected = {c["Image"] for c in containers}
    protected.update(lookup[ref] for ref in refs if ref in lookup)
    # Current .env may select a newly pulled candidate before release files change.
    project_dirs = {path.parent.parent for path in files}
    project_dirs.update(root.glob("*"))
    for env in sorted({p for base in project_dirs if base.is_dir()
                       for p in base.glob(".env*")}):
        if not env.is_file():
            continue
        for line in env.read_text().splitlines():
            match = re.fullmatch(r"\s*(?:export\s+)?([A-Z0-9_]*IMAGE)\s*=\s*(.*?)\s*", line)
            if match:
                ref = match[2].strip("\"'")
                if ref in lookup:
                    protected.add(lookup[ref])
    return images, containers, protected, files, refs, missing


def select(images, protected, repositories, keep, min_age_hours, now):
    protected = set(protected)
    repos = {}
    for image in images:
        tags = image.get("RepoTags") or []
        labels = image.get("Config", {}).get("Labels") or {}
        if labels.get("retention.keep", "").lower() == "true":
            protected.add(image["Id"])
        for tag in tags:
            repo, _, suffix = tag.rpartition(":")
            if repo in repositories:
                repos.setdefault(repo, {})[image["Id"]] = image
            if repo not in repositories or not COMMIT.fullmatch(suffix):
                protected.add(image["Id"])
    for versions in repos.values():
        newest = sorted(versions.values(), key=created, reverse=True)[:keep]
        protected.update(im["Id"] for im in newest)
    cutoff = now - dt.timedelta(hours=min_age_hours)
    candidates = []
    for image in images:
        tags = image.get("RepoTags") or []
        # Untagged images can be rollback references by SHA. Do not globally prune them.
        if tags and image["Id"] not in protected and created(image) < cutoff:
            candidates.append(image)
    return sorted(candidates, key=created), protected


@contextlib.contextmanager
def locks(root, state_dir):
    paths = [state_dir / ".retention.lock"]
    ci_lock = root / "workspace-ci" / "job.lock"
    if ci_lock.exists():
        paths.append(ci_lock)
    for release in sorted({p.parent for p in reference_files(root)}):
        paths.extend([release / ".deploy.lock", release / ".images.lock"])
    handles = []
    try:
        for path in paths:
            # New release locks must retain the release directory's owner so
            # unprivileged deployment runners can continue opening them.
            try:
                fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_EXCL, 0o660)
            except FileExistsError:
                handle = path.open("r+")
            else:
                if os.geteuid() == 0:
                    owner = path.parent.stat()
                    os.fchown(fd, owner.st_uid, owner.st_gid)
                    os.fchmod(fd, 0o660)
                handle = os.fdopen(fd, "r+")
            handles.append(handle)
            fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
        yield
    finally:
        for handle in reversed(handles):
            handle.close()


def save_status(state_dir, report):
    path = state_dir / "last-run.json"
    temp = state_dir / "last-run.json.tmp"
    temp.write_text(json.dumps(report, indent=2) + "\n")
    os.chmod(temp, 0o640)
    temp.replace(path)


def run(args, config):
    root, state_dir = Path(config["root"]), Path(config["state_dir"])
    if socket.gethostname() != config["expected_host"]:
        raise RuntimeError("Unexpected hostname; refusing Docker operations")
    if args.apply and os.geteuid() != 0:
        raise RuntimeError("Apply requires root")
    state_dir.mkdir(parents=True, exist_ok=True, mode=0o750)
    with locks(root, state_dir):
        images, containers, protected, files, refs, missing = snapshot(root)
        before = {c["Id"]: c["Image"] for c in containers}
        candidates, protected = select(images, protected, set(config["repositories"]),
                                       config["keep_latest"], config["min_age_hours"], dt.datetime.now(UTC))
        report = {"time_utc": dt.datetime.now(UTC).isoformat(), "mode": "apply" if args.apply else "plan",
                  "status": "planned", "release_files": [str(p) for p in files],
                  "protected_images": len(protected), "missing_release_refs": missing,
                  "candidate_images": len(candidates), "candidate_tags": [t for im in candidates for t in im["RepoTags"]],
                  "candidate_details": [{"id": im["Id"], "tags": im["RepoTags"], "created": im["Created"], "size_bytes": im.get("Size", 0)} for im in candidates],
                  "removed_tags": [], "free_gib_before": round(os.statvfs(root).f_bavail * os.statvfs(root).f_frsize / 2**30, 2)}
        if missing:
            report["status"] = "blocked_missing_release_images"
            save_status(state_dir, report)
            print(json.dumps(report, indent=2))
            return 1
        if args.apply:
            for image in candidates:
                # Recheck references and all containers immediately before each removal.
                fresh, _, pinned, _, _, missing = snapshot(root)
                if missing:
                    raise RuntimeError("Release reference disappeared; cleanup stopped")
                allowed, _ = select(fresh, pinned, set(config["repositories"]), config["keep_latest"],
                                    config["min_age_hours"], dt.datetime.now(UTC))
                by_id = {im["Id"]: im for im in allowed}
                if image["Id"] not in by_id or set(by_id[image["Id"]]["RepoTags"]) != set(image["RepoTags"]):
                    continue
                docker("image", "rm", "--no-prune", *image["RepoTags"])
                report["removed_tags"].extend(image["RepoTags"])
            after_images, after_containers, _, _, _, missing = snapshot(root)
            after = {c["Id"]: c["Image"] for c in after_containers}
            if missing or before != after or not protected.issubset({im["Id"] for im in after_images}):
                raise RuntimeError("Post-cleanup protection verification failed")
            report["status"] = "ok"
        report["free_gib_after"] = round(os.statvfs(root).f_bavail * os.statvfs(root).f_frsize / 2**30, 2)
        if args.apply and report["free_gib_after"] < config["min_free_gib"]:
            report["status"] = "low_disk_space"
        save_status(state_dir, report)
        print(json.dumps(report, indent=2))
        return 0 if report["status"] in {"ok", "planned"} else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", default="/etc/docker-image-retention.json")
    parser.add_argument("--apply", action="store_true", help="Default is a read-only image plan")
    args = parser.parse_args()
    try:
        config = json.loads(Path(args.config).read_text())
        if config["keep_latest"] < 5 or config["min_age_hours"] < 24:
            raise RuntimeError("Unsafe retention settings")
        return run(args, config)
    except BlockingIOError:
        print("Deployment/image-selection lock is busy; cleanup skipped")
        return 0
    except Exception as exc:
        print("Cleanup failed: " + str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
