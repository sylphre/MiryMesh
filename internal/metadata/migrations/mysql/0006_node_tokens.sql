-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0006_node_tokens (MySQL / MariaDB): same table as ../0006_node_tokens.sql, written by hand
-- because TEXT cannot be a key. 190 characters keep the key under the 767 byte index limit.

CREATE TABLE IF NOT EXISTS node_tokens (
  node_id VARCHAR(190) NOT NULL PRIMARY KEY,
  token_enc TEXT NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;