<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Security Policy

## Supported Versions
| Version | Supported |
|---------|-----------|
| main (dev) | ✅ |

## Reporting a Vulnerability
Do not open a public issue. Email the maintainers or use GitHub's private vulnerability reporting.
We aim to acknowledge within 3 days and to ship a fix within 14 days for validated issues.

## Hardening
- S3 SigV4 is verified on every S3 request; avoid `X-Amz-Content-Sha256=UNSIGNED-PAYLOAD` except for streaming.
- Controller ↔ datanode `internal/` endpoints require `X-Admin-Token` / registration token.
- Evacuation snapshots never contain secret keys — `AuthIdentities` is a presence marker only.

## Admin GUI/API — separate port
- Admin GUI/API lives on `:9001` (default `admin_listen`, separately from S3 `:9000`). Token is `X-Admin-Token` (from setup wizard → `localStorage`) but it alone is insufficient.
- Always keep `:9001` on `127.0.0.1` or Tailscale, or behind a reverse proxy with mTLS / ACL. Never expose `0.0.0.0:9001` to the public Internet.
- S3 port `:9000` returns `404 Admin API lives on admin port (:9001)` for `/api/admin/*` — use the admin port with `X-Admin-Token`.
