# Shared VPS CI policy

One admitted job owns `/opt/workspace-ci/job.lock` until its worker and tracked
children exit. Admission requires a 2560 MiB total memory budget, memory pressure
below 5%, and 30 GiB free disk. Queue waiting yields the lock to maintenance.

An admitted job has at most two hours of execution time, independently of its
queue wait and GitHub's connection. The local watcher freezes and kills only
that worker's verified process tree before releasing the lease. PID start times,
cgroup membership and pidfds prevent PID reuse from targeting unrelated tasks.
Cleanup errors retain the lease; they never admit an overlapping build.

Each minute, existing maintenance recovers idle runner memory. If no build owns
the lease, it also stops known CI BuildKit containers that retain memory. It
verifies their exact names, official image, cache volume and absence of exposed
ports. Images and cache volumes remain available; setup-buildx restarts the
builder when the next admitted job needs it. Production containers are excluded.

Deploy helper updates atomically after comparing exact installed SHA-256 values,
with backups, Linux unittest discovery, and rollback on failure. The watcher CLI
accepts the previous argument format so an older hook can finish during an update.
Already running watchers keep their loaded version until their job exits.

Run regression tests on Linux: `python3 -m unittest discover -s deploy/production/ci`.

Memory recovery works in bounded passes when a large file cache cannot fit the
whole target into the existing swap budget. Each pass keeps a 32 MiB swap margin,
restores the original soft limit, preserves hard caps and refuses active workers.

Every maintenance tick checks disk pressure. Below 32 GiB, the shared lease may
be used to prune only regular records from known, unused CI BuildKit caches,
aiming for 35 GiB free. Dependency cache mounts, Docker images, volumes and
production container states are preserved and checked. Active builds keep the
lease and cannot be pruned. Hourly age-based cache maintenance remains in place.
If regular cache records cannot restore that reserve, maintenance removes only
reproducible `node_modules` installs in validated CI checkouts. It requires the
shared lease, refuses executing workers, symlinks and mounted paths, and checks
the repository registration and npm lockfile. Source files and npm's download
cache are retained; the next `npm ci` recreates its disposable install.

Additional repository runners are listed in the root-owned
`/opt/workspace-ci/extra-runners.json` registry. Only the Workspace Mail repository
and its exact registration URL are accepted. It shares the
same global lease, 2.5 GiB admission budget and 30 GiB disk floor as primary jobs.
Their execution limit is 45 minutes, independently of time waiting for admission;
primary jobs retain the existing two-hour execution limit.

Additional runner SDKs use hard links to the maintained primary SDK and disable
independent automatic downloads. The existing maintenance tick refreshes idle
additional runners when the primary version changes, holding the shared lease
and restoring the previous SDK if activation fails. Configurations and workspaces
remain specific to each repository. Hourly Go cache maintenance evicts oldest
valid entries above 2 GiB instead of clearing all compilation results.
The admission thresholds are 2560 MiB RAM and 30 GiB disk. The largest
serialized image builder is capped at 2048 MiB, leaving 512 MiB of headroom.
Only the same worker's already allocated RSS can contribute up to 128 MiB
to admission; swap and other workers never count toward this budget.

While a hook waits, its parent reports the current resource or shared-slot
reason every 30 seconds. The watcher keeps its output detached so inherited
pipes cannot hold the job log open after admission.
