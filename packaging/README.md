<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Packaging — Phase4 stubs

Supported distros (docs/01): Debian 12/13, Ubuntu 24.04/26.04 LTS, AlmaLinux 9/10, Rocky Linux 9/10.

- `systemd/` — unit files (`mirymesh-controller.service`, `mirymesh-datanode.service`) + `/etc/mirymesh/config.yaml` + `/var/lib/mirymesh`, `/var/log` layout.
- `deb/` — `nfpm` config for DEB (Debian family), systemd. `make deb` will call `goreleaser/nfpm`.
- `rpm/` — `nfpm` config for RPM (RHEL family).
- `docker/` — multi-stage Dockerfile (golang:1.22 + distroless/static) + pinned toolchain.

Reproducible build: `go 1.22` pinned in `go.mod` + `GOWORK=off`, no cgo required in M4.
Phase4 does not yet produce artifacts; these dirs document layout and will be wired in CI next milestone.
