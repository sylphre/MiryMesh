#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
# MiryMesh — LXC installer (no Docker required).
#
# Master (controller) or Client/DataNodeを選んでインストールする簡易スクリプト。
# - Master: S3 API (controller) を起動（datanode も同梱するが任意）
# - Client: DataNode（storage node）として Master に登録して容量を提供
#
# Usage (対話式 — 引数なしで選択):
#   curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash
#   # → Master / Client を選択 → Master ならそのまま起動、Client なら Master の URL を入力
#
# Non-interactive:
#   sudo bash packaging/scripts/install.sh --master [--deb|--rpm|--from-source] [--repo DIR]
#   sudo bash packaging/scripts/install.sh --client --master-url http://master:9000 [--deb|--rpm|--from-source] [--repo DIR]
#   # DEB/RPM は事前に nfpm でビルドした成果物が必要: packaging/README.md 参照
#
# Env overrides:
#   MIRYMESH_MASTER_URL=http://master:9000  # Client で Master を指定
#   MIRYMESH_ROLE=master|client             # 対話をスキップ
set -eu

MODE="from-source"
ROLE=""
REPO=""
MASTER_URL="${MIRYMESH_MASTER_URL:-}"
# Allow env override for role
if [ -n "${MIRYMESH_ROLE:-}" ]; then
  case "$MIRYMESH_ROLE" in
    master|controller) ROLE="master" ;;
    client|datanode|data) ROLE="client" ;;
    *) echo "unknown MIRYMESH_ROLE=$MIRYMESH_ROLE (master|client)" >&2; exit 1 ;;
  esac
fi

for arg in "$@"; do
  case "$arg" in
    --master|--controller) ROLE="master" ;;
    --client|--datanode|--data-node) ROLE="client" ;;
    --deb) MODE="deb" ;;
    --rpm) MODE="rpm" ;;
    --from-source|--source) MODE="from-source" ;;
    --master-url=*) MASTER_URL="${arg#--master-url=}" ;;
    --master-url) echo "usage: --master-url URL" >&2; exit 1 ;;
    --repo) echo "usage: --repo DIR" >&2; exit 1 ;;
    --repo=*) REPO="${arg#--repo=}" ;;
    --no-enable) NO_ENABLE=1 ;;
    --help|-h)
      echo "usage: $0 [--master|--client] [--master-url URL] [--deb|--rpm|--from-source] [--repo DIR]"
      echo "  --master   Install as Master (controller, S3 API)"
      echo "  --client   Install as Client/DataNode (needs --master-url or MIRYMESH_MASTER_URL)"
      echo "  No args    Interactive selection (Master / Client)"
      exit 0
      ;;
    *) echo "unknown arg: $arg" >&2; echo "try: $0 --help" >&2; exit 1 ;;
  esac
done

log() { printf "[install] %s\n" "$*"; }
die() { printf "error: %s\n" "$*" >&2; exit 1; }

# Interactive selection if no role given
if [ -z "$ROLE" ]; then
  if [ -t 0 ] || [ -c /dev/tty ]; then
    printf "MiryMesh role — 1) Master (controller, S3 API)  2) Client (DataNode)\n"
    printf "Select [1/2] (default: 1): "
    ans=""
    if [ -c /dev/tty ]; then read -r ans < /dev/tty || true; else read -r ans || true; fi
    case "$ans" in
      2|client|datanode|c|C) ROLE="client" ;;
      *) ROLE="master" ;;
    esac
  else
    ROLE="master"
    log "Non-interactive and no --master/--client — defaulting to Master. Use --client --master-url URL for DataNode."
  fi
fi

# For master, ask gateway mode (Master holds no S3 objects)
if [ "$ROLE" = "master" ]; then
  if [ -t 0 ] || [ -c /dev/tty ]; then
    printf "Master gateway? (y = Master holds no S3 objects, all on DataNodes) [y/N]: "
    if [ -c /dev/tty ]; then read -r ans < /dev/tty || true; else read -r ans || true; fi
    case "$ans" in
      y|Y|yes|YES) MASTER_GATEWAY="yes" ;;
      *) MASTER_GATEWAY="no" ;;
    esac
    if [ "$MASTER_GATEWAY" = "yes" ]; then
      log "Gateway mode selected — Master will hold no S3 objects. Register at least one DataNode or Put will be 507."
    fi
  else
    MASTER_GATEWAY="no"
  fi
fi

# For client, ensure master URL
if [ "$ROLE" = "client" ] && [ -z "$MASTER_URL" ]; then
  if [ -t 0 ] || [ -c /dev/tty ]; then
    printf "Master URL (e.g. http://master:9000): "
    if [ -c /dev/tty ]; then read -r MASTER_URL < /dev/tty || true; else read -r MASTER_URL || true; fi
  fi
  [ -n "$MASTER_URL" ] || die "Master URL required for Client/DataNode — pass --master-url http://master:9000 or set MIRYMESH_MASTER_URL"
fi

is_lxc() { grep -q "container=lxc" /proc/1/environ 2>/dev/null || ([ -f /run/systemd/container ] && grep -q lxc /run/systemd/container 2>/dev/null) || false; }
if is_lxc; then log "LXC detected — native install (no Docker)"; else log "Not LXC — proceeding native install anyway (Docker not required)"; fi
log "Role: $ROLE${MASTER_URL:+ (master: $MASTER_URL)}"

# Resolve repo dir
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
  mkdir -p /usr/share/mirymesh; rm -rf /usr/share/mirymesh/static
  cp -a "$SRC/web/static" /usr/share/mirymesh/static
  if [ -f "$SRC/packaging/systemd/mirymesh-controller.service" ]; then
    sed 's|/usr/bin/mirymesh-controller|/usr/local/bin/mirymesh-controller|g' "$SRC/packaging/systemd/mirymesh-controller.service" > /etc/systemd/system/mirymesh-controller.service
    sed 's|/usr/bin/mirymesh-datanode|/usr/local/bin/mirymesh-datanode|g' "$SRC/packaging/systemd/mirymesh-datanode.service" > /etc/systemd/system/mirymesh-datanode.service
  fi
  log "Installed to /usr/local/bin"
}

install_deb() {
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

# DB check (Master only) — PostgreSQL or MySQL/MariaDB required
if [ "$ROLE" = "master" ] && [ "${SKIP_DB_CHECK:-}" != "1" ]; then
  _skip=false
  for a in "$@"; do [ "$a" = "--skip-db-check" ] && _skip=true; done
  if [ "$_skip" = "true" ] || [ "${MIRYMESH_SKIP_DB_CHECK:-}" = "1" ]; then
    log "DB check skipped (--skip-db-check)"
  else
    has_pg=false; has_mysql=false
    command -v psql >/dev/null 2>&1 && has_pg=true
    command -v mysql >/dev/null 2>&1 && has_mysql=true
    command -v mariadb >/dev/null 2>&1 && has_mysql=true
    if [ "$has_pg" = "false" ] && [ "$has_mysql" = "false" ]; then
      echo "[install] PostgreSQL or MySQL/MariaDB is required — install one then rerun (apt: postgresql, dnf: postgresql-server / mariadb-server)" >&2
      echo "       or: $0 --skip-db-check  to skip this check" >&2
      exit 1
    fi
    # Auto-create DB if possible (interactive or env)
    db_user="${MIRYMESH_DB_USER:-}"; db_pass="${MIRYMESH_DB_PASSWORD:-}"; db_name="${MIRYMESH_DB_NAME:-}"; db_driver="${MIRYMESH_DB_DRIVER:-}"
    for a in "$@"; do case "$a" in --db-user=*) db_user="${a#--db-user=}";; --db-password=*) db_pass="${a#--db-password=}";; --db-name=*) db_name="${a#--db-name=}";; --db-driver=*) db_driver="${a#--db-driver=}";; esac; done
    if [ -t 0 ] || [ -c /dev/tty ]; then
      # Interactive prompts for DB
      if [ -z "$db_user" ]; then printf "DB user [mirymesh]: "; if [ -c /dev/tty ]; then read -r tmp < /dev/tty || true; else read -r tmp || true; fi; db_user="${tmp:-mirymesh}"; fi
      if [ -z "$db_pass" ]; then db_pass="$(openssl rand -hex 12 2>/dev/null || head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')"; log "Generated DB password (will be shown once): $db_pass"; fi
      if [ -z "$db_name" ]; then printf "DB name [mirymesh]: "; if [ -c /dev/tty ]; then read -r tmp < /dev/tty || true; else read -r tmp || true; fi; db_name="${tmp:-mirymesh}"; fi
      if [ -z "$db_driver" ]; then printf "Driver [postgres]: "; if [ -c /dev/tty ]; then read -r tmp < /dev/tty || true; else read -r tmp || true; fi; db_driver="${tmp:-postgres}"; fi
    else
      db_user="${db_user:-mirymesh}"; db_name="${db_name:-mirymesh}"; db_driver="${db_driver:-postgres}"
      if [ -z "$db_pass" ]; then db_pass="$(openssl rand -hex 12 2>/dev/null || echo mirymeshtmp)"; fi
    fi
    # Try to create DB
    if [ "$db_driver" = "postgres" ] || [ "$db_driver" = "postgresql" ]; then
      if [ "$has_pg" = "true" ]; then
        sudo -u postgres psql -tc "SELECT 1 FROM pg_database WHERE datname='$db_name'" 2>/dev/null | grep -q 1 || sudo -u postgres psql -c "CREATE USER $db_user WITH PASSWORD '$db_pass'; CREATE DATABASE $db_name OWNER $db_user;" 2>/dev/null || log "PostgreSQL create skipped (needs sudo -u postgres or existing DB)"
        log "PostgreSQL DSN will be postgres://$db_user:***@127.0.0.1:5432/$db_name?sslmode=disable"
        # Saved to DB via setup (miry_setup.db_dsn_enc) — not plain config
        DB_DSN="postgres://$db_user:$db_pass@127.0.0.1:5432/$db_name?sslmode=disable"
        export DB_DSN
        echo "DSN password: $db_pass (save it!)"
      fi
    elif [ "$db_driver" = "mysql" ] || [ "$db_driver" = "mariadb" ]; then
      if [ "$has_mysql" = "true" ]; then
        mysql -u root -e "CREATE DATABASE IF NOT EXISTS $db_name; CREATE USER IF NOT EXISTS '$db_user'@'localhost' IDENTIFIED BY '$db_pass'; GRANT ALL ON $db_name.* TO '$db_user'@'localhost'; FLUSH PRIVILEGES;" 2>/dev/null || log "MySQL create: try mysql -u root -p then rerun or use --skip-db-check"
        DB_DSN="$db_user:$db_pass@tcp(127.0.0.1:3306)/$db_name"
        export DB_DSN
        echo "DSN password: $db_pass"
      fi
    fi
  fi
fi

# Config seeding (0600, only if missing — same as postinstall.sh)
mkdir -p /etc/mirymesh /var/lib/mirymesh /var/log/mirymesh
chown mirymesh:mirymesh /etc/mirymesh 2>/dev/null || true
chown mirymesh:mirymesh /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
chmod 0750 /etc/mirymesh /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
if [ ! -f /etc/mirymesh/config.yaml ] && [ -f "$SRC/packaging/config/config.yaml.example" ]; then
  cp "$SRC/packaging/config/config.yaml.example" /etc/mirymesh/config.yaml
  chmod 0600 /etc/mirymesh/config.yaml 2>/dev/null || true
  chown mirymesh:mirymesh /etc/mirymesh/config.yaml 2>/dev/null || chgrp mirymesh /etc/mirymesh/config.yaml 2>/dev/null || chmod 0644 /etc/mirymesh/config.yaml 2>/dev/null || true
  chown mirymesh:mirymesh /etc/mirymesh 2>/dev/null || true
  chown mirymesh:mirymesh /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
  log "Created /etc/mirymesh/config.yaml from example (0600)"

  if [ "${MASTER_GATEWAY:-no}" = "yes" ]; then
    if grep -q "^  mode:" /etc/mirymesh/config.yaml 2>/dev/null; then
      sed -i "s/^  mode:.*/  mode: gateway/" /etc/mirymesh/config.yaml
    else
      sed -i "/^server:/a\  mode: gateway" /etc/mirymesh/config.yaml 2>/dev/null || printf "  mode: gateway\n" >> /etc/mirymesh/config.yaml
    fi
    log "Gateway mode: wrote mode: gateway to /etc/mirymesh/config.yaml — DataNode 登録がないと Put は 507 になります"
  fi
  if [ "$ROLE" = "client" ] && [ -n "$MASTER_URL" ]; then
    # Inject master URL into config for DataNode
    if grep -q "controller_url:" /etc/mirymesh/config.yaml 2>/dev/null; then
      sed -i "s|controller_url:.*|controller_url: \"$MASTER_URL\"|g" /etc/mirymesh/config.yaml
    else
      printf "\ndatanode:\n  controller_url: \"%s\"\n" "$MASTER_URL" >> /etc/mirymesh/config.yaml
    fi
    log "DataNode config: controller_url set to $MASTER_URL"
  fi
elif [ ! -f /etc/mirymesh/config.yaml ]; then
  log "No config at /etc/mirymesh/config.yaml — create one from packaging/config/config.yaml.example"
else
  if [ "$ROLE" = "client" ] && [ -n "$MASTER_URL" ]; then
    if grep -q "controller_url:" /etc/mirymesh/config.yaml 2>/dev/null; then
      sed -i "s|controller_url:.*|controller_url: \"$MASTER_URL\"|g" /etc/mirymesh/config.yaml
    else
      printf "\ndatanode:\n  controller_url: \"%s\"\n" "$MASTER_URL" >> /etc/mirymesh/config.yaml
    fi
    log "DataNode config: controller_url updated to $MASTER_URL"
  fi
fi

# Edit hint
if [ "$ROLE" = "client" ]; then
  log "Client/DataNode: also set datanode.paths in /etc/mirymesh/config.yaml to your disk mounts (e.g. [\"/mnt/disk1\"])"
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload 2>/dev/null || true
  if [ "${NO_ENABLE:-}" != "1" ]; then
    if [ "$ROLE" = "master" ]; then sudo systemctl enable --now mirymesh-controller 2>/dev/null || true; log "Auto-enabled mirymesh-controller"; else sudo systemctl enable --now mirymesh-datanode 2>/dev/null || true; log "Auto-enabled mirymesh-datanode"; fi
    sleep 2
    if curl -sf http://127.0.0.1:9001/api/admin/setup/status >/dev/null 2>&1; then log "Controller up (9001/setup/status ok)"; else log "Controller not yet up — check journalctl -u mirymesh-controller -n 20"; journalctl -u mirymesh-controller -n 20 2>/dev/null | head -20 || true; fi
  fi
  if [ "$ROLE" = "master" ]; then
    log "To start Master: sudo systemctl enable --now mirymesh-controller && xdg-open http://127.0.0.1:9000/  # setup redirects to http://127.0.0.1:9001/static/setup.html (S3 0.0.0.0:9000 / Admin 0.0.0.0:9001, Tailscale <tailscale-ip>:900x, firewall/Tailscale ACL required)"
    log "Setup:    Master 初回はブラウザで http://<tailscale-ip>:9001/static/setup.html または http://127.0.0.1:9001/static/setup.html で AccessKey/Secret/Policy を入力 (Token は localStorage に保存, 0.0.0.0:9001 は public に晒さない)"
    log "Status:   systemctl status mirymesh-controller; journalctl -u mirymesh-controller -f"
    log "Health:   curl -f http://127.0.0.1:9000/healthz                          # S3 0.0.0.0:9000 (Tailscale <tailscale-ip>:9000)"
    log "Admin:    curl -H \"X-Admin-Token: \$TOKEN\" http://127.0.0.1:9001/api/admin/health  # Admin 0.0.0.0:9001 (Tailscale <tailscale-ip>:9001, firewall required)"
  else
    log "To start Client: sudo systemctl enable --now mirymesh-datanode"
    log "Status:   systemctl status mirymesh-datanode; journalctl -u mirymesh-datanode -f"
    log "Check Master: curl -H \"X-Admin-Token: \$TOKEN\" http://<tailscale-ip>:9001/api/admin/nodes  # from Tailscale or 127.0.0.1:9001 (admin 0.0.0.0:9001)"
    log "DataNode: listen 0.0.0.0:9002 (Tailscale <tailscale-ip>:9002), firewall/Tailscale ACL required"
  fi
else
  if [ "$ROLE" = "master" ]; then
    log "No systemctl — run directly: /usr/local/bin/mirymesh-controller /etc/mirymesh/config.yaml"
  else
    log "No systemctl — run directly: /usr/local/bin/mirymesh-datanode /etc/mirymesh/config.yaml"
  fi
fi

log "Done ($ROLE)."
