<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# s3compat — helper

Use `internal/sigv4.Signer` to sign requests and `httptest` to drive the controller in-process. Real E2E with AWS SDK will replace `net/http` direct calls.
