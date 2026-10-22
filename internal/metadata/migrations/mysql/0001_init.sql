-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved.
-- 0001_init (MySQL / MariaDB): same tables as ../0001_init.sql, written by hand because
--   * TEXT cannot be a key or carry a literal DEFAULT, so bounded VARCHAR / VARBINARY is used
--   * inline REFERENCES is parsed but ignored by InnoDB, so FOREIGN KEY is explicit
--   * key is a reserved word, so it is quoted
-- DDL is not transactional in MySQL, so every statement is idempotent (IF NOT EXISTS).

-- 1) Setup - singleton (id=1)
CREATE TABLE IF NOT EXISTS setup (
  id SMALLINT NOT NULL DEFAULT 1 PRIMARY KEY,
  admin_username VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  admin_password_hash TEXT NOT NULL,
  access_key TEXT NOT NULL,
  secret_key_enc TEXT NOT NULL,
  admin_token TEXT NOT NULL,
  default_policy VARCHAR(16) NOT NULL DEFAULT 'single',
  region VARCHAR(64) NOT NULL DEFAULT 'us-east-1',
  gateway BOOLEAN NOT NULL DEFAULT false,
  db_driver VARCHAR(16) NULL,
  db_dsn_enc TEXT NULL,
  datanode_paths TEXT NULL,
  initialized BOOLEAN NOT NULL DEFAULT true,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uq_setup_admin_username (admin_username),
  CONSTRAINT ck_setup_singleton CHECK (id = 1),
  CONSTRAINT ck_setup_default_policy CHECK (default_policy IN ('single','dual','triple','ALL')),
  CONSTRAINT ck_setup_db_driver CHECK (db_driver IS NULL OR db_driver IN ('postgres','postgresql','mysql','mariadb','sqlite','sqlite3'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 2) Sessions - hashed token
CREATE TABLE IF NOT EXISTS sessions (
  token_hash VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
  admin_username VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  expires_at DATETIME(6) NOT NULL,
  revoked BOOLEAN NOT NULL DEFAULT false,
  CONSTRAINT fk_sessions_admin FOREIGN KEY (admin_username) REFERENCES setup (admin_username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 3) Pair tokens - one-time datanode pairing
CREATE TABLE IF NOT EXISTS pair_tokens (
  token_hash VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  expires_at DATETIME(6) NOT NULL,
  used BOOLEAN NOT NULL DEFAULT false
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 4) Buckets
CREATE TABLE IF NOT EXISTS buckets (
  name VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
  owner VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  CONSTRAINT fk_buckets_owner FOREIGN KEY (owner) REFERENCES setup (admin_username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 5) Objects - key is VARBINARY(1024): S3 keys are at most 1024 bytes, compared byte-wise
CREATE TABLE IF NOT EXISTS objects (
  bucket VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  `key` VARBINARY(1024) NOT NULL,
  size BIGINT NOT NULL,
  etag VARCHAR(255) NOT NULL,
  content_type VARCHAR(255) NOT NULL DEFAULT 'application/octet-stream',
  meta JSON NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (bucket, `key`),
  CONSTRAINT ck_objects_size CHECK (size >= 0),
  CONSTRAINT fk_objects_bucket FOREIGN KEY (bucket) REFERENCES buckets (name) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 6) Placement - one row per bucket
CREATE TABLE IF NOT EXISTS placement (
  bucket VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
  policy VARCHAR(16) NOT NULL,
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  CONSTRAINT ck_placement_policy CHECK (policy IN ('single','dual','triple','ALL')),
  CONSTRAINT fk_placement_bucket FOREIGN KEY (bucket) REFERENCES buckets (name) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;