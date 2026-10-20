<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Packaging — Phase 9 (nfpm + goreleaser)

Supported distros (docs/01): Debian 12/13, Ubuntu 24.04/26.04 LTS, AlmaLinux 9/10, Rocky Linux 9/10.

- `systemd/` — unit files (`mirymesh-controller.service`, `mirymesh-datanode.service`) + `/etc/mirymesh/config.yaml` + `/var/lib/mirymesh`, `/var/log` layout.
- `nfpm.yaml` — unified `nfpm` config for DEB+RPM (single file, `--packager deb/rpm`). `packaging/scripts/{pre,post}{install,remove}.sh` create `mirymesh:mirymesh` idempotently; see `User=mirymesh` in systemd units.
- `config/config.yaml.example` — example for `/etc/mirymesh/config.yaml`.
- `nginx/nginx.conf.example` — NGINX reverse-proxy example: S3 `0.0.0.0:9000` (TLS + SigV4 passthrough, `client_max_body_size 5G`, `proxy_request_buffering off`) and Admin `0.0.0.0:9001` bound to `127.0.0.1` / `<tailscale-ip>` (Tailscale) only (`allow 100.64.0.0/10; deny all` or mTLS, or direct `127.0.0.1:9001` without NGINX). Copy to `/etc/nginx/sites-available/mirymesh` and adjust `server_name`/certs. All three listeners default to `0.0.0.0:900x` and are reachable via `<tailscale-ip>:900x` and `127.0.0.1:900x` — firewall/Tailscale ACL required.
- `haproxy/haproxy.cfg.example` — haproxy example for your setup (haproxy terminates TLS, S3 on `:80` → `0.0.0.0:9000`, `option forwardfor`, `Authorization` passthrough). Admin is direct `0.0.0.0:9001` / Tailscale `<tailscale-ip>:9001` (no public haproxy frontend for admin).
- `docker/Dockerfile` — multi-stage Dockerfile (golang:1.22-bookworm + distroless/static) + pinned toolchain.

Reproducible build: `go 1.22` pinned in `go.mod` + `GOWORK=off`, no cgo required in M4.

Build:
```bash
go build -trimpath -o /tmp/mirymesh-controller ./cmd/controller && go build -trimpath -o /tmp/mirymesh-datanode ./cmd/datanode && go build -trimpath -o /tmp/mirymesh ./cmd/mirymesh
nfpm pkg --packager deb --config packaging/nfpm.yaml --target mirymesh.deb
nfpm pkg --packager rpm --config packaging/nfpm.yaml --target mirymesh.rpm
```

Install (DEB, e.g. Debian 12/13 / Ubuntu 24.04):
```bash
sudo dpkg -i mirymesh.deb  # or: sudo apt install ./mirymesh.deb
sudo mkdir -p /etc/mirymesh
sudo cp /etc/mirymesh/config.yaml.example /etc/mirymesh/config.yaml  # postinstall does this if missing (0600)
sudoedit /etc/mirymesh/config.yaml  # set server.listen, auth.credentials, storage policies
sudo systemctl daemon-reload
sudo systemctl enable --now mirymesh-controller
sudo systemctl enable --now mirymesh-datanode  # on datanode hosts
systemctl status mirymesh-controller
journalctl -u mirymesh-controller -f
```

Install (RPM, e.g. Alma/Rocky 9/10):
```bash
sudo rpm -ivh mirymesh.rpm  # or: sudo dnf install ./mirymesh.rpm
# same /etc/mirymesh/config.yaml setup as above
sudo systemctl daemon-reload && sudo systemctl enable --now mirymesh-controller
```

Config placement: `/etc/mirymesh/config.yaml` (owner `root:root`, mode `0600` — contains `auth.credentials.secret_key`).
`postinstall.sh` seeds it from `config.yaml.example` only if missing; do not commit secrets. `/var/lib/mirymesh` and `/var/log/mirymesh` are created with `0750` and `mirymesh:mirymesh` ownership by `preinstall.sh`/`postinstall.sh`.

Verification:
```bash
go vet ./... && go test ./... -count=1 -race
golangci-lint run --timeout=3m
docker build -f packaging/docker/Dockerfile -t mirymesh:ci .
nfpm pkg --packager deb --config packaging/nfpm.yaml --target /tmp/mirymesh.deb && echo "deb ok" && rm /tmp/mirymesh.deb
nfpm pkg --packager rpm --config packaging/nfpm.yaml --target /tmp/mirymesh.rpm && echo "rpm ok" && rm /tmp/mirymesh.rpm
goreleaser --snapshot --skip-publish --clean  # or: goreleaser check
```
CI: `.github/workflows/ci.yml` now runs `packaging` (build + docker build + nfpm deb/rpm `--dry-run` + goreleaser check) and `linux-matrix` (Debian/Alma/Rocky via docker) and `e2e-external` on `schedule`/`workflow_dispatch`.

User creation: `preinstall.sh` (`getent group`/`id -u` + `groupadd`/`useradd --system --no-create-home --shell /usr/sbin/nologin`). `postinstall.sh` creates `/var/lib/mirymesh` etc. and seeds `config.yaml` from `.example` (0600).
