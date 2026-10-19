<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# Docker — Phase4 stub

Build: `docker build -f packaging/docker/Dockerfile -t mirymesh:dev .`
Run: `docker run -p 9000:9000 -v /var/lib/mirymesh:/var/lib/mirymesh mirymesh:dev`
Config: mount `/etc/mirymesh/config.yaml` or use defaults.
