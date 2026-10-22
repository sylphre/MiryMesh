<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# MiryMesh S3

MiryMesh は Linux（Debian / Alma / Rocky）向けの分散 S3 互換ストレージ。Master（S3:9000 / Admin:9001）+ DataNode（:9002、複数パス）を分散し、LXC-first で Docker は任意。

*MiryMesh is a distributed S3-compatible storage for Linux. Master (S3:9000 / Admin:9001) + DataNode (:9002) across disks/servers. LXC-first, Docker optional.*

> **注意**: `0.0.0.0:900x` を public に晒さない。`X-Admin-Token` / `X-Internal-Token` + Tailscale `<tailscale-ip>:900x` / `127.0.0.1:900x` / ファイアウォールで閉じる。`admin_listen: 127.0.0.1:9001` で localhost のみに絞ることも可。

## Quick start

### A) 開発で動かす（推奨）

```bash
docker compose -f docker-compose.dev.yml up --build
# → http://127.0.0.1:9001/setup で Admin 作成（username/password → 201 admin_token）→ login → Step 2（S3/DB/Storage、Access: Private/Public Read/Custom + Datanode Paths）→ Dashboard → Nodes で Node を追加
# DB は postgres:16 が compose で立つためローカルに DB 不要、MIRYMESH_MASTER_KEY は dev で自動
```

### B) LXC に直接入れる（本番）

```bash
# Master
curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash -s -- --master
# Client（DataNode）— pair_token は 15分で失効、1回限り
sudo bash packaging/scripts/install.sh --client --master-url http://<master>:9001 --pair-token <one-time> --path /mnt/disk1
# 源ビルド
git clone https://github.com/sylphre/MiryMesh && cd MiryMesh && go build -o /tmp/mirymesh-controller ./cmd/controller && sudo install -m 0755 /tmp/mirymesh-controller /usr/local/bin/ && sudo systemctl enable --now mirymesh-controller
# ヘルプ / DEB 変種
sudo bash packaging/scripts/install.sh --help
```

### C) DEB/RPM（任意）

```bash
go build -trimpath ... && nfpm pkg --packager deb --config packaging/nfpm.yaml --target mirymesh.deb && sudo apt install ./mirymesh.deb
```

詳細は [`packaging/README.md`](packaging/README.md) / [`packaging/config/config.yaml.example`](packaging/config/config.yaml.example) / [`packaging/scripts/install.sh`](packaging/scripts/install.sh)。

## Architecture

Master: Gin v1.12 で S3 `:9000` / Admin `:9001` / DataNode `:9002`。DB は `postgres:16` primary + `mysql/mariadb` + `sqlite`(dev, `mattn/go-sqlite3`)、DB名 `mirymesh` で判別し `miry_` prefix なし（`setup/sessions/pair_tokens/buckets/objects/placement`）。`MIRYMESH_MASTER_KEY` 必須で `secret_key_enc/dsn_enc` は AES-GCM。`buckets.policy JSONB` に `?policy` を永続、gateway は `server.mode: gateway` で master に実体を置かず DataNode のみに保存（`setup.db_driver CHECK(IS NULL OR IN)`）。

## S3 互換

P0 19 ops（`ListBuckets〜ListMultipartUploads`）は全て実装、P1 はほぼ完了、P2 のうち `?acl`（canned / grant ヘッダ / ACP ボディ）、`?tagging`、`?cors`、`?lifecycle` は DB の `s3_subresources` テーブル（DB 未接続時はメモリ）に永続化し、入力検証、往復取得、保存済み CORS による preflight 応答と `Access-Control-*` ヘッダ付与に対応します。`x-amz-tagging` は PutObject / CopyObject（`x-amz-tagging-directive`）に追従し、オブジェクトの上書き・削除・マルチパート完了・バケット削除で保存済みの設定を掃除します。`GET ?versioning` は未バージョニングを返し、有効化は `501`。`?analytics` / `?inventory` / `?metrics` は `?id=` 指定の設定として保存・取得・一覧・削除でき、オブジェクト単位の `?retention` / `?legal-hold` は削除・上書き（PutObject / CopyObject / CompleteMultipartUpload / DeleteObjects）をロックで保護します。`?versioning` の有効化は `501 NotImplemented`。`POST /{key}?select&select-type=2`（SelectObjectContent）は CSV / JSON（DOCUMENT・LINES）入力、NONE / GZIP / BZIP2 圧縮、CSV / JSON 出力、`RequestProgress`、SQL サブセット（`WHERE` / `LIMIT` / 集計関数 / `CAST` ほか）に対応し、イベントストリームで応答します。Parquet 入力と `ScanRange` は `501`。SSE-C（`x-amz-server-side-encryption-customer-*`、AES-256-CTR）と、`Content-MD5` / `x-amz-checksum-*` の検証（不一致は `400 BadDigest`、マルチパートは COMPOSITE / FULL_OBJECT）に対応します。`aws-chunked` ストリーミング本文（`STREAMING-AWS4-HMAC-SHA256-PAYLOAD[-TRAILER]` / `STREAMING-UNSIGNED-PAYLOAD-TRAILER`、チャンク署名・trailer 署名・trailer チェックサム）に対応します。配置不可は `507 InsufficientStorage`。`PUT/GET/DELETE ?policy` は `20KiB` / `MalformedPolicy` / `NoSuchBucketPolicy` で Misskey/Nextcloud の P0 は in-process と e2e で PASS。詳細は Compatibility を参照。

## Configuration

- `MIRYMESH_MASTER_KEY` 必須（`auth.master_key` または env、AES-GCM）
- マスター鍵のローテーション: 暗号文は `mk1:<鍵ID>:<base64>` 形式で保存され、現行鍵で暗号化、旧鍵は復号専用になります。旧形式（接頭辞なし）の暗号文も旧鍵・現行鍵で復号できます。手順は次のとおりです。
  1. 新しい鍵を `auth.master_key`（または `MIRYMESH_MASTER_KEY`）に、旧い鍵を `auth.old_master_keys`（または `MIRYMESH_MASTER_KEY_OLD`、カンマ区切り）に設定する
  2. `mirymesh rotate-master-key --config /etc/mirymesh/config.yaml [--dry-run]` で `setup.secret_key_enc` / `setup.db_dsn_enc` を現行鍵で再暗号化する（1トランザクション、冪等。`--dry-run` は書き込まない）
  3. 全ての controller を新しい鍵で再起動し、旧鍵を設定から削除する
- `db.driver/dsn`（`postgres/mysql/sqlite`、setup 前は `MIRYMESH_DB_DSN` で上書き可）
- `server.mode: gateway`（`gateway` で master に S3 実体を置かず DataNode のみに）
- `frontend/dist` は `go:embed all:dist`（`.gitkeep` で CI でも存在）

`packaging/config/config.yaml.example` を `/etc/mirymesh/config.yaml`（`0600`）に配置。`setup` テーブル（`setup/sessions/pair_tokens/buckets.policy JSONB`）に永続。

## 開発

```bash
docker compose -f docker-compose.dev.yml up --build  # air@1.49.0 -buildvcs=false + postgres:16 + NodeSource 24 + .: /src, frontend: npm run build → go:embed all:dist
docker compose -f docker-compose.dev.yml down -v      # クリーンに消える
```

### MySQL / MariaDB テスト

`MIRYMESH_TEST_MYSQL_DSN` を設定すると、MySQL 8.4 / MariaDB 11 向けの統合テスト（マイグレーション、S3 サブリソース、バケットポリシー、セットアップ / セッション / pair token）が走ります。未設定ならスキップされます。指定ユーザーには `CREATE` / `DROP DATABASE` 権限が必要で、テストごとに使い捨て DB（`mirymesh_test_*`）を作成し、終了時に削除します。CI の `mysql-integration` ジョブも同じ手順で実行します。

    docker run -d --name mirymesh-mysql-test -e MYSQL_ROOT_PASSWORD=mirytest -p 33306:3306 mysql:8.4
    MIRYMESH_TEST_MYSQL_DSN='root:mirytest@tcp(127.0.0.1:33306)/' go test ./internal/... -count=1
    docker rm -f mirymesh-mysql-test

MariaDB で試す場合は、イメージを `mariadb:11` に、環境変数を `MARIADB_ROOT_PASSWORD` に替えてください。

## Documentation

- Contributing: [`CONTRIBUTING.md`](CONTRIBUTING.md)
- Security: [`SECURITY.md`](SECURITY.md)
- License: [`LICENSE`](LICENSE) (`AGPL-3.0-only`)

## License

`AGPL-3.0-only`, see `LICENSE`. Every source file carries `SPDX-License-Identifier: AGPL-3.0-only`.
