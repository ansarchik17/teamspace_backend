# CI resource usage

## Avoid work that cannot change a release

Pushes changing only README.md or docs/**/*.md do not start a production release.
Pull-request checks remain unconditional to avoid leaving required checks pending.
Each job checks out its immutable source once; later phases keep validated build
artifacts instead of deleting them with another checkout. npm uses its local
download cache when possible; dependency lockfiles and audits remain enabled.
The shared VPS still admits one heavy job at a time to protect production.

## Reuse converter runtime in release CI

Release CI fingerprints docker/ and the UTC ISO week, resolves a published
infrastructure tag in the existing workspace-backend GHCR package, and builds it only on a real cache miss.
Refresh builds pull base images and bypass old package layers. The existing
converter tests run as app against that exact immutable runtime digest, which
also becomes the final application image base. Vet, all integration tests and
production converter checks remain mandatory. Four Go binaries are built once
in the runner and copied into Dockerfile.ci; the ordinary Dockerfile remains
usable for standalone source builds. No server admission thresholds change.
