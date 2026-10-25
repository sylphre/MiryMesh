<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Changelog
All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]
### Added
- `install.sh` always verifies the role before it changes anything and prints the facts it used: the recorded role, the enabled/active state of `mirymesh-controller` and `mirymesh-datanode`, `controller.env` and `datanode.token`. A contradiction (for example Master on a host with a DataNode token or service) stops the run; `--force-role` overrides. After the run it checks that `/etc/mirymesh/role` was written and that a paired DataNode still has its `datanode.token`, and warns when a controller is running on a DataNode host. New `--check-role` prints these facts and the detected role and exits without changing anything (add `--master` / `--client` to test a role).
- `install.sh` records the host role in `/etc/mirymesh/role` (`master` or `client`) after every install or update, and role detection trusts that file first. A run whose role differs from the record (for example `--master` on a DataNode host, or an auto-detected role) stops with an error before any database, `controller.env` or service change. Hosts installed before the record existed are checked for `/var/lib/mirymesh/datanode.token` and refused as Master if it exists. `--force-role` overrides the guard.
- `install.sh` (Master) can choose between PostgreSQL and MySQL/MariaDB when both are installed: an interactive prompt, `--db-driver postgres|mysql`, or PostgreSQL with `--yes`. New `--db-user`, `--db-name` and `--db-password` options.
- `install.sh` creates the MySQL/MariaDB user and database (root socket login) and hands the DSN to the controller, the same as it does for PostgreSQL.
- Update notice channel: the Settings page has an "更新のお知らせ" card to choose stable releases only, pre-releases too, or "follow the install" (the previous behavior, still the default). The choice is stored in the database (`app_settings`, migration `0005`), applies at once, and survives restarts. API: `PUT /api/admin/update/settings` with `{"notify_channel": "auto|release|prerelease"}`. `GET /api/admin/update` now also returns `notify_channel` and `include_prerelease`.
- `install.sh`: an interactive run on a host that already has MiryMesh asks where to update to: Release, Prerelease or Dev (branch). This is how a Dev source build moves to Prerelease or Release. `--update --channel release|prerelease` does the same without prompts.
- Console: the Placement tab of a bucket (`BucketDetail.vue`) now sets the placement mode (spread / same_node / any), the allow-list of disks the bucket may write to, and the disks reserved for that bucket only. Before, only the single/dual/triple/ALL policy could be chosen and the rest needed direct API calls.
- Integration tests for `RemoteStore` across several real DataNode HTTP servers (`internal/s3api/remote_store_multinode_test.go`): spreading over nodes, GET failover, merged and paged listing, delete, a node that is down, `same_node` with an allow-list, reserved disks, and per-node token rejection.
- S3 region can be changed from the console: the Settings page has an "S3 リージョン" card (presets plus free input, "既定に戻す" for `us-east-1`). The value is stored in the database (`app_settings`, key `s3.region`), applies at once without a restart, survives restarts, and overrides `server.region`. It is what `X-Amz-Bucket-Region` and GetBucketLocation report; SigV4 still accepts the region the client signed with. API: `GET/PUT /api/admin/region` with `{"region": "ap-northeast-1"}`.
- README: new "ソースビルド（Dev）のノードを更新する" section. It explains how to update a Master or DataNode that was built from a branch with `install.sh --update --dev --branch NAME`, why the Nodes page "更新" button must not be used on those hosts (it swaps in the release binary), the update order (Master first, then one DataNode at a time), and how to go back to Release or Prerelease.

### Fixed
- A paired DataNode no longer drops its node token when the Master rejects it. Before, a `403` on register deleted `datanode.token` and fell back to the already used one-time pair token, so the node could never come back. The token is kept, a rejected heartbeat (`403`) or unknown node (`404`) makes the node try to register again on every tick, a fresh pair token can still re-pair the node, and the replaced token is kept as `datanode.token.prev`. The token file is written through a temporary file and a rename. The Master only marks silent nodes offline; it never removes them or their disks, and they return on the next heartbeat (tests in `internal/datanode/keepalive_test.go`).
- `install.sh --update` treated a DataNode host as a Master. Source builds installed `mirymesh-controller` (binary and unit) on every host, and role detection returned "master" whenever a controller was present, so the update could create a database, write `controller.env` and start a controller on a DataNode. Source builds now install the controller only for the Master role. When both a controller and a DataNode are found, detection uses which service is enabled, then whether `/var/lib/mirymesh/datanode.token` exists, and chooses DataNode over Master in doubt. `--client` / `--master` still override detection.
- `install.sh` created the PostgreSQL role but not the database. `CREATE USER` and `CREATE DATABASE` ran in one `psql -c`, which PostgreSQL rejects (`CREATE DATABASE` cannot run in a transaction block), and `2>/dev/null` hid the error. Each statement now runs separately, errors are printed, and the installer checks that it can log in over `127.0.0.1` with the new password. A rerun resets the password of an existing role instead of failing.
- The Setup page asked for a DSN even though `install.sh` had just created the database. The installer now writes `MIRYMESH_DB_DRIVER`, `MIRYMESH_DB_DSN` and a generated `MIRYMESH_MASTER_KEY` to `/etc/mirymesh/controller.env` (root:root 0600) and loads it through a systemd drop-in, so the controller starts with the database configured. An existing master key or DB setting is kept.
- Source builds printed a `-Wdiscarded-qualifiers` warning from the bundled go-sqlite3 C code. `install.sh` now passes `-Wno-discarded-qualifiers` to cgo for that build only.
- A DataNode with several data paths ignored the `?disk=` the placement engine sends and always wrote to the first path. It now opens a store per configured path, writes to the requested disk (creating the bucket there), and rejects a `disk` that is not a configured path with `400`. Requests without `?disk=` (GET, HEAD, DELETE, list) search all disks, default disk first, so objects on the second and later disks can be read and deleted.
- Listing a bucket across several disks or DataNodes broke `continuation-token` paging. The DataNode now merges the listings of its disks (duplicates dropped, byte order, at most `max-keys` entries) and returns `is_truncated` and `next_continuation_token`; the controller merges the pages of all online nodes the same way instead of ignoring those fields. Nodes that answer with an error for a bucket are skipped.

## [1.0.0-rc.2] - 2026-10-24
### Fixed
- `install.sh` source builds (Master, including the prerelease channel) installed the old Alpine.js console. `frontend/dist` is git-ignored, so a fresh clone had nothing to embed and the controller fell back to `web/static`. The installer now builds the Vue console before `go build` and stops with an error if `frontend/dist/index.html` is missing.
- `install.sh` requires Node.js 22+ and npm on the host for Master source builds. It does not install Node.js. If either is missing or too old, it stops with an error before building, like the existing database check.
- `install.sh` removes `/usr/share/mirymesh/static` left behind by older installs, and its setup hints now point to `/setup`.

### Changed
- The release workflow, CI (`packaging`, `setup-smoke`) and the production `Dockerfile` build `frontend/` before compiling the controller.
- A controller built without the console now answers `503` with build instructions on non-API paths instead of serving a stale UI. `/api/*` keeps working.
- The DEB/RPM no longer ships `/usr/share/mirymesh/static`.

### Removed
- The Alpine.js console (`web/static`, `web/templates`) and its filesystem fallback in the controller (`resolveStaticDir`, `hasTokenGin`). The old `/static/*.html` URLs still redirect to the Vue routes.

## [1.0.0-rc.1] - 2026-10-24
### Added
- S3-compatible API endpoint (P0: Put/Get/Delete/List/Head, CopyObject, DeleteObjects, multipart).
- Placement engine (single/dual/triple/ALL, 95% quota).
- Per-bucket storage quota: `GET/PUT /api/admin/buckets/{bucket}/quota` and a Quota tab in the console. Writes beyond the limit (PutObject, CopyObject, multipart parts, console upload) return `507 InsufficientStorage`; `0` means unlimited.
- Replication engine (checksum, retry, interrupted-job recovery).
- Metadata evacuation / adoption (fan-out to datanodes, placement + auth presence snapshot).
- Management GUI (plain HTML/CSS/JS, no build) and `/api/admin/*` + `/internal/*`.
- Packaging via `nfpm` (DEB/RPM), `systemd` units, `Dockerfile` (distroless).
- CI: `ci.yml` (lint-test, e2e, packaging, linux-matrix, e2e-external).
- S3 SSE-C (AES-256-CTR, copy-source keys, multipart) and checksum validation (`Content-MD5`, CRC32/CRC32C/CRC64NVME/SHA1/SHA256, COMPOSITE / FULL_OBJECT multipart checksums, `x-amz-checksum-mode`).
- S3 `aws-chunked` request bodies: `STREAMING-AWS4-HMAC-SHA256-PAYLOAD[-TRAILER]` and `STREAMING-UNSIGNED-PAYLOAD-TRAILER` with chunk/trailer signature verification and trailing `x-amz-checksum-*` validation.
- `examples/`: NGINX (S3 / public read) and systemd drop-in samples.
- `install.sh --client` installs the prebuilt, sha256-verified static DataNode from GitHub Releases (falls back to a source build); the release workflow now publishes `mirymesh-datanode-linux-{amd64,arm64}` and `SHA256SUMS`.
- `install.sh`: accepts `--opt VALUE` as well as `--opt=VALUE`, accepts `--skip-db-check`, makes the DataNode data paths writable under `ProtectSystem=strict`, and passes the one-time pair token through a drop-in that is removed after pairing.
- Nodes page: the "Node を追加" command is now a `curl | sudo bash` one-liner that works on a host without a repository checkout.
- `install.sh`: interactive prompts (role, Master URL, pair token, data paths, confirmation) that also work under `curl | sudo bash`; `--yes` disables them. New `--update` flag updates an existing install in place (role auto-detected, config and data kept). Without a stable release it falls back to the newest pre-release such as `v1.0.0-rc.1`.
- Update notice: the Master checks GitHub Releases (`GET /api/admin/update`, `POST /api/admin/update/check`) and the console shows a dismissible banner when a newer version exists.
- One-click DataNode update: the Nodes page shows each node's version and an "更新" button (`POST /api/admin/nodes/update`). The DataNode downloads the release binary, verifies it against `SHA256SUMS`, stages it and exits with code 75. A root-owned `ExecStartPre` helper installed by `install.sh` replaces the binary (previous one kept as `.prev`) before systemd restarts the service. Run `install.sh --update` once on each existing node to enable it, or pass `--no-self-update` to opt out.
- DataNodes report their version when registering.
- `install.sh`: on a host that already has MiryMesh, the interactive run offers only "Update". New `--dev` and `--branch NAME` build from source; source builds (and the source fallback) now clone the `release` branch by default, and `--version TAG` still takes precedence. A fresh interactive install asks for Release or Dev.
- Branch flow `dev` > `prerelease` > `release`: the release workflow now runs on pushes to `prerelease` (publishes a GitHub pre-release, e.g. `v1.0.0-rc.1`) and `release` (publishes a normal release, e.g. `v1.0.0`). The branch decides the type instead of the words beta/dev/rc in the merge message, and a version whose suffix does not match the branch fails before anything is published.
- `install.sh`: new `--prerelease` / `--channel release|prerelease` and `MIRYMESH_CHANNEL`. The prerelease channel installs the newest release including pre-releases and builds source installs from the `prerelease` branch. `--update` stays on the channel the host is on (detected from the installed version or `/etc/mirymesh/channel`). A prerelease Master also sets `MIRYMESH_UPDATE_PRERELEASE=1` so the update notice offers rc builds. A fresh interactive install now asks Release, Prerelease or Dev.

## [0.0.0-dev] - Initial
- Repository scaffolding and Phase 0 baseline.
