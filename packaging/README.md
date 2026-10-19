<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Packaging — Phase 9 (nfpm + goreleaser)

Supported distros (docs/01): Debian 12/13, Ubuntu 24.04/26.04 LTS, AlmaLinux 9/10, Rocky Linux 9/10.

- `systemd/` — unit files (`mirymesh-controller.service`, `mirymesh-datanode.service`) + `/etc/mirymesh/config.yaml` + `/var/lib/mirymesh`, `/var/log` layout.
- `nfpm.yaml` — unified `nfpm` config for DEB+RPM (single file, `--packager deb/rpm`). `packaging/scripts/{pre,post}{install,remove}.sh` create `mirymesh:mirymesh` idempotently; see `User=mirymesh` in systemd units.
- `config/config.yaml.example` — example for `/etc/mirymesh/config.yaml`.
- `docker/Dockerfile` — multi-stage Dockerfile (golang:1.22-bookworm + distroless/static) + pinned toolchain.

Reproducible build: `go 1.22` pinned in `go.mod` + `GOWORK=off`, no cgo required in M4.

Build:
```bash
go build -trimpath -o /tmp/mirymesh-controller ./cmd/controller && go build -trimpath -o /tmp/mirymesh-datanode ./cmd/datanode && go build -trimpath -o /tmp/mirymesh ./cmd/mirymesh
nfpm pkg --packager deb --config packaging/nfpm.yaml --target mirymesh.deb
nfpm pkg --packager rpm --config packaging/nfpm.yaml --target mirymesh.rpm
```
CI: `.github/workflows/ci.yml` now runs `packaging` (build + docker build + nfpm deb/rpm `--dry-run` + goreleaser check) and `linux-matrix` (Debian/Alma/Rocky via docker) and `e2e-external` on `schedule`/`workflow_dispatch`.

User creation: `preinstall.sh` (`getent group`/`id -u` + `groupadd`/`useradd --system --no-create-home --shell /usr/sbin/nologin`). `postinstall.sh` creates `/var/lib/mirymesh` etc. and seeds `config.yaml` from `.example` (0600).
