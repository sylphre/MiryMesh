<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# minio-client (mc) / rclone E2E — Phase5+ stub

```bash
mc alias set mirymesh http://localhost:9000 TESTKEY testsecret12345
mc cp file.txt mirymesh/e2e-bkt/
rclone copy file.txt mirymesh:e2e-bkt/ --s3-provider Other --s3-endpoint http://localhost:9000
```
Placeholder test always passes in default tags.
