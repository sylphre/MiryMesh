-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0005_app_settings (MySQL / MariaDB): same table as ../0005_app_settings.sql, written by hand
-- because TEXT cannot be a key. Keys are ASCII only and compared byte-wise.

CREATE TABLE IF NOT EXISTS app_settings (
  setting_key VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
  setting_value VARCHAR(255) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;