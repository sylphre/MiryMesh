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
# Run interactively on a host that already has MiryMesh, only "Update" is offered.
#
# Branches: dev > prerelease > release. Pre-releases (rc) are published from `prerelease`,
# stable releases from `release`.
#   sudo bash packaging/scripts/install.sh --prerelease                 # follow the prerelease channel
#   sudo bash packaging/scripts/install.sh --update --channel release   # back to the stable channel
# An update stays on the channel the host is on (MIRYMESH_CHANNEL=release|prerelease also works).
#
# Dev (build from source). The default branch is `release` (`prerelease` with --prerelease):
#   sudo bash packaging/scripts/install.sh --dev [--branch NAME]
# A Master source build also builds the web console (frontend/, Vue + Vite) and embeds it in the
# controller. This needs Node.js 22+ and npm on the host; the script does not install them and
# stops with an error if they are missing.
# Never prompt (for scripts / automation): add --yes
#
# Env overrides:
#   MIRYMESH_MASTER_URL=http://master:9000  # Client で Master を指定
#   MIRYMESH_ROLE=master|client             # 対話をスキップ
#   MIRYMESH_BRANCH=release                 # ソースビルドのブランチ (既定: release)
set -eu

MODE=""            # 空 = 自動: --client はリリースのバイナリ、--master はソースビルド
MODE_AUTO=""
VERSION=""
SKIP_DB_CHECK="${SKIP_DB_CHECK:-}"
ROLE=""
REPO=""
MASTER_URL="${MIRYMESH_MASTER_URL:-}"
BRANCH="${MIRYMESH_BRANCH:-}"   # ソースビルド時のブランチ。空 = release
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
CHANNEL="${MIRYMESH_CHANNEL:-}"   # release | prerelease. Empty = decided below
CHANNEL_FILE=/etc/mirymesh/channel
ROLE_FILE=/etc/mirymesh/role      # "master" or "client", written by every successful install/update
FORCE_ROLE=""
ALLOW_DOWNGRADE=""
CHECK_ROLE=""                     # --check-role: report the role of this host and stop, changing nothing
VR_TOKEN_BEFORE="no"              # was datanode.token present before this run? (see verify_role)
# "--opt VALUE" と "--opt=VALUE" の両方を受け付ける。前者を後者に書き換えて、後段の case で扱う。
_left=$#
while [ "$_left" -gt 0 ]; do
  _opt="$1"; shift; _left=$((_left - 1))
  case "$_opt" in
    --master-url|--pair-token|--path|--repo|--version|--branch|--channel|--db-driver|--db-user|--db-name|--db-password)
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
    --branch) echo "usage: --branch NAME" >&2; exit 1 ;;
    --branch=*) BRANCH="${arg#--branch=}" ;;
    --channel) echo "usage: --channel release|prerelease" >&2; exit 1 ;;
    --channel=*) CHANNEL="${arg#--channel=}" ;;
    --prerelease) CHANNEL="prerelease" ;;
    --dev) MODE="from-source" ;;
    --no-enable) NO_ENABLE=1 ;;
    --force-role) FORCE_ROLE=1 ;;
    --allow-downgrade) ALLOW_DOWNGRADE=1 ;;
    --check-role) CHECK_ROLE=1; ASSUME_YES=1 ;;
    --skip-db-check) SKIP_DB_CHECK=1 ;;
    --db-driver=*|--db-user=*|--db-name=*|--db-password=*) ;; # read again in the Master DB step
    --release) MODE="release" ;;
    --version=*) VERSION="${arg#--version=}" ;;
    --update|--upgrade) UPDATE=1 ;;
    --yes|-y) ASSUME_YES=1 ;;
    --no-self-update) SELF_UPDATE=0 ;;
    --help|-h)
      echo "usage: $0 [--master|--client] [--master-url URL] [--pair-token TOKEN] [--path /mnt/disk1] [--deb|--rpm|--from-source|--dev] [--branch NAME] [--prerelease] [--update] [--repo DIR]"
      echo "  --master   Install as Master (controller, S3 API)"
      echo "  --client   Install as Client/DataNode (needs --master-url or MIRYMESH_MASTER_URL)"
      echo "  --pair-token  One-time pairing token (from POST /api/admin/pair-token)"
      echo "  --path     Datanode path (repeatable). Default: /var/lib/mirymesh/data"
      echo "  --release  Install the prebuilt DataNode from GitHub Releases (default for --client)"
      echo "  --version  Release tag used by --release, e.g. v0.1.0 (default: latest)"
      echo "  --from-source  Build with Go (default for --master)"
      echo "  --dev      Dev: build from source (same as --from-source; for --client too)"
      echo "  --branch   Branch used for source builds (default: release, or prerelease with --prerelease; --version TAG wins)"
      echo "  --prerelease  Follow the prerelease channel: newest release including pre-releases, and the prerelease branch for source builds"
      echo "  --channel  release|prerelease. --update keeps the channel the host is on unless this is given"
      echo "  --skip-db-check  Master: skip the PostgreSQL/MySQL presence check"
      echo "  --db-driver postgres|mysql  Master: database to set up when both are installed (default: ask, or postgres with --yes)"
      echo "  --db-user NAME / --db-name NAME / --db-password PASS  Master: role/user, database and password to create (default: mirymesh, mirymesh, random)"
      echo "  --update   Update an existing install in place (role and config are kept; --version TAG pins the release)"
      echo "  --yes, -y  Never prompt, use defaults (for scripts)"
      echo "  --force-role  Allow a role that differs from the one recorded in /etc/mirymesh/role (dangerous)"
      echo "  --allow-downgrade  Let --update install an older build than the one on this host (refused by default)"
      echo "  --check-role  Only report the role of this host (and verify it); change nothing. Add --master/--client to test a role"
      echo "  --no-self-update  DataNode: do not enable one-click update from the Master"
      echo "  No args    Interactive selection (Master / Client). If MiryMesh is already installed: Update only"
      exit 0
      ;;
    *) echo "unknown arg: $arg" >&2; echo "try: $0 --help" >&2; exit 1 ;;
  esac
done

case "$CHANNEL" in
  ""|release|prerelease) ;;
  stable) CHANNEL="release" ;;
  *) echo "unknown channel '$CHANNEL' (release|prerelease)" >&2; exit 1 ;;
esac

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

# --branch only makes sense for a source build.
if [ -z "$MODE" ] && [ -n "$BRANCH" ]; then MODE="from-source"; fi

# Role recorded by a previous install/update ("master" or "client"). Prints nothing when unknown.
recorded_role() {
  [ -f "$ROLE_FILE" ] || return 0
  case "$(head -1 "$ROLE_FILE" 2>/dev/null)" in
    master) echo "master" ;;
    client) echo "client" ;;
  esac
  return 0
}

# What is installed on this host? Prints "master", "client" or nothing.
# The recorded role wins. Hosts installed before the record existed fall back to guessing from files.
detect_installed_role() {
  _dr_r="$(recorded_role)"
  if [ -n "$_dr_r" ]; then echo "$_dr_r"; return 0; fi
  _dr_c=""; _dr_d=""
  if [ -f /etc/systemd/system/mirymesh-controller.service ] || [ -x /usr/local/bin/mirymesh-controller ] || [ -x /usr/bin/mirymesh-controller ]; then
    _dr_c=1
  fi
  if [ -f /etc/systemd/system/mirymesh-datanode.service ] || [ -x /usr/local/bin/mirymesh-datanode ] || [ -x /usr/bin/mirymesh-datanode ]; then
    _dr_d=1
  fi
  if [ -n "$_dr_c" ] && [ -z "$_dr_d" ]; then echo "master"; return 0; fi
  if [ -z "$_dr_c" ] && [ -n "$_dr_d" ]; then echo "client"; return 0; fi
  [ -n "$_dr_c" ] || return 0
  # Both are present. Older source builds put the controller on every host, so the files alone
  # prove nothing. Decide by which service is enabled; if that is unclear, a paired DataNode
  # (datanode.token) means client. Guessing "client" is the safe side: it never starts a controller.
  _dr_ce=""; _dr_de=""
  if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-enabled --quiet mirymesh-controller 2>/dev/null; then _dr_ce=1; fi
    if systemctl is-enabled --quiet mirymesh-datanode 2>/dev/null; then _dr_de=1; fi
  fi
  if [ -n "$_dr_ce" ] && [ -z "$_dr_de" ]; then echo "master"; return 0; fi
  if [ -z "$_dr_ce" ] && [ -n "$_dr_de" ]; then echo "client"; return 0; fi
  if [ -s /var/lib/mirymesh/datanode.token ]; then
    echo "role detection: controller and DataNode are both installed here; treating this host as a DataNode (pass --master to override)" >&2
    echo "client"
  else
    echo "master"
  fi
}
INSTALLED_ROLE="$(detect_installed_role)"

# Version of the installed binary (one line, empty when it cannot be run).
installed_version() {
  for _iv_b in /usr/local/bin/mirymesh-datanode /usr/bin/mirymesh-datanode /usr/local/bin/mirymesh-controller /usr/bin/mirymesh-controller; do
    if [ -x "$_iv_b" ]; then
      if command -v timeout >/dev/null 2>&1; then
        timeout 5 "$_iv_b" --version 2>/dev/null | head -1 || true
      else
        "$_iv_b" --version 2>/dev/null | head -1 || true
      fi
      return 0
    fi
  done
  return 0
}

# Channel of the existing install. Prints "prerelease" when the installed version has a pre-release
# suffix (v1.0.0-rc.1) or the channel recorded by a previous run says so. Prints nothing otherwise.
detect_installed_channel() {
  case "$(installed_version)" in
    v[0-9]*-*) echo "prerelease"; return 0 ;;
  esac
  if [ -f "$CHANNEL_FILE" ] && [ "$(head -1 "$CHANNEL_FILE" 2>/dev/null)" = "prerelease" ]; then
    echo "prerelease"
  fi
  return 0
}

# --- Downgrade guard -------------------------------------------------------------------------
# An update must not put an older build on a host: the database schema and the node token store
# of a newer build no longer match an older binary. Two checks run before anything is installed:
#   1. version: the release / tag to install must not be older than the installed version;
#   2. source builds: the commit installed last time (COMMIT_FILE) must be part of the history of
#      the commit to install. A source build reports the version "dev", so only this catches it.
# --allow-downgrade skips both. Both only run with --update.
COMMIT_FILE=/etc/mirymesh/installed_commit
DOWNGRADE_MSG=""

# Succeeds when the argument looks like MAJOR.MINOR.PATCH[-pre] (with or without a leading v).
is_semver() {
  case "${1#v}" in
    [0-9]*.[0-9]*.[0-9]*) return 0 ;;
  esac
  return 1
}

# semver_lt A B: succeeds when version A is older than version B (a pre-release is older than its release).
semver_lt() {
  _sl_a="${1#v}"; _sl_a="${_sl_a%%+*}"
  _sl_b="${2#v}"; _sl_b="${_sl_b%%+*}"
  _sl_ac="${_sl_a%%-*}"; _sl_bc="${_sl_b%%-*}"
  _sl_ap=""; _sl_bp=""
  case "$_sl_a" in *-*) _sl_ap="${_sl_a#*-}" ;; esac
  case "$_sl_b" in *-*) _sl_bp="${_sl_b#*-}" ;; esac
  if [ "$_sl_ac" != "$_sl_bc" ]; then
    if [ "$(printf '%s\n%s\n' "$_sl_ac" "$_sl_bc" | sort -V | head -1)" = "$_sl_ac" ]; then return 0; fi
    return 1
  fi
  if [ "$_sl_ap" = "$_sl_bp" ]; then return 1; fi
  if [ -z "$_sl_ap" ]; then return 1; fi
  if [ -z "$_sl_bp" ]; then return 0; fi
  if [ "$(printf '%s\n%s\n' "$_sl_ap" "$_sl_bp" | sort -V | head -1)" = "$_sl_ap" ]; then return 0; fi
  return 1
}

# refuse_version_downgrade NEW_VERSION: returns 1 and sets DOWNGRADE_MSG when NEW_VERSION is older
# than the installed one. Versions that are not MAJOR.MINOR.PATCH (for example "dev") are not compared.
refuse_version_downgrade() {
  [ -n "$UPDATE" ] || return 0
  [ -z "$ALLOW_DOWNGRADE" ] || return 0
  _rv_new="$1"
  _rv_old="$(installed_version)"
  is_semver "$_rv_old" || return 0
  is_semver "$_rv_new" || return 0
  if semver_lt "$_rv_new" "$_rv_old"; then
    DOWNGRADE_MSG="downgrade refused: $_rv_old is installed and this update would install $_rv_new. Nothing was changed. Downgrades are not supported (rebuild the host instead); --allow-downgrade overrides this."
    return 1
  fi
  return 0
}

# refuse_source_downgrade SRC_DIR: returns 1 and sets DOWNGRADE_MSG when the commit built last time
# is not contained in the history of SRC_DIR's HEAD. Skipped when no commit was recorded, git is
# missing, or the checkout is shallow (the history needed for the check is not there).
refuse_source_downgrade() {
  [ -n "$UPDATE" ] || return 0
  [ -z "$ALLOW_DOWNGRADE" ] || return 0
  [ -f "$COMMIT_FILE" ] || return 0
  _rs_old="$(head -1 "$COMMIT_FILE" 2>/dev/null || true)"
  case "$_rs_old" in ''|*[!0-9a-f]*) return 0 ;; esac
  command -v git >/dev/null 2>&1 || return 0
  git -C "$1" rev-parse --git-dir >/dev/null 2>&1 || return 0
  [ "$(git -C "$1" rev-parse --is-shallow-repository 2>/dev/null || true)" != "true" ] || return 0
  _rs_new="$(git -C "$1" rev-parse HEAD 2>/dev/null || true)"
  [ -n "$_rs_new" ] || return 0
  [ "$_rs_old" != "$_rs_new" ] || return 0
  if git -C "$1" cat-file -e "${_rs_old}^{commit}" 2>/dev/null \
     && git -C "$1" merge-base --is-ancestor "$_rs_old" "$_rs_new" 2>/dev/null; then
    return 0
  fi
  DOWNGRADE_MSG="downgrade refused: the installed build ($(printf '%.7s' "$_rs_old")) is not part of the history of what this update would install ($(printf '%.7s' "$_rs_new")). The target is older or on another line of history, so it may lack changes this host already runs. Nothing was changed. Downgrades are not supported (rebuild the host instead); --allow-downgrade overrides this."
  return 1
}

# Interactive run on a host that already has MiryMesh: offer Update only.
# Role, gateway and install-type questions are skipped, the run continues like "--update".
# An explicit --master / --client (or MIRYMESH_ROLE) still means a fresh install.
if [ -z "$UPDATE" ] && [ -z "$ROLE" ] && [ -n "$INSTALLED_ROLE" ] && interactive; then
  _v="$(installed_version)"
  printf "MiryMesh is already installed on this host (role: %s%s).\n" "$INSTALLED_ROLE" "${_v:+, $_v}"
  ask ans "Update it now? (Y/n)" "Y"
  case "$ans" in n|N|no|NO) die "aborted" ;; esac
  # Where to update to. This is also how a Dev (source) build moves to Prerelease or Release.
  # Skipped when --channel / --prerelease / --dev / --branch already said it.
  if [ -z "$CHANNEL" ] && [ -z "$MODE" ] && [ -z "$BRANCH" ]; then
    case "$(detect_installed_channel)" in prerelease) _cd=2 ;; *) _cd=1 ;; esac
    printf "Update to — 1) Release (stable)  2) Prerelease (rc)  3) Dev (build a branch from source)\n"
    ask ans "Select 1, 2 or 3" "$_cd"
    case "$ans" in
      2|pre|prerelease|Prerelease|p|P) CHANNEL="prerelease" ;;
      3|dev|Dev|DEV|d|D)
        MODE="from-source"
        ask BRANCH "Branch to build" "release"
        ;;
      *) CHANNEL="release" ;;
    esac
  fi
  UPDATE=1; ASSUME_YES=1; SKIP_DB_CHECK=1; HAVE_TTY=""
fi

# --update: find out what is installed here.
if [ -n "$UPDATE" ] && [ -z "$ROLE" ]; then
  ROLE="$INSTALLED_ROLE"
  [ -n "$ROLE" ] || die "--update: no MiryMesh install found on this host (run without --update to install)"
  log "Update: detected role $ROLE (pass --master or --client to override)"
fi

# --update stays on the channel the host is on, unless --channel / --prerelease says otherwise.
if [ -n "$UPDATE" ] && [ -z "$CHANNEL" ]; then
  CHANNEL="$(detect_installed_channel)"
  [ -z "$CHANNEL" ] || log "Update: this host follows the $CHANNEL channel (leave it with --channel release)"
fi

# --check-role without --master/--client: check the role this host would be updated as.
if [ -n "$CHECK_ROLE" ] && [ -z "$ROLE" ]; then
  ROLE="$INSTALLED_ROLE"
  [ -n "$ROLE" ] || die "--check-role: no MiryMesh install found on this host"
  log "Check: detected role $ROLE"
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

# Role guard: stop before any Master step (DB, controller.env, controller service) can run on a
# DataNode host, or the other way round. Nothing has been changed on the host yet at this point.
if [ -z "$FORCE_ROLE" ]; then
  _rg_rec="$(recorded_role)"
  if [ -n "$_rg_rec" ] && [ "$_rg_rec" != "$ROLE" ]; then
    die "role mismatch: this host is recorded as '$_rg_rec' ($ROLE_FILE) but this run would install it as '$ROLE'. Nothing was changed. Use --$([ "$_rg_rec" = "client" ] && echo client || echo master), or --force-role if you really want to switch."
  fi
  # No record (installed by an older install.sh): a paired DataNode token means this is a DataNode.
  if [ -z "$_rg_rec" ] && [ "$ROLE" = "master" ] && [ -s /var/lib/mirymesh/datanode.token ]; then
    die "role mismatch: /var/lib/mirymesh/datanode.token exists, so this host looks like a DataNode, but this run would install it as 'master'. Nothing was changed. Use --client, or --force-role if this host really is a Master."
  fi
fi

# ---- Role verification (read-only) ----------------------------------------------------------
# Every run checks the role against the facts on the host BEFORE anything is changed: the record,
# the systemd state of both services, the DataNode token and controller.env. The facts are always
# printed, so a wrong decision is visible. A contradiction stops the run (--force-role overrides).
unit_flag() { # unit_flag is-enabled|is-active UNIT -> yes / no
  if command -v systemctl >/dev/null 2>&1 && systemctl "$1" --quiet "$2" 2>/dev/null; then echo yes; else echo no; fi
}
file_flag() { # file_flag PATH -> yes when the file exists and is not empty
  if [ -s "$1" ]; then echo yes; else echo no; fi
}

verify_role() {
  _vr_want="$1"
  _vr_rec="$(recorded_role)"
  _vr_ce="$(unit_flag is-enabled mirymesh-controller)"
  _vr_ca="$(unit_flag is-active mirymesh-controller)"
  _vr_de="$(unit_flag is-enabled mirymesh-datanode)"
  _vr_da="$(unit_flag is-active mirymesh-datanode)"
  _vr_tok="$(file_flag /var/lib/mirymesh/datanode.token)"
  _vr_env="no"
  if [ -e /etc/mirymesh/controller.env ]; then _vr_env="yes"; fi
  VR_TOKEN_BEFORE="$_vr_tok"

  log "Role check: this run installs the host as '$_vr_want'"
  log "  recorded role ($ROLE_FILE): ${_vr_rec:-(none)}"
  log "  mirymesh-controller: enabled=$_vr_ce active=$_vr_ca, controller.env: $_vr_env"
  log "  mirymesh-datanode:   enabled=$_vr_de active=$_vr_da, datanode.token: $_vr_tok"
  if [ "$(id -u)" != "0" ]; then
    log "  WARNING: not running as root; /var/lib/mirymesh may be unreadable, so datanode.token can show 'no' wrongly"
  fi

  _vr_why=""
  if [ -n "$_vr_rec" ]; then
    # The record wins. Only a different role is a contradiction.
    if [ "$_vr_rec" != "$_vr_want" ]; then _vr_why="the record says '$_vr_rec'"; fi
  elif [ "$_vr_want" = "master" ]; then
    if [ "$_vr_tok" = "yes" ]; then
      _vr_why="datanode.token exists (this host is paired as a DataNode)"
    elif [ "$_vr_de" = "yes" ] || [ "$_vr_da" = "yes" ]; then
      _vr_why="mirymesh-datanode is enabled or running here"
    fi
  else
    # A client run is allowed on a damaged DataNode host (that is the recovery path). It is only
    # refused when the host looks like a pure Master: controller on, no DataNode token or service.
    if [ "$_vr_tok" = "no" ] && [ "$_vr_de" = "no" ] && [ "$_vr_da" = "no" ] \
       && { [ "$_vr_ce" = "yes" ] || [ "$_vr_ca" = "yes" ]; }; then
      _vr_why="mirymesh-controller is enabled or running and there is no DataNode token or service"
    fi
  fi

  if [ -n "$_vr_why" ]; then
    if [ -n "$FORCE_ROLE" ]; then
      log "WARNING: role check failed ($_vr_why), continuing because of --force-role"
    else
      die "role check failed: $_vr_why, but this run would install the host as '$_vr_want'. Nothing was changed. If '$_vr_want' is right: echo $_vr_want | sudo tee $ROLE_FILE (or pass --force-role)."
    fi
  fi
  if [ "$_vr_want" = "client" ] && { [ "$_vr_ce" = "yes" ] || [ "$_vr_ca" = "yes" ]; }; then
    log "WARNING: mirymesh-controller is enabled or running on a DataNode host. It is left alone; stop it with: sudo systemctl disable --now mirymesh-controller"
  fi
  log "Role check: OK ('$_vr_want' agrees with this host)"
}

# After the run: the record must say what was installed, a paired DataNode must still have its token.
verify_after_install() {
  _va_bad=""
  if [ "$(recorded_role)" != "$ROLE" ]; then _va_bad="$ROLE_FILE does not say '$ROLE'"; fi
  if [ "$VR_TOKEN_BEFORE" = "yes" ] && [ ! -s /var/lib/mirymesh/datanode.token ]; then
    _va_bad="datanode.token disappeared during this run (the node is no longer paired)"
  fi
  if [ -n "$_va_bad" ]; then die "post-install role check failed: $_va_bad"; fi
  if [ "$ROLE" = "client" ] && [ "$(unit_flag is-active mirymesh-controller)" = "yes" ]; then
    log "WARNING: mirymesh-controller is running on this DataNode host. Stop it with: sudo systemctl disable --now mirymesh-controller"
  fi
  log "Role check after install: OK ($ROLE; record written, DataNode token kept)"
}

verify_role "$ROLE"
if [ -n "$CHECK_ROLE" ]; then
  log "Role detection (what --update would pick): $(detect_installed_role)"
  log "--check-role: nothing was changed."
  exit 0
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

# Install type (fresh installs only): Release, Prerelease (rc), or Dev = build from source (default branch: release).
if [ -z "$UPDATE" ] && [ -z "$MODE" ] && [ -z "$CHANNEL" ] && interactive; then
  if [ "$ROLE" = "client" ]; then
    _rel="prebuilt DataNode from GitHub Releases"; _pre="prebuilt DataNode, newest incl. pre-releases"
  else
    _rel="built from source, release branch"; _pre="built from source, prerelease branch"
  fi
  printf "Install type — 1) Release (%s)  2) Prerelease (%s)  3) Dev (build from source, you pick the branch)\n" "$_rel" "$_pre"
  ask ans "Select 1, 2 or 3" "1"
  case "$ans" in
    2|pre|prerelease|Prerelease|p|P) CHANNEL="prerelease" ;;
    3|dev|Dev|DEV|d|D)
      MODE="from-source"
      [ -n "$BRANCH" ] || ask BRANCH "Branch to build" "release"
      ;;
  esac
fi

[ -n "$CHANNEL" ] || CHANNEL="release"

if interactive; then
  log "Plan: role=$ROLE${MASTER_URL:+ master=$MASTER_URL}${DATANODE_PATHS:+ paths=$DATANODE_PATHS}${MODE:+ mode=$MODE}${BRANCH:+ branch=$BRANCH} channel=$CHANNEL"
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
  if [ -z "$VERSION" ] && [ "$CHANNEL" = "prerelease" ]; then
    _t="$(resolve_latest_tag || true)"
    case "$_t" in
      v[0-9]*) VERSION="$_t"; log "Prerelease channel — using the newest release $VERSION" ;;
      *) log "Prerelease channel: could not look up the newest release"; return 1 ;;
    esac
  elif [ -z "$VERSION" ] && ! curl -fsSIL -o /dev/null "$RELEASE_BASE/latest/download/SHA256SUMS" 2>/dev/null; then
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
  # The binary is static: ask it for its version before it replaces the installed one.
  _new_v="$("$_tmp/$_name" --version 2>/dev/null | head -1 || true)"
  refuse_version_downgrade "$_new_v" || { rm -rf "$_tmp"; die "$DOWNGRADE_MSG"; }
  install -m 0755 "$_tmp/$_name" /usr/local/bin/mirymesh-datanode || { rm -rf "$_tmp"; return 1; }
  # A release binary has no source commit; drop the record of an earlier source build.
  rm -f "$COMMIT_FILE"
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
  # Pinned tag (--version) > chosen branch (--branch / Dev) > the channel's branch (release, or prerelease).
  _def_branch="release"
  [ "$CHANNEL" != "prerelease" ] || _def_branch="prerelease"
  _ref="${VERSION:-${BRANCH:-$_def_branch}}"
  log "Cloning MiryMesh ($_ref) to $SRC"
  # Full history (no --depth 1): the downgrade check needs it.
  git clone --branch "$_ref" https://github.com/sylphre/MiryMesh "$SRC" \
    || die "could not clone '$_ref' from GitHub — check the branch/tag name (--branch NAME / --version TAG)"
fi
log "Source: $SRC"
if [ "$MODE" != "release" ]; then
  refuse_version_downgrade "$VERSION" || die "$DOWNGRADE_MSG"
  if [ -n "$SRC" ]; then refuse_source_downgrade "$SRC" || die "$DOWNGRADE_MSG"; fi
fi

# Ensure mirymesh user/group (idempotent)
if ! getent group mirymesh >/dev/null 2>&1; then
  groupadd --system mirymesh 2>/dev/null || groupadd mirymesh || true
fi
if ! id -u mirymesh >/dev/null 2>&1; then
  useradd --system --gid mirymesh --home-dir /var/lib/mirymesh --no-create-home --shell /usr/sbin/nologin mirymesh 2>/dev/null \
    || useradd --system --gid mirymesh mirymesh || true
fi

# Web console (frontend/, Vue + Vite). frontend/dist is git-ignored and go:embed bakes it into the
# controller, so it must be built BEFORE `go build`; otherwise the binary has no console at all.
# Node.js and npm are prerequisites like the database: this script does not install them.
build_frontend() {
  [ -f "$SRC/frontend/package.json" ] || die "frontend/ not found in $SRC — cannot build the web console"
  command -v node >/dev/null 2>&1 \
    || die "node not found — install Node.js 22+ and npm (apt: nodejs npm, dnf: nodejs npm, apk: nodejs npm) then rerun"
  command -v npm >/dev/null 2>&1 \
    || die "npm not found — install npm (apt: npm, dnf: npm, apk: npm) then rerun"
  _fe_major="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || echo 0)"
  case "$_fe_major" in ''|*[!0-9]*) _fe_major=0 ;; esac
  [ "$_fe_major" -ge 22 ] \
    || die "Node.js 22+ is required to build the web console (found $(node --version 2>/dev/null || echo unknown)) — upgrade Node.js then rerun"
  log "Building the web console with Node.js $(node --version) (npm ci && npm run build)"
  _fe_tmp="$(mktemp -d /tmp/mirymesh-frontend.XXXXXX)"
  (
    export npm_config_cache="$_fe_tmp/npm-cache"
    cd "$SRC/frontend" || exit 1
    npm ci --no-audit --no-fund && npm run build
  ) || { rm -rf "$_fe_tmp"; die "web console build failed — see the npm output above"; }
  rm -rf "$_fe_tmp"
  [ -s "$SRC/frontend/dist/index.html" ] \
    || die "frontend/dist/index.html is missing after the build — refusing to install a controller without the web console"
  log "Web console built: frontend/dist will be embedded into the controller"
}

install_from_source() {
  if ! command -v go >/dev/null 2>&1; then
    die "go not found — install golang (apt: golang-go, dnf: golang) then rerun"
  fi
  # go:embed reads frontend/dist at compile time, so the console is built before `go build`.
  if [ "$ROLE" != "client" ]; then build_frontend; fi
  log "Building from source (go 1.22, trimpath, no Docker)"
  # The bundled go-sqlite3 C source triggers a harmless -Wdiscarded-qualifiers warning on newer gcc.
  # Silence only that warning; everything else still shows.
  CGO_CFLAGS="${CGO_CFLAGS:-} -Wno-discarded-qualifiers"
  export CGO_CFLAGS
  # Version stamped into the binaries (Master and DataNode alike): the pinned tag from --version,
  # otherwise "<7-char commit>-dev" (for example 1f7b438-dev), so a source build says what it is built from.
  # It is not a semantic version, so the update check and the downgrade guard treat it like "dev".
  _bv="$VERSION"
  if [ -z "$_bv" ]; then
    _bh="$(git -C "$SRC" rev-parse --short=7 HEAD 2>/dev/null || true)"
    case "$_bh" in
      ''|*[!0-9a-f]*) ;;
      *) _bv="${_bh}-dev" ;;
    esac
  fi
  log "Build version: ${_bv:-dev}"
  _ldf=""
  [ -z "$_bv" ] || _ldf="-X github.com/sylphre/MiryMesh/internal/version.Version=$_bv"
  # A DataNode host gets no controller: its mere presence made later runs detect the host as a Master.
  if [ "$ROLE" != "client" ]; then
    (cd "$SRC" && go build -trimpath -ldflags="$_ldf" -o /tmp/mirymesh-controller ./cmd/controller)
  fi
  (cd "$SRC" && go build -trimpath -ldflags="$_ldf" -o /tmp/mirymesh-datanode   ./cmd/datanode)
  (cd "$SRC" && go build -trimpath -ldflags="$_ldf" -o /tmp/mirymesh            ./cmd/mirymesh)
  if [ "$ROLE" != "client" ]; then
    install -m 0755 /tmp/mirymesh-controller /usr/local/bin/mirymesh-controller
  fi
  install -m 0755 /tmp/mirymesh-datanode   /usr/local/bin/mirymesh-datanode
  install -m 0755 /tmp/mirymesh            /usr/local/bin/mirymesh
  # Remember which commit this build came from, so a later --update can refuse to go back in history.
  mkdir -p /etc/mirymesh
  if _ic="$(git -C "$SRC" rev-parse HEAD 2>/dev/null)" && [ -n "$_ic" ]; then
    printf '%s\n' "$_ic" > "$COMMIT_FILE" 2>/dev/null || true
  else
    rm -f "$COMMIT_FILE"
  fi
  # The console is embedded in the controller binary. Drop the copy an older install left behind.
  rm -rf /usr/share/mirymesh/static
  rmdir /usr/share/mirymesh 2>/dev/null || true
  if [ -f "$SRC/packaging/systemd/mirymesh-controller.service" ]; then
    if [ "$ROLE" != "client" ]; then
      sed 's|/usr/bin/mirymesh-controller|/usr/local/bin/mirymesh-controller|g' "$SRC/packaging/systemd/mirymesh-controller.service" > /etc/systemd/system/mirymesh-controller.service
    fi
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

# DB (Master only): PostgreSQL or MySQL/MariaDB is required.
# Pick one, create the role/user and the database, and hand the DSN to the controller through
# /etc/mirymesh/controller.env (see write_controller_env). The Setup page then has no DSN to ask for.
DB_DRIVER_FINAL=""
DB_DSN=""

# db_rand BYTES — random hex string (2*BYTES characters).
db_rand() {
  openssl rand -hex "$1" 2>/dev/null || head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'
}

# Names go into SQL, so allow only [A-Za-z_][A-Za-z0-9_]* (max 32). Passwords also end up in a URL/DSN.
db_ident_ok() {
  case "$1" in ''|[0-9]*|*[!A-Za-z0-9_]*) return 1 ;; esac
  [ "${#1}" -le 32 ]
}
db_pass_ok() {
  case "$1" in ''|*[!A-Za-z0-9._~-]*) return 1 ;; esac
}

# Run psql as the postgres OS user; SQL comes from stdin, stdout is the bare result.
pg_admin() {
  if command -v runuser >/dev/null 2>&1; then
    (cd / && runuser -u postgres -- psql -X -q -A -t -v ON_ERROR_STOP=1 "$@")
  elif command -v sudo >/dev/null 2>&1; then
    (cd / && sudo -u postgres psql -X -q -A -t -v ON_ERROR_STOP=1 "$@")
  else
    echo "neither runuser nor sudo found" >&2
    return 127
  fi
}

# Create (or reset the password of) the role and create the database, one statement per call:
# CREATE DATABASE cannot run inside the implicit transaction of a multi-statement -c.
provision_pg() {
  _pg_out="$(printf "SELECT 1 FROM pg_roles WHERE rolname='%s';\n" "$db_user" | pg_admin 2>&1)" \
    || { log "PostgreSQL: cannot run psql as the postgres user: $_pg_out"; return 1; }
  if [ "$_pg_out" = "1" ]; then
    _pg_sql="ALTER ROLE \"$db_user\" WITH LOGIN PASSWORD '$db_pass';"
  else
    _pg_sql="CREATE ROLE \"$db_user\" WITH LOGIN PASSWORD '$db_pass';"
  fi
  _pg_out="$(printf '%s\n' "$_pg_sql" | pg_admin 2>&1)" \
    || { log "PostgreSQL: role setup failed: $_pg_out"; return 1; }

  _pg_out="$(printf "SELECT 1 FROM pg_database WHERE datname='%s';\n" "$db_name" | pg_admin 2>&1)" \
    || { log "PostgreSQL: database lookup failed: $_pg_out"; return 1; }
  if [ "$_pg_out" = "1" ]; then
    _pg_sql="ALTER DATABASE \"$db_name\" OWNER TO \"$db_user\";"
  else
    _pg_sql="CREATE DATABASE \"$db_name\" OWNER \"$db_user\";"
  fi
  _pg_out="$(printf '%s\n' "$_pg_sql" | pg_admin 2>&1)" \
    || { log "PostgreSQL: database setup failed: $_pg_out"; return 1; }

  # The controller connects over TCP with a password, so check exactly that.
  _pg_out="$(PGPASSWORD="$db_pass" psql -X -q -A -t -h 127.0.0.1 -p 5432 -U "$db_user" -d "$db_name" -c 'SELECT 1' </dev/null 2>&1)" \
    || { log "PostgreSQL: login as $db_user over 127.0.0.1:5432 failed: $_pg_out"; return 1; }
  log "PostgreSQL: role '$db_user' and database '$db_name' are ready"
}

# MySQL/MariaDB: connect as root through the local socket (what a fresh apt/dnf install allows).
provision_mysql() {
  _my_out="$(
    {
      printf "CREATE DATABASE IF NOT EXISTS \`%s\` CHARACTER SET utf8mb4;\n" "$db_name"
      for _h in localhost 127.0.0.1; do
        printf "CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s';\n" "$db_user" "$_h" "$db_pass"
        printf "ALTER USER '%s'@'%s' IDENTIFIED BY '%s';\n" "$db_user" "$_h" "$db_pass"
        printf "GRANT ALL PRIVILEGES ON \`%s\`.* TO '%s'@'%s';\n" "$db_name" "$db_user" "$_h"
      done
    } | "$MYSQL_CLI" -u root 2>&1
  )" || { log "MySQL/MariaDB: could not run SQL as root (needs socket auth for root): $_my_out"; return 1; }

  _my_out="$(MYSQL_PWD="$db_pass" "$MYSQL_CLI" --protocol=TCP -h 127.0.0.1 -P 3306 -u "$db_user" -D "$db_name" -e 'SELECT 1' </dev/null 2>&1)" \
    || { log "MySQL/MariaDB: login as $db_user over 127.0.0.1:3306 failed: $_my_out"; return 1; }
  log "MySQL/MariaDB: user '$db_user' and database '$db_name' are ready"
}

# Hand the DSN (and the master key it needs) to the controller. Written root:root 0600 and read by
# systemd through a drop-in, so the secrets are not in config.yaml. An existing master key is never replaced.
write_controller_env() {
  [ -n "$DB_DSN" ] || return 0
  _ce=/etc/mirymesh/controller.env
  if [ ! -f "$_ce" ]; then ( umask 077; : > "$_ce" ); fi
  chmod 0600 "$_ce" 2>/dev/null || true
  chown root:root "$_ce" 2>/dev/null || true
  if ! grep -q '^MIRYMESH_MASTER_KEY=.' "$_ce" 2>/dev/null; then
    _mk="${MIRYMESH_MASTER_KEY:-$(db_rand 32)}"
    printf 'MIRYMESH_MASTER_KEY=%s\n' "$_mk" >> "$_ce"
    log "Generated a master key in $_ce — back this file up; stored secrets cannot be decrypted without the key"
  fi
  # Replace (not append) the DB lines so a rerun never leaves two copies.
  _ce_tmp="$_ce.tmp"
  ( umask 077; grep -v -e '^MIRYMESH_DB_DRIVER=' -e '^MIRYMESH_DB_DSN=' "$_ce" > "$_ce_tmp" || true )
  printf 'MIRYMESH_DB_DRIVER=%s\nMIRYMESH_DB_DSN=%s\n' "$DB_DRIVER_FINAL" "$DB_DSN" >> "$_ce_tmp"
  mv "$_ce_tmp" "$_ce"
  chmod 0600 "$_ce" 2>/dev/null || true
  chown root:root "$_ce" 2>/dev/null || true
  _dd=/etc/systemd/system/mirymesh-controller.service.d
  mkdir -p "$_dd"
  cat > "$_dd/10-secrets.conf" <<'EOF'
[Unit]
After=postgresql.service mariadb.service mysql.service

[Service]
EnvironmentFile=/etc/mirymesh/controller.env
EOF
  log "Database ($DB_DRIVER_FINAL) passed to the controller via $_ce — the Setup page will not ask for a DSN"
}

if [ "$ROLE" = "master" ] && [ "${SKIP_DB_CHECK:-}" != "1" ]; then
  _skip=false
  for a in "$@"; do [ "$a" = "--skip-db-check" ] && _skip=true; done
  if [ "$_skip" = "true" ] || [ "${MIRYMESH_SKIP_DB_CHECK:-}" = "1" ]; then
    log "DB check skipped (--skip-db-check)"
  else
    has_pg=false; has_mysql=false; MYSQL_CLI=""
    command -v psql >/dev/null 2>&1 && has_pg=true
    if command -v mariadb >/dev/null 2>&1; then MYSQL_CLI=mariadb; has_mysql=true
    elif command -v mysql >/dev/null 2>&1; then MYSQL_CLI=mysql; has_mysql=true
    fi
    if [ "$has_pg" = "false" ] && [ "$has_mysql" = "false" ]; then
      echo "[install] PostgreSQL or MySQL/MariaDB is required — install one then rerun (apt: postgresql, dnf: postgresql-server / mariadb-server)" >&2
      echo "       or: $0 --skip-db-check  to skip this check" >&2
      exit 1
    fi

    db_user="${MIRYMESH_DB_USER:-}"; db_pass="${MIRYMESH_DB_PASSWORD:-}"; db_name="${MIRYMESH_DB_NAME:-}"; db_driver="${MIRYMESH_DB_DRIVER:-}"
    for a in "$@"; do case "$a" in --db-user=*) db_user="${a#--db-user=}";; --db-password=*) db_pass="${a#--db-password=}";; --db-name=*) db_name="${a#--db-name=}";; --db-driver=*) db_driver="${a#--db-driver=}";; esac; done

    if [ -s /etc/mirymesh/controller.env ] && grep -q '^MIRYMESH_DB_DSN=.' /etc/mirymesh/controller.env 2>/dev/null; then
      log "Database already configured in /etc/mirymesh/controller.env — keeping it"
    elif grep -Eq '^[[:space:]]+dsn:[[:space:]]*[^[:space:]#]' /etc/mirymesh/config.yaml 2>/dev/null; then
      log "Database already configured in /etc/mirymesh/config.yaml — keeping it"
    else
      case "$db_driver" in
        postgres|postgresql) db_driver=postgres ;;
        mysql|mariadb) db_driver=mysql ;;
        '') ;;
        *) die "unsupported --db-driver '$db_driver' (use postgres or mysql)" ;;
      esac
      if [ -z "$db_driver" ]; then
        if [ "$has_pg" = "true" ] && [ "$has_mysql" = "true" ]; then
          if interactive; then
            ask _db_choice "Database — 1) PostgreSQL  2) MySQL/MariaDB" "1"
            case "$_db_choice" in
              1|postgres|postgresql) db_driver=postgres ;;
              2|mysql|mariadb) db_driver=mysql ;;
              *) die "select 1 or 2 for the database" ;;
            esac
          else
            db_driver=postgres
            log "Both PostgreSQL and MySQL/MariaDB found — using postgres (choose with --db-driver mysql)"
          fi
        elif [ "$has_pg" = "true" ]; then db_driver=postgres
        else db_driver=mysql
        fi
      fi
      if [ "$db_driver" = "postgres" ] && [ "$has_pg" != "true" ]; then die "postgres selected but psql was not found"; fi
      if [ "$db_driver" = "mysql" ] && [ "$has_mysql" != "true" ]; then die "mysql selected but neither mysql nor mariadb was found"; fi

      if interactive; then
        [ -n "$db_user" ] || ask db_user "DB user" "mirymesh"
        [ -n "$db_name" ] || ask db_name "DB name" "mirymesh"
      fi
      db_user="${db_user:-mirymesh}"; db_name="${db_name:-mirymesh}"
      db_ident_ok "$db_user" || die "invalid DB user '$db_user' (letters, digits and _ only; must not start with a digit; max 32)"
      db_ident_ok "$db_name" || die "invalid DB name '$db_name' (letters, digits and _ only; must not start with a digit; max 32)"
      [ -n "$db_pass" ] || db_pass="$(db_rand 12)"
      db_pass_ok "$db_pass" || die "invalid DB password (allowed: letters, digits and . _ ~ -)"

      log "Database: $db_driver (user $db_user, database $db_name)"
      if [ "$db_driver" = "postgres" ]; then
        if provision_pg; then
          DB_DRIVER_FINAL=postgres
          DB_DSN="postgres://$db_user:$db_pass@127.0.0.1:5432/$db_name?sslmode=disable"
        fi
      else
        if provision_mysql; then
          DB_DRIVER_FINAL=mysql
          DB_DSN="$db_user:$db_pass@tcp(127.0.0.1:3306)/$db_name"
        fi
      fi
      if [ -z "$DB_DSN" ]; then
        log "The database was not set up automatically — fix the cause above and rerun, or enter the DSN on the /setup page"
      fi
    fi
  fi
fi
# Config seeding (0600, only if missing — same as postinstall.sh)
mkdir -p /etc/mirymesh /var/lib/mirymesh /var/log/mirymesh
chown mirymesh:mirymesh /etc/mirymesh 2>/dev/null || true
chown mirymesh:mirymesh /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
chmod 0750 /etc/mirymesh /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
# Remember the channel so that a later --update stays on it (a source build carries no version suffix).
printf '%s\n' "$CHANNEL" > "$CHANNEL_FILE" 2>/dev/null || true
# Remember the role: later runs trust this over guessing from installed files.
printf '%s\n' "$ROLE" > "$ROLE_FILE" 2>/dev/null || true
# Master: pass the DB the installer just created (and the master key) to the controller.
write_controller_env
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
# RequiresMountsFor is a [Unit] key; in [Service] systemd ignores it.
[Unit]
RequiresMountsFor=$_rw

[Service]
ReadWritePaths=$_rw
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
# Master: the update notice follows pre-releases only on the prerelease channel.
# (A Master that runs a pre-release build, or a "dev" source build, follows them anyway.)
if [ "$ROLE" = "master" ]; then
  _ch_conf=/etc/systemd/system/mirymesh-controller.service.d/40-channel.conf
  if [ "$CHANNEL" = "prerelease" ]; then
    mkdir -p /etc/systemd/system/mirymesh-controller.service.d
    cat > "$_ch_conf" <<CONF
# Written by install.sh (prerelease channel). Makes the update notice follow pre-releases.
[Service]
Environment=MIRYMESH_UPDATE_PRERELEASE=1
CONF
    log "Channel: prerelease (the update notice also offers pre-releases)"
  else
    rm -f "$_ch_conf"
  fi
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
    log "To start Master: sudo systemctl enable --now mirymesh-controller && xdg-open http://127.0.0.1:9000/  # setup redirects to http://127.0.0.1:9001/setup (S3 0.0.0.0:9000 / Admin 0.0.0.0:9001, Tailscale <tailscale-ip>:900x, firewall/Tailscale ACL required)"
    log "Setup:    Master 初回はブラウザで http://<tailscale-ip>:9001/setup または http://127.0.0.1:9001/setup で AccessKey/Secret/Policy を入力 (Token は localStorage に保存, 0.0.0.0:9001 は public に晒さない)"
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

verify_after_install
if [ -n "$UPDATE" ]; then log "Update finished ($ROLE)."; else log "Done ($ROLE)."; fi
