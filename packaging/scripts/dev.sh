#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
set -e
docker compose -f docker-compose.dev.yml up --build --watch 2>/dev/null || docker compose -f docker-compose.dev.yml up --build
