-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0004_users (MySQL / MariaDB): same tables as ../0004_users.sql, written by hand because
-- TEXT cannot be a key and inline REFERENCES is ignored by InnoDB.
-- Names and buckets are ASCII only and compared byte-wise.

CREATE TABLE IF NOT EXISTS users (
  name VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
  access_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  secret_key_enc TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uq_users_access_key (access_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS user_bucket_perms (
  user_name VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  bucket VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  permission VARCHAR(16) NOT NULL,
  PRIMARY KEY (user_name, bucket),
  CONSTRAINT ck_user_bucket_perms_permission CHECK (permission IN ('read','write','readwrite')),
  CONSTRAINT fk_user_bucket_perms_user FOREIGN KEY (user_name) REFERENCES users (name) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;