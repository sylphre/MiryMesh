<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Packaging — Phase 5+ (CI wired)

Supported distros (docs/01): Debian 12/13, Ubuntu 24.04/26.04 LTS, AlmaLinux 9/10, Rocky Linux 9/10.

- `systemd/` — unit files (`mirymesh-controller.service`, `mirymesh-datanode.service`) + `/etc/mirymesh/config.yaml` + `/var/lib/mirymesh`, `/var/log` layout.
- `deb/` — `nfpm` config for DEB (Debian family, TODO: wire `nfpm pkg --packager deb`), systemd. `make deb` will call `goreleaser/nfpm`.
- `rpm/` — `nfpm` config for RPM (RHEL family, TODO: wire `nfpm pkg --packager rpm`).
- `docker/` — multi-stage Dockerfile (golang:1.22-bookworm + distroless/static) + pinned toolchain.

Reproducible build: `go 1.22` pinned in `go.mod` + `GOWORK=off`, no cgo required in M4.

CI: `.github/workflows/ci.yml` now runs:
- `packaging` job: `go build -trimpath` + `docker build -f packaging/docker/Dockerfile`
- `linux-matrix` job: `docker run` on Debian 12/13 (golang:1.22-bookworm), AlmaLinux 9/10, Rocky 9 — verifies `go vet/test -short` and systemd units. `schedule` + `workflow_dispatch` also trigger `e2e-external` (Nextcloud/Misskey awscli/mc fixtures).

Next: wire `nfpm` DEB/RPM build and `goreleaser` in CI (when `packaging/nfpm.yaml` lands).
