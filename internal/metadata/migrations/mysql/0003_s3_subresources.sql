-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0003_s3_subresources (MySQL / MariaDB): persisted S3 P2 subresources (acl / tagging / cors / lifecycle).
-- object_key is VARBINARY(1024): exact byte comparison (case and trailing spaces matter) and a
-- composite primary key of 255 + 1024 + 32 bytes, well under the InnoDB 3072 byte limit.
-- object_key empty means the subresource belongs to the bucket itself.

CREATE TABLE IF NOT EXISTS s3_subresources (
  bucket VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  object_key VARBINARY(1024) NOT NULL DEFAULT '',
  kind VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  body LONGTEXT NOT NULL,
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (bucket, object_key, kind)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;