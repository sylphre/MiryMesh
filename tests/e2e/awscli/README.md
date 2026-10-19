<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# awscli E2E — Phase5+ stub

Requires external runner (`e2e_external`):
```bash
AWS_ACCESS_KEY_ID=TESTKEY AWS_SECRET_ACCESS_KEY=testsecret12345 \
  aws --endpoint-url http://localhost:9000 --region us-east-1 s3 ls
  aws --endpoint-url http://localhost:9000 s3 cp file.txt s3://e2e-bkt/
```
CI will run via `docker run amazon/aws-cli` against a temporary controller (`go run ./cmd/controller` with ephemeral `DataDir`).

Placeholder test always passes in default tags.
