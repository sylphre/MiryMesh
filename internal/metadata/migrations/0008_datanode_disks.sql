-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0008_datanode_disks: last reported disks of each DataNode. disk_id is the hex SHA-256 of
-- node_id, a NUL byte and path, so the key has a fixed length on every database.

CREATE TABLE IF NOT EXISTS datanode_disks (
  disk_id TEXT PRIMARY KEY,
  node_id TEXT NOT NULL,
  path TEXT NOT NULL,
  capacity BIGINT NOT NULL DEFAULT 0,
  used BIGINT NOT NULL DEFAULT 0,
  limit_bytes BIGINT NOT NULL DEFAULT 0
);