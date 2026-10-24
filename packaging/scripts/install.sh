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
# Update an existing install (role is detected, config and data paths are kept):
#   sudo bash packaging/scripts/install.sh --update [--version v1.0.0-rc.1]
# Never prompt (for scripts / automation): add --yes
#
# Env overrides:
#   MIRYMESH_MASTER_URL=http://master:9000  # Client で Master を指定
#   MIRYMESH_ROLE=master|client             # 対話をスキップ
set -eu

MODE=""            # 空 = 自動: --client はリリースのバイナリ、--master はソースビルド
MODE_AUTO=""
VERSION=""
SKIP_DB_CHECK="${SKIP_DB_CHECK:-}"
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

PAIR_TOKEN=""
UPDATE=""
ASSUME_YES=""
SELF_UPDATE="1"
# "--opt VALUE" と "--opt=VALUE" の両方を受け付ける。前者を後者に書き換えて、後段の case で扱う。
_left=$#
while [ "$_left" -gt 0 ]; do
  _opt="$1"; shift; _left=$((_left - 1))
  case "$_opt" in
    --master-url|--pair-token|--path|--repo|--version)
      if [ "$_left" -eq 0 ]; then echo "usage: $_opt VALUE" >&2; exit 1; fi
      set -- "$@" "$_opt=$1"; shift; _left=$((_left - 1)) ;;
    *) set -- "$@" "$_opt" ;;
  esac
done
for arg in "$@"; do
  case "$arg" in
    --master|--controller) ROLE="master" ;;
    --client|--datanode|--data-node) ROLE="client" ;;
    --deb) MODE="deb" ;;
    --rpm) MODE="rpm" ;;
    --from-source|--source) MODE="from-source" ;;
    --master-url=*) MASTER_URL="${arg#--master-url=}" ;;
    --master-url) echo "usage: --master-url URL" >&2; exit 1 ;;
    --pair-token=*) PAIR_TOKEN="${arg#--pair-token=}" ;;
    --pair-token) echo "usage: --pair-token TOKEN" >&2; exit 1 ;;
    --path=*) DATANODE_PATHS="${DATANODE_PATHS:-} ${arg#--path=}" ;;
    --repo) echo "usage: --repo DIR" >&2; exit 1 ;;
    --repo=*) REPO="${arg#--repo=}" ;;
    --no-enable) NO_ENABLE=1 ;;
    --skip-db-check) SKIP_DB_CHECK=1 ;;
    --release) MODE="release" ;;
    --version=*) VERSION="${arg#--version=}" ;;
    --update|--upgrade) UPDATE=1 ;;
    --yes|-y) ASSUME_YES=1 ;;
    --no-self-update) SELF_UPDATE=0 ;;
    --help|-h)
      echo "usage: $0 [--master|--client] [--master-url URL] [--pair-token TOKEN] [--path /mnt/disk1] [--deb|--rpm|--from-source] [--repo DIR]"
      echo "  --master   Install as Master (controller, S3 API)"
      echo "  --client   Install as Client/DataNode (needs --master-url or MIRYMESH_MASTER_URL)"
      echo "  --pair-token  One-time pairing token (from POST /api/admin/pair-token)"
      echo "  --path     Datanode path (repeatable). Default: /var/lib/mirymesh/data"
      echo "  --release  Install the prebuilt DataNode from GitHub Releases (default for --client)"
      echo "  --version  Release tag used by --release, e.g. v0.1.0 (default: latest)"
      echo "  --from-source  Build with Go (default for --master)"
      echo "  --skip-db-check  Master: skip the PostgreSQL/MySQL presence check"
      echo "  --update   Update an existing install in place (role and config are kept; --version TAG pins the release)"
      echo "  --yes, -y  Never prompt, use defaults (for scripts)"
      echo "  --no-self-update  DataNode: do not enable one-click update from the Master"
      echo "  No args    Interactive selection (Master / Client)"
      exit 0
      ;;
    *) echo "unknown arg: $arg" >&2; echo "try: $0 --help" >&2; exit 1 ;;
  esac
done

log() { printf "[install] %s\n" "$*"; }
die() { printf "error: %s\n" "$*" >&2; exit 1; }

# Prompts use the terminal when there is one (also under "curl | sudo bash").
# --yes and --update never ask.
[ -z "$UPDATE" ] || { ASSUME_YES=1; SKIP_DB_CHECK=1; }
HAVE_TTY=""
if [ "$ASSUME_YES" != "1" ]; then
  if [ -t 0 ]; then
    HAVE_TTY="stdin"
  elif ( : </dev/tty ) 2>/dev/null; then
    HAVE_TTY="tty"
  fi
fi
interactive() { [ -n "$HAVE_TTY" ]; }
# ask VAR "prompt" [default] — stores the answer (or the default) in VAR.
ask() {
  _a_var="$1"; _a_prompt="$2"; _a_def="${3:-}"; _a_ans=""
  if [ -n "$_a_def" ]; then printf "%s [%s]: " "$_a_prompt" "$_a_def"; else printf "%s: " "$_a_prompt"; fi
  if [ "$HAVE_TTY" = "tty" ]; then read -r _a_ans </dev/tty || true; else read -r _a_ans || true; fi
  [ -n "$_a_ans" ] || _a_ans="$_a_def"
  eval "$_a_var=\$_a_ans"
}

# --update: find out what is installed here.
if [ -n "$UPDATE" ] && [ -z "$ROLE" ]; then
  if [ -f /etc/systemd/system/mirymesh-controller.service ] || [ -x /usr/local/bin/mirymesh-controller ] || [ -x /usr/bin/mirymesh-controller ]; then
    ROLE="master"
  elif [ -f /etc/systemd/system/mirymesh-datanode.service ] || [ -x /usr/local/bin/mirymesh-datanode ] || [ -x /usr/bin/mirymesh-datanode ]; then
    ROLE="client"
  else
    die "--update: no MiryMesh install found on this host (run without --update to install)"
  fi
  log "Update: detected role $ROLE (pass --master or --client to override)"
fi

# Interactive selection if no role given
if [ -z "$ROLE" ]; then
  if interactive; then
    printf "MiryMesh role — 1) Master (controller, S3 API)  2) Client (DataNode)\n"
    ask ans "Select 1 or 2" "1"
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
MASTER_GATEWAY="no"
if [ "$ROLE" = "master" ] && interactive; then
  ask ans "Gateway mode? y = Master holds no S3 objects, everything is stored on DataNodes (y/N)" "N"
  case "$ans" in
    y|Y|yes|YES)
      MASTER_GATEWAY="yes"
      log "Gateway mode selected — Master will hold no S3 objects. Register at least one DataNode or Put will be 507."
      ;;
  esac
fi

# For client: master URL, pair token and data paths
if [ "$ROLE" = "client" ] && [ -z "$UPDATE" ]; then
  if [ -z "$MASTER_URL" ] && interactive; then
    ask MASTER_URL "Master Admin URL (e.g. http://master:9001)"
  fi
  [ -n "$MASTER_URL" ] || die "Master URL required for Client/DataNode — pass --master-url http://master:9001 or set MIRYMESH_MASTER_URL"
  MASTER_URL="${MASTER_URL%/}"
  if [ -z "$PAIR_TOKEN" ] && interactive; then
    ask PAIR_TOKEN "Pair token (Nodes -> add node in the Master UI; leave empty to pair later)"
  fi
  if [ -z "${DATANODE_PATHS:-}" ]; then
    if interactive; then
      ask _paths "Data directory (separate several disks with spaces)" "/var/lib/mirymesh/data"
      DATANODE_PATHS="$_paths"
    else
      DATANODE_PATHS="/var/lib/mirymesh/data"
      log "No --path given — storing objects in $DATANODE_PATHS (use --path /mnt/diskN for a dedicated disk)"
    fi
  fi
fi

if interactive; then
  log "Plan: role=$ROLE${MASTER_URL:+ master=$MASTER_URL}${DATANODE_PATHS:+ paths=$DATANODE_PATHS}"
  ask ans "Proceed? (Y/n)" "Y"
  case "$ans" in n|N|no|NO) die "aborted" ;; esac
fi

is_lxc() { grep -q "container=lxc" /proc/1/environ 2>/dev/null || ([ -f /run/systemd/container ] && grep -q lxc /run/systemd/container 2>/dev/null) || false; }
if is_lxc; then log "LXC detected — native install (no Docker)"; else log "Not LXC — proceeding native install anyway (Docker not required)"; fi
log "Role: $ROLE${MASTER_URL:+ (master: $MASTER_URL)}"

# Install method: --client uses the prebuilt release by default, everything else builds from source.
if [ -z "$MODE" ]; then
  if [ "$ROLE" = "client" ]; then MODE="release"; MODE_AUTO=1; else MODE="from-source"; fi
fi

RELEASE_BASE="${MIRYMESH_RELEASE_BASE:-https://github.com/sylphre/MiryMesh/releases}"

# The newest tag including pre-releases (GitHub's "latest" skips them, so v1.0.0-rc.1 would 404).
resolve_latest_tag() {
  _api="${MIRYMESH_RELEASE_API:-https://api.github.com/repos/sylphre/MiryMesh/releases?per_page=1}"
  curl -fsSL "$_api" 2>/dev/null | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1
}

# Download the static DataNode binary, verify its sha256 and install it.
# Returns 1 when no usable release asset exists (the caller decides whether to fall back).
install_release() {
  case "$(uname -m)" in
    x86_64|amd64)  _arch="amd64" ;;
    aarch64|arm64) _arch="arm64" ;;
    *) log "No prebuilt DataNode for CPU $(uname -m)"; return 1 ;;
  esac
  command -v curl >/dev/null 2>&1 || { log "curl not found"; return 1; }
  command -v sha256sum >/dev/null 2>&1 || { log "sha256sum not found"; return 1; }
  if [ -z "$VERSION" ] && ! curl -fsSIL -o /dev/null "$RELEASE_BASE/latest/download/SHA256SUMS" 2>/dev/null; then
    _t="$(resolve_latest_tag || true)"
    case "$_t" in
      v[0-9]*) VERSION="$_t"; log "No stable release yet — using the newest pre-release $VERSION" ;;
    esac
  fi
  if [ -n "$VERSION" ]; then _url="$RELEASE_BASE/download/$VERSION"; else _url="$RELEASE_BASE/latest/download"; fi
  _name="mirymesh-datanode-linux-$_arch"
  _tmp="$(mktemp -d)" || return 1
  log "Downloading $_url/$_name"
  if ! curl -fsSL -o "$_tmp/$_name" "$_url/$_name" || ! curl -fsSL -o "$_tmp/SHA256SUMS" "$_url/SHA256SUMS"; then
    rm -rf "$_tmp"
    return 1
  fi
  _want="$(awk -v n="$_name" '$2 == n || $2 == ("*" n) { print $1 }' "$_tmp/SHA256SUMS")"
  _got="$(sha256sum "$_tmp/$_name" | awk '{ print $1 }')"
  if [ -z "$_want" ] || [ "$_want" != "$_got" ]; then
    rm -rf "$_tmp"
    die "checksum verification failed for $_name — refusing to install"
  fi
  install -m 0755 "$_tmp/$_name" /usr/local/bin/mirymesh-datanode || { rm -rf "$_tmp"; return 1; }
  rm -rf "$_tmp"
  log "Installed /usr/local/bin/mirymesh-datanode (sha256 verified, version: $(/usr/local/bin/mirymesh-datanode --version 2>/dev/null || echo unknown))"
}

if [ "$MODE" = "release" ]; then
  if [ "$ROLE" != "client" ]; then
    die "--release only supports --client (Master: use --deb, --rpm or --from-source)"
  fi
  if install_release; then
    :
  elif [ "$MODE_AUTO" = "1" ]; then
    log "Prebuilt DataNode not available — building from source instead (needs git and Go)"
    MODE="from-source"
  else
    die "could not download the DataNode release — try --from-source"
  fi
fi

# Resolve repo dir
if [ "$MODE" = "release" ]; then
  SRC=""
elif [ -n "$REPO" ]; then
  SRC="$REPO"
elif [ -f ./go.mod ] && [ -f ./packaging/nfpm.yaml ]; then
  SRC="$(pwd)"
else
  command -v git >/dev/null 2>&1 || die "git not found — install git then rerun"
  SRC="$(mktemp -d /tmp/mirymesh-src.XXXXXX)"
  trap 'rm -rf "$SRC"' EXIT
  log "Cloning MiryMesh${VERSION:+ $VERSION} to $SRC"
  if [ -n "$VERSION" ]; then
    git clone --depth 1 --branch "$VERSION" https://github.com/sylphre/MiryMesh "$SRC"
  else
    git clone --depth 1 https://github.com/sylphre/MiryMesh "$SRC"
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
  _ldf=""
  [ -z "$VERSION" ] || _ldf="-X github.com/sylphre/MiryMesh/internal/version.Version=$VERSION"
  (cd "$SRC" && go build -trimpath -ldflags="$_ldf" -o /tmp/mirymesh-controller ./cmd/controller)
  (cd "$SRC" && go build -trimpath -ldflags="$_ldf" -o /tmp/mirymesh-datanode   ./cmd/datanode)
  (cd "$SRC" && go build -trimpath -ldflags="$_ldf" -o /tmp/mirymesh            ./cmd/mirymesh)
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

# Files that the DEB/RPM or the repo checkout normally provide, for the binary-only (release) install.
# Keep the unit in sync with packaging/systemd/mirymesh-datanode.service.
prepare_release_files() {
  mkdir -p /etc/mirymesh /var/lib/mirymesh /var/log/mirymesh
  chown mirymesh:mirymesh /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
  chmod 0750 /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
  if [ ! -f /etc/mirymesh/config.yaml ]; then
    cat > /etc/mirymesh/config.yaml <<CONF
datanode:
  listen: "0.0.0.0:9002"
  controller_url: "$MASTER_URL"
  paths: []
log:
  level: "info"
CONF
    chmod 0600 /etc/mirymesh/config.yaml
    chown mirymesh:mirymesh /etc/mirymesh/config.yaml 2>/dev/null || true
  fi
  cat > /etc/systemd/system/mirymesh-datanode.service <<'UNIT'
[Unit]
Description=MiryMesh Data Node (0.0.0.0:9002 — Tailscale <tailscale-ip>:9002 or 127.0.0.1; do not expose to public)
After=network.target

[Service]
Type=simple
User=mirymesh
Group=mirymesh
ExecStart=/usr/local/bin/mirymesh-datanode /etc/mirymesh/config.yaml
Restart=on-failure
RestartSec=2
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/mirymesh /var/log/mirymesh

[Install]
WantedBy=multi-user.target
UNIT
}

# Path of the installed DataNode binary (taken from the unit, so DEB/RPM and /usr/local both work).
datanode_bin() {
  for f in /etc/systemd/system/mirymesh-datanode.service /lib/systemd/system/mirymesh-datanode.service /usr/lib/systemd/system/mirymesh-datanode.service; do
    if [ -f "$f" ]; then
      sed -n 's/^ExecStart=\([^ ]*\).*/\1/p' "$f" | head -1
      return 0
    fi
  done
  echo "/usr/local/bin/mirymesh-datanode"
}

# One-click update from the Master: the service downloads and verifies a release binary into
# /var/lib/mirymesh/update and exits. This root-owned helper, run by systemd before the next
# start (ExecStartPre=+), moves it into place. The service itself stays read-only (ProtectSystem=strict).
enable_self_update() {
  _bin="$(datanode_bin)"
  mkdir -p /usr/local/lib/mirymesh /etc/systemd/system/mirymesh-datanode.service.d
  cat > /usr/local/lib/mirymesh/apply-update.sh <<'HELPER'
#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
# Written by install.sh. Runs as root (ExecStartPre=+) before mirymesh-datanode starts and
# moves a binary that the DataNode downloaded and sha256-verified into place.
set -eu
TARGET="${1:?usage: apply-update.sh /path/to/mirymesh-datanode}"
NEW="${MIRYMESH_STAGE_DIR:-/var/lib/mirymesh/update}/mirymesh-datanode"
[ -e "$NEW" ] || [ -L "$NEW" ] || exit 0
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
# Take the entry out of the service-writable directory first, then inspect our own copy.
mv -f "$NEW" "$WORK/new"
if [ -L "$WORK/new" ] || [ ! -f "$WORK/new" ]; then
  echo "apply-update: staged file is not a regular file, ignoring" >&2
  exit 0
fi
if [ "$(head -c 4 "$WORK/new" | od -An -tx1 | tr -d ' \n')" != "7f454c46" ]; then
  echo "apply-update: staged file is not an ELF binary, ignoring" >&2
  exit 0
fi
install -m 0755 -o root -g root "$WORK/new" "$TARGET.new"
[ ! -f "$TARGET" ] || cp -p "$TARGET" "$TARGET.prev"
mv -f "$TARGET.new" "$TARGET"
echo "apply-update: installed the staged DataNode into $TARGET (previous binary: $TARGET.prev)"
HELPER
  chmod 0755 /usr/local/lib/mirymesh/apply-update.sh
  cat > /etc/systemd/system/mirymesh-datanode.service.d/30-self-update.conf <<CONF
# Written by install.sh (skip with --no-self-update). Lets the Master update this node from its console.
[Service]
Environment=MIRYMESH_SELF_UPDATE=1
ExecStartPre=+/usr/local/lib/mirymesh/apply-update.sh $_bin
CONF
  log "Self-update enabled: this node can be updated from the Nodes page of the Master"
}

case "$MODE" in
  release)     prepare_release_files ;;
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
    if interactive; then
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

if [ "$ROLE" = "client" ] && [ -n "${DATANODE_PATHS:-}" ]; then
  # Build JSON array once: ["p1","p2",...]
  _paths_json=""
  for p in $DATANODE_PATHS; do
    if [ -n "$_paths_json" ]; then _paths_json="$_paths_json, \"$p\""; else _paths_json="\"$p\""; fi
  done
  _paths_json="[$_paths_json]"
  if grep -q "paths:" /etc/mirymesh/config.yaml 2>/dev/null; then
    # Replace whole paths line once
    sed -i "s|paths:.*|paths: $_paths_json|" /etc/mirymesh/config.yaml 2>/dev/null || true
  else
    # Append datanode.paths under datanode section
    if grep -q "^datanode:" /etc/mirymesh/config.yaml 2>/dev/null; then
      sed -i "/^datanode:/a\  paths: $_paths_json" /etc/mirymesh/config.yaml 2>/dev/null || printf "  paths: %s\n" "$_paths_json" >> /etc/mirymesh/config.yaml
    else
      printf "\ndatanode:\n  paths: %s\n" "$_paths_json" >> /etc/mirymesh/config.yaml
    fi
  fi
  log "DataNode paths: $DATANODE_PATHS -> $_paths_json"
fi
# DataNode: the unit uses ProtectSystem=strict, so everything outside ReadWritePaths is read-only.
# List the data paths explicitly, and wait for their mounts before starting.
if [ "$ROLE" = "client" ] && [ -n "${DATANODE_PATHS:-}" ]; then
  _rw=""
  for p in $DATANODE_PATHS; do
    mkdir -p "$p"
    chown mirymesh:mirymesh "$p" 2>/dev/null || true
    _rw="$_rw $p"
  done
  mkdir -p /etc/systemd/system/mirymesh-datanode.service.d
  cat > /etc/systemd/system/mirymesh-datanode.service.d/20-data-paths.conf <<CONF
# Written by install.sh. Run it again with --path to change the list.
[Service]
ReadWritePaths=$_rw
RequiresMountsFor=$_rw
CONF
  log "Data paths made writable for the service:$_rw"
fi

if [ "$ROLE" = "client" ]; then
  if [ "$SELF_UPDATE" = "1" ]; then
    enable_self_update
  else
    rm -f /etc/systemd/system/mirymesh-datanode.service.d/30-self-update.conf
  fi
fi

# Pair token: passed through a drop-in + EnvironmentFile instead of editing the unit.
# It is removed again after the first successful pairing (see below).
if [ "$ROLE" = "client" ] && [ -n "${PAIR_TOKEN:-}" ]; then
  mkdir -p /etc/mirymesh /etc/systemd/system/mirymesh-datanode.service.d
  ( umask 077; printf 'PAIR_TOKEN=%s\n' "$PAIR_TOKEN" > /etc/mirymesh/datanode.env )
  _dn_bin="$(for f in /etc/systemd/system/mirymesh-datanode.service /lib/systemd/system/mirymesh-datanode.service /usr/lib/systemd/system/mirymesh-datanode.service; do
    if [ -f "$f" ]; then sed -n 's/^ExecStart=\([^ ]*\).*/\1/p' "$f" | head -1; break; fi
  done)"
  [ -n "$_dn_bin" ] || _dn_bin="/usr/local/bin/mirymesh-datanode"
  cat > /etc/systemd/system/mirymesh-datanode.service.d/10-pair-token.conf <<CONF
# Written by install.sh. Removed again once the node is paired.
[Service]
EnvironmentFile=/etc/mirymesh/datanode.env
ExecStart=
ExecStart=$_dn_bin --pair-token \${PAIR_TOKEN} /etc/mirymesh/config.yaml
CONF
  log "Pair token configured (one-time; removed after the first successful pairing)"
fi
# Edit hint
if [ "$ROLE" = "client" ]; then
  log "Client/DataNode: data paths -> ${DATANODE_PATHS:-(none)} (change with --path, or datanode.paths in /etc/mirymesh/config.yaml)"
  if [ -z "${PAIR_TOKEN:-}" ]; then
    log "Hint: get pair token via: curl -H \"X-Admin-Token: \$ADMIN_TOKEN\" http://master:9001/api/admin/pair-token -X POST"
  fi
fi

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload 2>/dev/null || true
  if [ "${NO_ENABLE:-}" != "1" ]; then
    if [ "$ROLE" = "master" ]; then systemctl enable mirymesh-controller 2>/dev/null || true; systemctl restart mirymesh-controller 2>/dev/null || true; log "Auto-enabled and (re)started mirymesh-controller"; else systemctl enable mirymesh-datanode 2>/dev/null || true; systemctl restart mirymesh-datanode 2>/dev/null || true; log "Auto-enabled and (re)started mirymesh-datanode"; fi
    sleep 2
    if [ "$ROLE" != "master" ]; then
      if [ -n "${PAIR_TOKEN:-}" ]; then
        _tf=/var/lib/mirymesh/datanode.token
        _i=0
        while [ "$_i" -lt 20 ] && [ ! -s "$_tf" ]; do sleep 1; _i=$((_i + 1)); done
        if [ -s "$_tf" ]; then
          rm -f /etc/mirymesh/datanode.env /etc/systemd/system/mirymesh-datanode.service.d/10-pair-token.conf
          systemctl daemon-reload 2>/dev/null || true
          log "Paired with the Master. The one-time pair token has been removed from this host."
        else
          log "Not paired yet. Is the token older than 15 minutes, or is the Master URL unreachable from here?"
          journalctl -u mirymesh-datanode -n 15 --no-pager 2>/dev/null || true
        fi
      fi
    elif curl -sf http://127.0.0.1:9001/api/admin/setup/status >/dev/null 2>&1; then log "Controller up (9001/setup/status ok)"; else log "Controller not yet up — check journalctl -u mirymesh-controller -n 20"; journalctl -u mirymesh-controller -n 20 2>/dev/null | head -20 || true; fi
  fi
  if [ "$ROLE" = "master" ]; then
    log "To start Master: sudo systemctl enable --now mirymesh-controller && xdg-open http://127.0.0.1:9000/  # setup redirects to http://127.0.0.1:9001/static/setup.html (S3 0.0.0.0:9000 / Admin 0.0.0.0:9001, Tailscale <tailscale-ip>:900x, firewall/Tailscale ACL required)"
    log "Setup:    Master 初回はブラウザで http://<tailscale-ip>:9001/static/setup.html または http://127.0.0.1:9001/static/setup.html で AccessKey/Secret/Policy を入力 (Token は localStorage に保存, 0.0.0.0:9001 は public に晒さない)"
    log "Status:   systemctl status mirymesh-controller; journalctl -u mirymesh-controller -f"
    log "Health:   curl -f http://127.0.0.1:9000/healthz                          # S3 0.0.0.0:9000 (Tailscale <tailscale-ip>:9000)"
    log "Admin:    curl -H \"X-Admin-Token: \$TOKEN\" http://127.0.0.1:9001/api/admin/health  # Admin 0.0.0.0:9001 (Tailscale <tailscale-ip>:9001, firewall required)"
  else
    log "To start Client: sudo systemctl enable --now mirymesh-datanode"
    log "Node ID:  $(hostname) — the Master must reach this node as $(hostname):9002 (Tailscale MagicDNS, DNS or /etc/hosts)"
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

if [ -n "$UPDATE" ]; then log "Update finished ($ROLE)."; else log "Done ($ROLE)."; fi
