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
# One-liner install (LXC, no Docker) — builds from source or installs DEB/RPM
curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash
# or locally, after clone:
sudo bash packaging/scripts/install.sh          # build from source
sudo bash packaging/scripts/install.sh --deb    # or: use prebuilt DEB (needs nfpm-built artifact)
sudo bash packaging/scripts/install.sh --rpm    # or: RPM

# then edit config and start
sudoedit /etc/mirymesh/config.yaml
sudo systemctl enable --now mirymesh-controller
```

スクリプトは `preinstall.sh`/`postinstall.sh` と同様に `mirymesh:mirymesh` ユーザを作成し、`/var/lib/mirymesh` を用意します。

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

P0（`docs/06`）：`Put/Get/Delete/List/Head`、`CopyObject`、`DeleteObjects`、`multipart`（Nextcloud / Misskey で検証: `forcePathStyle` / `requestChecksumCalculation`）。`GET /` の `2006-03-01` XML xmlns を満たし、エラーも `application/xml`（`NoSuchBucket`/`NoSuchKey` 等）で返却。`2006-03-01` は API バージョン識別子であり、AWS 側の操作追加とは独立して追跡。

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
