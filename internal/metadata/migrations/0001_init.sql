-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0001_init: initial schema (1 table 1責務, no prefix — DB name mirymesh discriminates)

-- 1) Setup — singleton (id=1)
CREATE TABLE setup (
  id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  admin_username TEXT NOT NULL,
  admin_password_hash TEXT NOT NULL,
  access_key TEXT NOT NULL,
  secret_key_enc TEXT NOT NULL,
  admin_token TEXT NOT NULL,
  default_policy TEXT NOT NULL DEFAULT 'single' CHECK (default_policy IN ('single','dual','triple','ALL')),
  region TEXT NOT NULL DEFAULT 'us-east-1',
  gateway BOOLEAN NOT NULL DEFAULT false,
  db_driver TEXT NOT NULL CHECK (db_driver IN ('postgres','postgresql','mysql','mariadb','sqlite','sqlite3')),
  db_dsn_enc TEXT NOT NULL,
  datanode_paths TEXT,
  initialized BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2) Sessions — hashed token
CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  admin_username TEXT NOT NULL REFERENCES setup(admin_username),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  revoked BOOLEAN NOT NULL DEFAULT false
);

-- 3) Pair tokens — one-time datanode pairing
CREATE TABLE pair_tokens (
  token_hash TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL,
  used BOOLEAN NOT NULL DEFAULT false
);

-- 4) Buckets
CREATE TABLE buckets (
  name TEXT PRIMARY KEY,
  owner TEXT NOT NULL REFERENCES setup(admin_username),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 5) Objects
CREATE TABLE objects (
  bucket TEXT NOT NULL REFERENCES buckets(name) ON DELETE CASCADE,
  key TEXT NOT NULL,
  size BIGINT NOT NULL CHECK (size >= 0),
  etag TEXT NOT NULL,
  content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
  meta JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (bucket, key)
);

-- 6) Placement — one row per bucket
CREATE TABLE placement (
  bucket TEXT PRIMARY KEY REFERENCES buckets(name) ON DELETE CASCADE,
  policy TEXT NOT NULL CHECK (policy IN ('single','dual','triple','ALL')),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Future: replicas (node_id, disk_path, bucket, key, state pending/verified/missing/stale, checksum)
