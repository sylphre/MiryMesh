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
- Admin GUI/API lives on `0.0.0.0:9001` (default `admin_listen`) and S3 on `0.0.0.0:9000`, DataNode on `0.0.0.0:9002` — all reachable via Tailscale `100.114.64.41:900x` and `127.0.0.1:900x`. Token is `X-Admin-Token` (from setup wizard → `localStorage`) but it alone is insufficient.
- Always keep `:9001` (and `:9002`) behind `127.0.0.1` or Tailscale ACL / firewall / mTLS. `0.0.0.0:900x` must not be exposed to the public Internet.
- If you bind `admin_listen: 127.0.0.1:9001` you restrict admin to localhost only; `0.0.0.0:9001` is the default to allow `100.114.64.41:9001` via Tailscale — firewall it.
- S3 port `0.0.0.0:9000` returns `404 Admin API lives on admin port (0.0.0.0:9001)` for `/api/admin/*` — use the admin port with `X-Admin-Token` and close it via Tailscale ACL / firewall.
