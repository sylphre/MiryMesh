<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Changelog
All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
