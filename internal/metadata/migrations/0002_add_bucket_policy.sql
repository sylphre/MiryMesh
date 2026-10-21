-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0002_add_bucket_policy: S3 bucket policy (Misskey public-read etc.)

ALTER TABLE buckets ADD COLUMN policy JSONB;
