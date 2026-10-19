#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
set -eu
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop mirymesh-controller mirymesh-datanode 2>/dev/null || true
  systemctl disable mirymesh-controller mirymesh-datanode 2>/dev/null || true
fi
exit 0
