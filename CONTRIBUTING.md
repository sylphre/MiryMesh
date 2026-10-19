<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Contributing to MiryMesh

## Development Setup
```bash
go 1.22 is required (see go.mod)
go vet ./... && go test ./... -count=1 -race
gofmt -l .  # must be empty
```

## Workflow
- Format: `gofmt -w .` (no other formatter).
- Lint: `golangci-lint run --timeout=3m` (see `.golangci.yml`).
- Tests: `go test ./... -count=1 -race` always before pushing.
- Branches: one milestone per PR, keep `go test -cover`.

## Architecture
- See `AGENTS.md §2-3` for component boundaries (S3 API / metadata / placement / replication / GUI).
- ADRs live in `docs/ADR-*.md` (ignored in git; ask maintainer if you need one).
- Controller (`cmd/controller`) and datanode (`cmd/datanode`) share `internal/config` validation centrally.

## Commit / PR
- License header on every new file:
  `// SPDX-License-Identifier: AGPL-3.0-only`
  `// Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.`
- No AI-usage disclosure in public git (see `.gitignore`).
- One logical change per commit; include tests + packaging/README updates where applicable.

## Reporting Issues
- Use issue templates: `bug_report.md` / `feature_request.md`.
- For security issues, see `SECURITY.md` — do not file a public issue.
