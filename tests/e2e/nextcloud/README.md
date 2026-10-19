<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Nextcloud E2E — Phase5 stub

Primary storage S3 (docs/06): exercises `PutObject/GetObject/DeleteObject/ListObjectsV2/HeadObject/HeadBucket/CreateBucket` + `CreateMultipartUpload/UploadPart/CompleteMultipartUpload` (when M6).

Env (maps to `config.php`):
- `NC_S3_BUCKET=e2e-nextcloud`, `NC_S3_KEY=TESTKEY`, `NC_S3_SECRET=testsecret12345`, `NC_S3_HOST=controller:9000`, `NC_S3_USE_PATH_STYLE=true`, `NC_S3_AUTOCREATE=true`, `NC_S3_VERIFY_BUCKET_EXISTS=true`.

Run (future):
```bash
docker compose -f tests/e2e/nextcloud/docker-compose.yml up --abort-on-container-exit
docker exec nextcloud php occ files:scan --all
go test ./tests/e2e/nextcloud -tags=e2e_external -run TestNextcloud
```
Placeholder test (`-tags=!e2e_external`) always passes so CI inner loop stays fast.
