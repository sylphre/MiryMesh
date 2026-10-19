<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Misskey E2E — Phase5 stub

S3 drive (S3Service.ts/DriveService.ts): exercises `PutObject(+multipart)/DeleteObject` with `forcePathStyle` + `requestChecksumCalculation`.

Env:
- `MISSKEY_S3_BUCKET=e2e-misskey`, `MISSKEY_S3_KEY=TESTKEY`, `MISSKEY_S3_SECRET=testsecret12345`, `MISSKEY_S3_HOST=controller:9000`, `MISSKEY_S3_FORCE_PATH_STYLE=true`.

Run (future):
```bash
docker compose -f tests/e2e/misskey/docker-compose.yml up --abort-on-container-exit
go test ./tests/e2e/misskey -tags=e2e_external -run TestMisskey
```
Placeholder test (`-tags=!e2e_external`) always passes.
