-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0008_datanode_disks (MySQL / MariaDB): same table as ../0008_datanode_disks.sql, written
-- by hand because TEXT cannot be a key. disk_id is a 64 character hex digest.

CREATE TABLE IF NOT EXISTS datanode_disks (
  disk_id CHAR(64) NOT NULL PRIMARY KEY,
  node_id VARCHAR(190) NOT NULL,
  path TEXT NOT NULL,
  capacity BIGINT NOT NULL DEFAULT 0,
  used BIGINT NOT NULL DEFAULT 0,
  limit_bytes BIGINT NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;