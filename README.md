<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# MiryMesh S3

<p lang="ja">
MiryMesh は Linux（Debian / Alma / Rocky）向けの分散 S3 互換オブジェクトストレージです。S3 互換 API を提供しつつ、複数の物理ディスク / サーバに容量を分散できます。LXC 上での素の Linux 運用を主とし、Docker は任意です。
</p>

<p lang="en">
MiryMesh is a distributed, S3-compatible object storage for Linux (Debian / Alma / Rocky). It exposes an S3-compatible API while spreading capacity across multiple disks and servers. LXC-first, Docker is optional.
</p>

---

## Quick start / クイックスタート — LXC（Docker 不使用 / without Docker）

> 最短：LXC 上でビルド or DEB/RPM インストール → `config.yaml` を1ファイル用意 → `systemctl enable --now` で起動。詳細は [`packaging/README.md`](packaging/README.md) と [`packaging/config/config.yaml.example`](packaging/config/config.yaml.example)。

> Shortest path: on LXC, build from source or install DEB/RPM → put one `config.yaml` → `systemctl enable --now`.

### 1) LXC 上でビルドして起動（最短 / fastest from source）

```bash
# inside LXC (Debian 12/13, Ubuntu 24.04, Alma/Rocky 9/10)
sudo apt update && sudo apt install -y golang-go git  # or: sudo dnf install -y golang git
git clone https://github.com/sylphre/MiryMesh && cd MiryMesh

# build (no Docker required)
go build -o /tmp/mirymesh-controller ./cmd/controller
go build -o /tmp/mirymesh-datanode   ./cmd/datanode
go build -o /tmp/mirymesh             ./cmd/mirymesh

# minimal config (single-node)
sudo mkdir -p /etc/mirymesh /var/lib/mirymesh
sudo cp packaging/config/config.yaml.example /etc/mirymesh/config.yaml
sudoedit /etc/mirymesh/config.yaml   # set auth.credentials / server.data_dir
sudo install -m 0755 /tmp/mirymesh-controller /usr/local/bin/
sudo install -m 0755 /tmp/mirymesh       /usr/local/bin/
sudo cp packaging/systemd/mirymesh-controller.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now mirymesh-controller
curl -f http://127.0.0.1:9000/healthz   # ok
```

### 2) 簡単インストールスクリプト（LXC 推奨 / recommended for LXC）

```bash
# One-liner install (LXC, no Docker) — Master / Client を選択してインストール
# 対話式: Master か Client を選べる（Client は Master の URL を聞かれる）
curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash
# 対話なしで Master を入れる
curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash -s -- --master
# 対話なしで Client（DataNode）を入れる
curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash -s -- --client --master-url http://master:9000

# ローカルに clone 済みの場合
sudo bash packaging/scripts/install.sh                 # 対話式で Master/Client 選択
sudo bash packaging/scripts/install.sh --master        # Master として
sudo bash packaging/scripts/install.sh --client --master-url http://master:9000  # Client として
sudo bash packaging/scripts/install.sh --client --master-url http://master:9000 --deb  # DEB で Client
sudo bash packaging/scripts/install.sh --help          # ヘルプ

# Master はそのまま起動、ブラウザでセットアップを完了
sudo systemctl enable --now mirymesh-controller
# ブラウザで http://master:9001/ を開く → 初回のみ /static/setup.html へリダイレクト（Admin は 9001）
# AccessKey / Secret / Policy を入力 → Admin Token は localStorage に保存（:9001 は 127.0.0.1 / Tailscale のみに閉じること）
# 以降は http://master:9001/static/settings.html で default_policy / DataNode paths を確認・変更
```

スクリプトは `preinstall.sh`/`postinstall.sh` と同様に `mirymesh:mirymesh` ユーザを作成し、`/var/lib/mirymesh` を用意します。
Client は `install.sh --client --master-url http://master:9000` で `datanode.controller_url` が既に注入されるため、セットアップ画面は出ません。不足があれば Web の Settings で `datanode.paths` を確認してください。
Master では `mirymesh-controller` が 2 ポートで起動します（S3 は `:9000`、Admin GUI/API は `:9001`）。`:9001` は `admin_listen`（例: `127.0.0.1:9001` や Tailscale IP）で閉じること（`SECURITY.md`）。Client では `mirymesh-datanode` が Master に登録して容量を提供します。Client の `datanode.paths`（例: `["/mnt/disk1"]`）と `datanode.controller_url` はスクリプトが `--master-url` から自動設定します。

| Port | 用途 | 例 |
|---|---|---|
| `:9000` | S3 API（`PUT/GET` 等、SigV4） | `curl -f http://127.0.0.1:9000/healthz` |
| `:9001` | Admin GUI/API（`setup`/`settings`/`/api/admin/*`、Token `X-Admin-Token`） | `curl -H "X-Admin-Token: $TOKEN" http://127.0.0.1:9001/api/admin/health` |

`9000` の `/api/admin/*` は `404 Admin API lives on admin port (:9001)` を返す。

### 3) DEB/RPM から（任意 / via package）

```bash
# after: go build -trimpath ... && nfpm pkg --packager deb --config packaging/nfpm.yaml --target mirymesh.deb
sudo apt install ./mirymesh.deb    # Debian/Ubuntu
# or: sudo rpm -ivh mirymesh.rpm   # Alma/Rocky
sudoedit /etc/mirymesh/config.yaml
sudo systemctl enable --now mirymesh-controller
```

### 4) Docker を使う場合（任意 / optional）

```bash
docker build -f packaging/docker/Dockerfile -t mirymesh:dev .
docker run -p 9000:9000 -v /var/lib/mirymesh:/var/lib/mirymesh mirymesh:dev
```

---

## S3 互換 / S3 compatibility

P0（Nextcloud / Misskey が実利用する最小セット）は全て実装済み。`GET /` の `2006-03-01` XML `xmlns` を満たし、エラーも `application/xml`（`NoSuchBucket`/`NoSuchKey` 等）で返却。`2006-03-01` は API バージョン識別子であり、AWS 側の操作追加とは独立して追跡（[AWS S3 API Reference `2006-03-01`](https://docs.aws.amazon.com/AmazonS3/latest/API/Welcome.html) を一次参照）。

| S3 操作 | HTTP | 状態 | 備考 |
|---|---|---|---|
| `ListBuckets` | `GET /` | ✅ | `2006-03-01` xmlns |
| `HeadBucket` | `HEAD /{bucket}` | ✅ | `X-Amz-Bucket-Region` |
| `CreateBucket` | `PUT /{bucket}` | ✅ | `InvalidBucketName 400` / `BucketAlreadyOwnedByYou 409` |
| `DeleteBucket` | `DELETE /{bucket}` | ✅ | `BucketNotEmpty 409` |
| `GetBucketLocation` | `GET /{bucket}?location` | ✅ | |
| `ListObjectsV2` | `GET /{bucket}?list-type=2` | ✅ | `prefix/delimiter/max-keys/encoding-type=url/continuation-token` + `CommonPrefixes` |
| `HeadObject` | `HEAD /{bucket}/{key}` | ✅ | `If-None-Match`/`If-Modified-Since` → `304` |
| `GetObject` | `GET /{bucket}/{key}` | ✅ | `Range: bytes=` → `206`, `ETag`/`Content-Type`/`x-amz-meta-*` |
| `PutObject` | `PUT /{bucket}/{key}` | ✅ | ストリーム `io.Copy+md5`（5GiB 対応）, `x-amz-meta-*` |
| `DeleteObject` | `DELETE /{bucket}/{key}` | ✅ | 冪等 `204` |
| `CopyObject` | `PUT x-amz-copy-source` | ✅ | `COPY/REPLACE` + `x-amz-copy-source-if-*` → `412`, 5GiB 制限, `?versionId` 無視 |
| `DeleteObjects` | `POST /?delete` | ✅ | 1000 件, `<DeleteResult>`, `Quiet` |
| `CreateMultipartUpload` | `POST ?uploads` | ✅ | |
| `UploadPart` | `PUT ?uploadId&partNumber` | ✅ | ストリーム |
| `UploadPartCopy` | `PUT ?uploadId&partNumber x-amz-copy-source` | ✅ | `x-amz-copy-source-range` 対応 |
| `ListParts` | `GET ?uploadId` | ✅ | |
| `CompleteMultipartUpload` | `POST ?uploadId + XML` | ✅ | `ETag = hex(md5(concat md5s))-N`, ストリーム連結 |
| `AbortMultipartUpload` | `DELETE ?uploadId` | ✅ | |
| `ListMultipartUploads` | `GET /{bucket}?uploads` | ✅ | |
| SigV4（header / presigned + `UNSIGNED-PAYLOAD`） | — | ✅ | RFC3986 `%20` 正規化, 時計 ±15 分 |

P1 は `UploadPartCopy` を含めほぼ完了。`GetObjectAttributes` は P2 として `501 NotImplemented` のまま。
P2（`?acl/tagging/policy/cors/lifecycle/versioning/select` 等）は `501 NotImplemented` XML で返す。配置不可（quota / ノード不足）は `507 InsufficientStorage`。

Nextcloud / Misskey は `forcePathStyle` / `requestChecksumCalculation` を含め `tests/e2e/s3compat` の in-process E2E（`sigv4.Signer` + `httptest`）で P0 全行程を PASS。`tests/e2e/nextcloud/misskey` は `docker-compose.yml` + `-tags=e2e_external` の 1 bucket 1 file smoke を `workflow_dispatch` で実行。置き場の決定（`single/dual/triple/ALL`）や 95% quota、再起動での中断復旧を備える。

## 設定 / Configuration

`packaging/config/config.yaml.example` を起点に `/etc/mirymesh/config.yaml`（`0600`）を配置。`auth.credentials`（`AccessKey/SecretKey`）、`server.region` / `data_dir`、`storage.default_policy`（`single/dual/triple/ALL`）。詳細は `internal/config` の集中バリデーションを参照。

## Documentation

- Contributing: [`CONTRIBUTING.md`](CONTRIBUTING.md) (dev setup, workflow, ADRs)
- Security: [`SECURITY.md`](SECURITY.md) (private reporting)
- Support: [`SUPPORT.md`](SUPPORT.md) (supported versions)
- Changelog: [`CHANGELOG.md`](CHANGELOG.md) (Keep a Changelog)
- License: [`LICENSE`](LICENSE) (`AGPL-3.0-only`)

## License

`AGPL-3.0-only`, see `LICENSE`. Every source file carries `SPDX-License-Identifier: AGPL-3.0-only`.
