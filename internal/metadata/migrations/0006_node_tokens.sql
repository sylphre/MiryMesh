-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0006_node_tokens: one random credential per DataNode, sealed with the master key.
-- The Master uses it to call the node and the node uses it to call the Master, so
-- pairing no longer depends on the Admin / internal token.

CREATE TABLE IF NOT EXISTS node_tokens (
  node_id TEXT PRIMARY KEY,
  token_enc TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);