-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0007_datanodes: the DataNodes the Master knows about, so a Master restart does not
-- empty the node list. Nodes come back as offline and turn online on their next heartbeat.

CREATE TABLE IF NOT EXISTS datanodes (
  node_id TEXT PRIMARY KEY,
  addr TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT ''
);