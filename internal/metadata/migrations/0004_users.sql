-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0004_users: S3 users. Each user owns a generated access key / secret key pair
-- and per-bucket permissions. bucket = '*' grants the permission on every bucket.
-- permission is one of read, write, readwrite.

CREATE TABLE IF NOT EXISTS users (
  name TEXT PRIMARY KEY,
  access_key TEXT NOT NULL UNIQUE,
  secret_key_enc TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_bucket_perms (
  user_name TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE,
  bucket TEXT NOT NULL,
  permission TEXT NOT NULL CHECK (permission IN ('read','write','readwrite')),
  PRIMARY KEY (user_name, bucket)
);