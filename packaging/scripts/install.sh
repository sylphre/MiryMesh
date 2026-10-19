#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
# MiryMesh — LXC installer (no Docker required).
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash
#   sudo bash packaging/scripts/install.sh [--deb|--rpm|--from-source] [--repo DIR]
set -eu

MODE="from-source"
REPO=""
for arg in "$@"; do
  case "$arg" in
    --deb) MODE="deb" ;;
    --rpm) MODE="rpm" ;;
    --from-source|--source) MODE="from-source" ;;
    --repo) echo "usage: --repo DIR" >&2; exit 1 ;;
    --repo=*) REPO="${arg#--repo=}" ;;
    *) echo "unknown arg: $arg (expected --deb|--rpm|--from-source|--repo=DIR)" >&2; exit 1 ;;
  esac
done

log() { printf "[install] %s\n" "$*"; }
die() { printf "error: %s\n" "$*" >&2; exit 1; }

is_lxc() { grep -q "container=lxc" /proc/1/environ 2>/dev/null || [ -f /run/systemd/container ] && grep -q lxc /run/systemd/container 2>/dev/null || false; }
if is_lxc; then log "LXC detected — native install (no Docker)"; else log "Not LXC — proceeding native install anyway (Docker not required)"; fi

# Resolve repo dir: if REPO given use it, else if we are inside a MiryMesh checkout use it, else clone to /tmp/mirymesh-src
if [ -n "$REPO" ]; then
  SRC="$REPO"
elif [ -f ./go.mod ] && [ -f ./packaging/nfpm.yaml ]; then
  SRC="$(pwd)"
else
  SRC="/tmp/mirymesh-src"
  if [ -d "$SRC/.git" ]; then
    log "Updating $SRC"
    git -C "$SRC" fetch --depth=1 origin master || true
    git -C "$SRC" rev-parse --verify HEAD >/dev/null 2>&1 || git clone https://github.com/sylphre/MiryMesh "$SRC"
  else
    log "Cloning MiryMesh to $SRC"
    rm -rf "$SRC"
    git clone https://github.com/sylphre/MiryMesh "$SRC"
  fi
fi
log "Source: $SRC"

# Ensure mirymesh user/group (idempotent)
if ! getent group mirymesh >/dev/null 2>&1; then
  groupadd --system mirymesh 2>/dev/null || groupadd mirymesh || true
fi
if ! id -u mirymesh >/dev/null 2>&1; then
  useradd --system --gid mirymesh --home-dir /var/lib/mirymesh --no-create-home --shell /usr/sbin/nologin mirymesh 2>/dev/null \
    || useradd --system --gid mirymesh mirymesh || true
fi

# Build or install via package
install_from_source() {
  if ! command -v go >/dev/null 2>&1; then
    die "go not found — install golang (apt: golang-go, dnf: golang) then rerun"
  fi
  log "Building from source (go 1.22, trimpath, no Docker)"
  (cd "$SRC" && go build -trimpath -o /tmp/mirymesh-controller ./cmd/controller)
  (cd "$SRC" && go build -trimpath -o /tmp/mirymesh-datanode   ./cmd/datanode)
  (cd "$SRC" && go build -trimpath -o /tmp/mirymesh            ./cmd/mirymesh)
  install -m 0755 /tmp/mirymesh-controller /usr/local/bin/mirymesh-controller
  install -m 0755 /tmp/mirymesh-datanode   /usr/local/bin/mirymesh-datanode
  install -m 0755 /tmp/mirymesh            /usr/local/bin/mirymesh
  # systemd units — prefer /usr/local/bin path for source installs
  if [ -f "$SRC/packaging/systemd/mirymesh-controller.service" ]; then
    sed 's|/usr/bin/mirymesh-controller|/usr/local/bin/mirymesh-controller|g' "$SRC/packaging/systemd/mirymesh-controller.service" > /etc/systemd/system/mirymesh-controller.service
    sed 's|/usr/bin/mirymesh-datanode|/usr/local/bin/mirymesh-datanode|g' "$SRC/packaging/systemd/mirymesh-datanode.service" > /etc/systemd/system/mirymesh-datanode.service
  fi
  log "Installed to /usr/local/bin"
}

install_deb() {
  # Expect a .deb built via: nfpm pkg --packager deb --config packaging/nfpm.yaml --target mirymesh.deb
  DEB=""
  for c in "$SRC/mirymesh.deb" ./mirymesh.deb /tmp/mirymesh.deb; do [ -f "$c" ] && DEB="$c" && break; done
  [ -n "$DEB" ] || die "mirymesh.deb not found — build first: nfpm pkg --packager deb --config packaging/nfpm.yaml --target mirymesh.deb"
  log "Installing DEB: $DEB"
  if command -v apt >/dev/null 2>&1; then apt install -y "$DEB"; else dpkg -i "$DEB"; fi
}

install_rpm() {
  RPM=""
  for c in "$SRC/mirymesh.rpm" ./mirymesh.rpm /tmp/mirymesh.rpm; do [ -f "$c" ] && RPM="$c" && break; done
  [ -n "$RPM" ] || die "mirymesh.rpm not found — build first: nfpm pkg --packager rpm --config packaging/nfpm.yaml --target mirymesh.rpm"
  log "Installing RPM: $RPM"
  if command -v dnf >/dev/null 2>&1; then dnf install -y "$RPM"; elif command -v yum >/dev/null 2>&1; then yum install -y "$RPM"; else rpm -ivh "$RPM"; fi
}

case "$MODE" in
  from-source) install_from_source ;;
  deb)         install_deb ;;
  rpm)         install_rpm ;;
esac

# Config seeding (0600, only if missing — same as postinstall.sh)
mkdir -p /etc/mirymesh /var/lib/mirymesh /var/log/mirymesh
chown mirymesh:mirymesh /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
chmod 0750 /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
if [ ! -f /etc/mirymesh/config.yaml ] && [ -f "$SRC/packaging/config/config.yaml.example" ]; then
  cp "$SRC/packaging/config/config.yaml.example" /etc/mirymesh/config.yaml
  chmod 0600 /etc/mirymesh/config.yaml 2>/dev/null || true
  log "Created /etc/mirymesh/config.yaml from example (0600) — edit before starting if needed"
elif [ ! -f /etc/mirymesh/config.yaml ]; then
  log "No config at /etc/mirymesh/config.yaml — create one from packaging/config/config.yaml.example"
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload 2>/dev/null || true
  log "To start: sudo systemctl enable --now mirymesh-controller  # and mirymesh-datanode on storage nodes"
  log "Status:   systemctl status mirymesh-controller; journalctl -u mirymesh-controller -f"
  log "Health:   curl -f http://127.0.0.1:9000/healthz"
else
  log "No systemctl — run directly: /usr/local/bin/mirymesh-controller /etc/mirymesh/config.yaml"
fi

log "Done."
