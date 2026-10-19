<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# MiryMesh S3

MiryMesh is an open-source, distributed S3-compatible object storage for Linux (Debian/Alma/Rocky families). See `AGENTS.md §1-3` for architecture.

## Quick start

```bash
# Build
go build -o /tmp/mirymesh-controller ./cmd/controller
/tmp/mirymesh-controller /etc/mirymesh/config.yaml  # or: go run ./cmd/controller /dev/null
# Or Docker
docker build -f packaging/docker/Dockerfile -t mirymesh:dev .
docker run -p 9000:9000 -v /var/lib/mirymesh:/var/lib/mirymesh mirymesh:dev
```

See `packaging/README.md` for DEB/RPM install, and `packaging/config/config.yaml.example`.

## Documentation

- Contributing: [`CONTRIBUTING.md`](CONTRIBUTING.md) (dev setup, workflow, ADRs)
- Security: [`SECURITY.md`](SECURITY.md) (private reporting)
- Support: [`SUPPORT.md`](SUPPORT.md) (supported versions)
- Changelog: [`CHANGELOG.md`](CHANGELOG.md) (Keep a Changelog)
- License: [`LICENSE`](LICENSE) (`AGPL-3.0-only`)

## License

`AGPL-3.0-only`, see `LICENSE`. Every source file carries `SPDX-License-Identifier: AGPL-3.0-only`.
