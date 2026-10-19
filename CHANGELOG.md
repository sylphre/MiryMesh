<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Changelog
All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] - 0.0.0-dev
### Added
- S3-compatible API endpoint (P0: Put/Get/Delete/List/Head, CopyObject, DeleteObjects, multipart).
- Placement engine (single/dual/triple/ALL, 95% quota).
- Replication engine (checksum, retry, interrupted-job recovery).
- Metadata evacuation / adoption (fan-out to datanodes, placement + auth presence snapshot).
- Management GUI (plain HTML/CSS/JS, no build) and `/api/admin/*` + `/internal/*`.
- Packaging via `nfpm` (DEB/RPM), `systemd` units, `Dockerfile` (distroless).
- CI: `ci.yml` (lint-test, e2e, packaging, linux-matrix, e2e-external).

## [0.0.0-dev] - Initial
- Repository scaffolding and Phase 0 baseline.
