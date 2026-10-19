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
- Evacuation snapshots (`EvacuationSnapshot`) never contain secret keys — `AuthIdentities` is a presence marker only (docs/07).
