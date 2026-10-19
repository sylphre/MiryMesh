#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
set -eu
# Create mirymesh user/group if missing (idempotent, works on both deb/rpm)
if ! getent group mirymesh >/dev/null 2>&1; then
  groupadd --system mirymesh || groupadd mirymesh || true
fi
if ! id -u mirymesh >/dev/null 2>&1; then
  useradd --system --gid mirymesh --home-dir /var/lib/mirymesh --no-create-home --shell /usr/sbin/nologin mirymesh \
    || useradd --system --gid mirymesh mirymesh || true
fi
exit 0
