<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# E2E — Phase5

Clients (docs/06 S3 compat + docs/10): Nextcloud 28-36 primary_storage (`docs/06` P0 + `config.php` bucket/key/secret/use_path_style/verify_bucket_exists/autocreate), Misskey drive (`S3Service.ts` Put/Delete + forcePathStyle + multipart), AWS SDK Go v2 / JS v3, rclone, minio-client.
Baseline P0: Put/Get/Delete/List/Head (+ multipart `CreateMultipartUpload/UploadPart/Complete/Abort/ListParts` is Nextcloud/Misskey P0, currently `NotImplemented` until M6+). Each client runs against a real controller instance (single-node local storage) and verifies `2006-03-01` XML xmlns and error formatting.

Layout:
- `tests/e2e/s3compat/` — in-process Go E2E (httptest + sigv4.Signer) + future aws-sdk-go-v2 external client (`//go:build e2e_external`).
- `tests/e2e/nextcloud/` / `tests/e2e/misskey/` — docker-compose fixtures (future) that exercise `config.php` / `S3Service.ts` paths. Instructions:
  - `docker compose -f tests/e2e/nextcloud/docker-compose.yml up` — brings up Nextcloud with `primary_storage` set to `http://controller:9000` (`use_path_style=true`, `verify_bucket_exists=true`). See `tests/e2e/nextcloud/README.md:2` for env vars.
  - `docker compose -f tests/e2e/misskey/docker-compose.yml up` — brings up Misskey with `drive` S3 config. See `tests/e2e/misskey/README.md`.
- `tests/e2e/fault/` — failure injection (disk error, node disconnect -> 507, interrupted write, controller restart -> file-backed meta reconstruction) per docs/07/10.
- `tests/e2e/s3compat/README.md` — sigv4.Signer helper vs external SDK guidance.

CI: `.github/workflows/ci.yml` runs `tests/e2e/... -race` as a dedicated `e2e` job. Real external clients (awscli, Nextcloud/Misskey docker) will run as `e2e-external` with `if: github.event_name == 'schedule'` or manual `workflow_dispatch` to avoid slow inner loop.
