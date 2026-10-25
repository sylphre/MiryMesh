-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0007_datanodes (MySQL / MariaDB): same table as ../0007_datanodes.sql, written by hand
-- because TEXT cannot be a key. 190 characters keep the key under the 767 byte index limit.

CREATE TABLE IF NOT EXISTS datanodes (
  node_id VARCHAR(190) NOT NULL PRIMARY KEY,
  addr TEXT NOT NULL,
  version VARCHAR(64) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;