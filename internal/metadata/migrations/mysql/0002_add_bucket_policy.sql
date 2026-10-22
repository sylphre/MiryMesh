-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0002_add_bucket_policy (MySQL / MariaDB): S3 bucket policy.
-- MySQL has no ADD COLUMN IF NOT EXISTS; schema_migrations already makes this run once.

ALTER TABLE buckets ADD COLUMN policy JSON NULL;