<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# E2E — Phase4 stubs

Clients (docs/06 S3 compat + docs/10): Nextcloud 28-36 primary_storage, Misskey drive, AWS SDK Go v2 / JS v3, rclone, minio-client.
Baseline P0: Put/Get/Delete/List/Head (+ multipart later). Each client runs against a real controller instance (single-node local storage) and verifies `2006-03-01` XML xmlns and error formatting.

Layout:
- `tests/e2e/s3compat/` — Go helper + per-client cases (aws-sdk-go-v2 example included as placeholder).
- `tests/e2e/nextcloud/` / `tests/e2e/misskey/` — docker-compose fixtures (future) that exercise primary_storage/drive configs (`docs/06` bucket/key/secret/path-style).
- `tests/e2e/fault/` — failure injection stubs (disk error, node disconnect, interrupted write, controller restart) per docs/07/10.

CI will run E2E as a separate job with real services; Phase4 only scaffolds the dirs and this README.
