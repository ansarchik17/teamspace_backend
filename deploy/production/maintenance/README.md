# VPS cache and image maintenance

Versioned sources of the existing VPS maintenance tools. Updating the checkout
does not install them automatically. The host-specific guard, global CI lease,
release locks and protection checks apply before any mutation.

| Source | Installed target |
| --- | --- |
| `ci-cache-maintenance.py` | `/usr/local/sbin/workspace-ci-cache.py` |
| `docker-retention/retention.py` | `/usr/local/sbin/docker-image-retention.py` |
| `docker-retention/policy.json` | `/etc/docker-image-retention.json` |

Image retention preserves all containers, live release/rollback references,
existing archived references, environment-selected images, named special tags,
unknown repositories, at least five recent versions, and images under 24 hours.
Missing **live** release images block image deletion. Missing historical backup
images do not block maintenance, while any historical image still present
remains protected.

Cache maintenance cannot delete application images. If a release reference is
missing, it protects every existing image and can still recover reproducible
caches. Its existing age/budget policy also rotates old runner diagnostics and
temporary files; inspect a plan before requesting a manual cleanup.

For the 2026-11-01 manual recovery, log expiration was disabled for that pass.
Only regular BuildKit layers in a verified unused cache and one reproducible
CI `node_modules` install were removed. Application volumes and data remained.

Run tests in separate interpreters so production helper imports cannot shadow
the checkout's other CI regression modules:

```sh
python3 -B -m unittest discover -s deploy/production/maintenance -p test_ci_cache_maintenance.py -v
python3 -B -m unittest discover -s deploy/production/maintenance/docker-retention -v
```

Before installation, compare the installed SHA-256 with the audited baseline,
back up the original, run the tests on Linux, acquire the shared CI lock,
and replace atomically with the original root ownership and permissions.
Do not install over a running image cleanup. Keep the baseline backups for rollback.
