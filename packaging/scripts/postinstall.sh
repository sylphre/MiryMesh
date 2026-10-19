#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
set -eu
mkdir -p /var/lib/mirymesh /var/log/mirymesh /etc/mirymesh
chown mirymesh:mirymesh /var/lib/mirymesh /var/log/mirymesh || true
chmod 0750 /var/lib/mirymesh /var/log/mirymesh || true
if [ ! -f /etc/mirymesh/config.yaml ] && [ -f /etc/mirymesh/config.yaml.example ]; then
  cp /etc/mirymesh/config.yaml.example /etc/mirymesh/config.yaml
  chmod 0600 /etc/mirymesh/config.yaml || true
  chown mirymesh:mirymesh /etc/mirymesh/config.yaml 2>/dev/null || chgrp mirymesh /etc/mirymesh/config.yaml 2>/dev/null || chmod 0644 /etc/mirymesh/config.yaml 2>/dev/null || true
  chown mirymesh:mirymesh /etc/mirymesh 2>/dev/null || true
  chown mirymesh:mirymesh /var/lib/mirymesh /var/log/mirymesh 2>/dev/null || true
fi
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi
exit 0
