-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0003_s3_subresources: persisted S3 P2 subresources (?acl / ?tagging / ?cors / ?lifecycle)
-- object_key = '' means the subresource belongs to the bucket itself.

CREATE TABLE IF NOT EXISTS s3_subresources (
  bucket TEXT NOT NULL,
  object_key TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  body TEXT NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (bucket, object_key, kind)
);