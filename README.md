<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# MiryMesh S3

複数のサーバー・ディスクにまたがって使える、Linux（Debian / Alma / Rocky）向けの S3 互換ストレージです。Master（S3 :9000 / Admin :9001）と DataNode（:9002、複数パス可）で構成し、LXC を第一にしています。Docker は任意です。

*MiryMesh is a distributed S3-compatible storage for Linux. Master (S3:9000 / Admin:9001) + DataNode (:9002) across disks/servers. LXC-first, Docker optional.*

> **注意**: `0.0.0.0:900x` を public に晒さないでください。`X-Admin-Token` / `X-Internal-Token` に加えて、Tailscale（`<tailscale-ip>:900x`）、`127.0.0.1:900x`、ファイアウォールのいずれかで閉じます。`admin_listen: 127.0.0.1:9001` で Admin を localhost だけに絞ることもできます。

## 構成

    S3 client (Nextcloud / Misskey / rclone / aws-cli)
            |  S3 API (SigV4)
            v
    +-------------------------------+
    | Master (controller)           |
    |   S3 :9000 / Admin :9001      |
    |   metadata DB                 |
    +---------------+---------------+
                    |  register / heartbeat / data
           +--------+--------+
           v                 v
    DataNode :9002    DataNode :9002    (multiple disks per node)

| ポート | 役割 | 公開範囲 |
| --- | --- | --- |
| 9000 | S3 API | NGINX / HAProxy の TLS 終端経由で公開できる（[`examples/nginx/`](examples/nginx/)） |
| 9001 | Admin UI / API | 公開しない（localhost / Tailscale のみ） |
| 9002 | DataNode | 公開しない（Master から届けばよい） |

## クイックスタート

### 1. まず試す（Docker 開発環境）

    docker compose -f docker-compose.dev.yml up --build

1. ブラウザで `http://127.0.0.1:9001/setup` を開き、Admin を作成します（username / password を送ると `201` と `admin_token` が返ります）。
2. login して Step 2 に進み、S3 / DB / Storage を設定します（Access は Private / Public Read / Custom、Datanode Paths も指定）。
3. Dashboard の Nodes で Node を追加します。

DB は compose が `postgres:16` を立てるので、ローカルに用意する必要はありません。`MIRYMESH_MASTER_KEY` も dev では自動で入ります。

### 2. Master を入れる（本番）

    curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash -s -- --master

- 事前に PostgreSQL か MySQL / MariaDB が必要です（`apt: postgresql`、`dnf: postgresql-server / mariadb-server`）。チェックを飛ばすなら `--skip-db-check` を付けます。
- `MIRYMESH_MASTER_KEY` が必須です（[設定](#設定)）。環境変数ファイルで渡す例は [`examples/systemd/`](examples/systemd/) にあります。
- 入れ終わったら、ブラウザで `http://127.0.0.1:9001/setup`（Tailscale 経由なら `http://<tailscale-ip>:9001/setup`）を開いて初期設定をします。

### 3. DataNode を足す

Master の Web UI で **Nodes → 「Node を追加」** を開くと、次の形のコマンドが表示されます。DataNode にしたいサーバーに貼り付けて実行するだけです。

    curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash -s -- --client --master-url http://<master>:9001 --pair-token <ワンタイムトークン> --path /mnt/disk1

このコマンドは次の処理を行います。

- リリースから静的リンクの `mirymesh-datanode` をダウンロードし、sha256 を検証して `/usr/local/bin` に入れます。この場合、Go も git も不要です。
- リリースに該当ファイルが無いときは、ソースビルドに自動で切り替わります（git と Go が必要）。`release` ブランチへの merge で release ワークフローが走るまでは、この経路になります。
- `mirymesh` ユーザー、`/etc/mirymesh/config.yaml`、systemd ユニットを作ります。
- `--path` のディレクトリを `mirymesh` の所有にして、サービスから書き込めるようにします。ユニットは `ProtectSystem=strict` なので、指定しないと書けません。
- ワンタイムトークンで Master に登録し、成功したらトークンをこのサーバーから消します。以降の再起動には保存済みのノードトークンが使われます。

注意点:

- `pair-token` は 15 分で失効し、1 回しか使えません。
- Node ID はホスト名です。Master が `<ホスト名>:9002` でこのノードに届く必要があります（Tailscale MagicDNS、DNS、`/etc/hosts` など）。
- ディスクを増やすときは、`--pair-token` を外し、使うディスクの `--path` を全部並べて再実行します（例: `--client --master-url http://<master>:9001 --path /mnt/disk1 --path /mnt/disk2`）。保存先の一覧は置き換わり、サービスは再起動されます。`--path` を省略すると `/var/lib/mirymesh/data` を使います。
- 状態確認: `systemctl status mirymesh-datanode` / `journalctl -u mirymesh-datanode -f`

#### install.sh のオプション

`--opt VALUE` と `--opt=VALUE` のどちらでも指定できます。`--help` で一覧が出ます。

| オプション | 説明 |
| --- | --- |
| `--master` / `--client` | 役割。省略すると対話で選びます |
| `--master-url URL` | Master の URL（Nodes 画面が表示するもの） |
| `--pair-token TOKEN` | ワンタイムのペアリングトークン |
| `--path DIR` | DataNode の保存先（複数指定可、既定 `/var/lib/mirymesh/data`） |
| `--release` / `--version TAG` | リリースのバイナリを使う / タグを固定する（`--client` の既定は `--release`） |
| `--from-source` / `--deb` / `--rpm` | ソースビルド（`--master` の既定）/ DEB / RPM から入れる |
| `--no-enable` | インストールだけして起動しない |
| `--skip-db-check` | Master の DB 有無チェックを飛ばす |
| `--update` | 入っている MiryMesh をその場で更新する（役割は自動判定、設定とデータは保持） |
| `--yes` / `-y` | 何も聞かず、既定値で進める（スクリプト向け） |
| `--no-self-update` | DataNode のワンクリック更新を有効にしない |

#### 対話式で入れる

`--master` / `--client` を付けずに実行すると、役割、Master の URL、ペアリングトークン、保存先を順に聞き、最後に内容を確認します。`curl ... | sudo bash` のようにパイプで渡した場合も、端末（`/dev/tty`）から読みます。聞かれたくないとき（自動化など）は `--yes` を付けます。

#### 更新する

新しいリリース（例: `v1.0.0-rc.1`）が公開されると、Master の Web UI の上部にお知らせが出ます。閉じたお知らせは、次のバージョンが出るまで再表示されません。

- **Master:** 次のコマンドで更新します。`--version v1.0.0-rc.1` のようにタグを固定することもできます。

      curl -fsSL https://raw.githubusercontent.com/sylphre/MiryMesh/master/packaging/scripts/install.sh | sudo bash -s -- --update

- **DataNode:** **Nodes** 画面の Version 列が Master と違うノードに「更新」ボタンが出ます。押すと、DataNode がリリースから静的バイナリをダウンロードして sha256 を検証し、終了します。systemd が再起動する前に、root 権限のヘルパー（`/usr/local/lib/mirymesh/apply-update.sh`）がバイナリを差し替えます。直前のバイナリは `<バイナリ>.prev` に残ります。
- ワンクリック更新を使うには、各 DataNode で `install.sh --update` を 1 回実行して有効化します。それ以降は Master から更新できます。無効のままにしたいときは `--no-self-update` を付けます。
- 安定版のリリースがまだ無い間は、最新のプレリリースを使います。

### その他のインストール方法

ソースから手動で入れる場合:

    git clone https://github.com/sylphre/MiryMesh && cd MiryMesh
    go build -o /tmp/mirymesh-controller ./cmd/controller
    sudo install -m 0755 /tmp/mirymesh-controller /usr/local/bin/
    sudo systemctl enable --now mirymesh-controller

DEB / RPM のビルドと入れ方は [`packaging/README.md`](packaging/README.md) を参照してください。設定の雛形は [`packaging/config/config.yaml.example`](packaging/config/config.yaml.example)、インストーラ本体は [`packaging/scripts/install.sh`](packaging/scripts/install.sh) です。

## 設定例（examples/）

NGINX と systemd の設定例を [`examples/`](examples/) にまとめています。

- [`examples/nginx/`](examples/nginx/): S3 を TLS 終端の裏に置く例、Public Read のバケットを公開ドメインから配る例
- [`examples/systemd/`](examples/systemd/): データ用ディスクを書き込み可にする drop-in、Master 鍵を環境変数ファイルで渡す drop-in、Tailscale 起動順の drop-in

## 設定

`packaging/config/config.yaml.example` を `/etc/mirymesh/config.yaml`（`0600`）に置きます。`setup` テーブル（`setup` / `sessions` / `pair_tokens` / `buckets.policy JSONB`）に永続化されます。

- `MIRYMESH_MASTER_KEY` が必須です（`auth.master_key` または環境変数、AES-GCM）。
- `db.driver` / `db.dsn`（`postgres` / `mysql` / `sqlite`）。setup 前は `MIRYMESH_DB_DSN` で上書きできます。
- `server.mode: gateway`: Master に S3 の実体を置かず、DataNode のみに保存します。
- `frontend/dist` は `go:embed all:dist` で埋め込まれます（`.gitkeep` により CI でも存在します）。

### マスター鍵のローテーション

暗号文は `mk1:<鍵ID>:<base64>` 形式で保存されます。現行鍵で暗号化し、旧鍵は復号専用です。旧形式（接頭辞なし）の暗号文も、旧鍵と現行鍵で復号できます。

1. 新しい鍵を `auth.master_key`（または `MIRYMESH_MASTER_KEY`）に、旧い鍵を `auth.old_master_keys`（または `MIRYMESH_MASTER_KEY_OLD`、カンマ区切り）に設定します。
2. `mirymesh rotate-master-key --config /etc/mirymesh/config.yaml [--dry-run]` を実行し、`setup.secret_key_enc` / `setup.db_dsn_enc` を現行鍵で再暗号化します（1 トランザクション、冪等。`--dry-run` は書き込みません）。
3. 全ての controller を新しい鍵で再起動し、旧鍵を設定から削除します。

## アーキテクチャ

- Master は Gin v1.12 で S3 `:9000`、Admin `:9001`、DataNode 用 `:9002` を提供します。
- DB は `postgres:16` が primary で、`mysql` / `mariadb` と `sqlite`（dev 用、`mattn/go-sqlite3`）にも対応します。DB 名は `mirymesh` で判別し、テーブル名に `miry_` prefix はありません（`setup` / `sessions` / `pair_tokens` / `buckets` / `objects` / `placement`）。
- `MIRYMESH_MASTER_KEY` が必須で、`secret_key_enc` と `dsn_enc` は AES-GCM で暗号化します。
- `buckets.policy JSONB` に `?policy` を永続化します。
- gateway は `server.mode: gateway` で有効になり、Master に実体を置かず DataNode のみに保存します（`setup.db_driver CHECK(IS NULL OR IN)`）。

## S3 互換

Misskey / Nextcloud の P0 は in-process テストと e2e で PASS しています。

**実装済み**

- P0 の 19 オペレーション（`ListBuckets`〜`ListMultipartUploads`）は全て実装済みで、P1 もほぼ完了しています。
- `?acl`（canned / grant ヘッダ / ACP ボディ）、`?tagging`、`?cors`、`?lifecycle` は DB の `s3_subresources` テーブルに永続化します（DB 未接続時はメモリ）。入力検証、往復取得、保存済み CORS による preflight 応答と `Access-Control-*` ヘッダ付与に対応します。
- `x-amz-tagging` は PutObject / CopyObject（`x-amz-tagging-directive`）に追従します。オブジェクトの上書き・削除、マルチパート完了、バケット削除で、保存済みの設定を掃除します。
- `?analytics` / `?inventory` / `?metrics` は `?id=` 指定の設定として、保存・取得・一覧・削除ができます。
- オブジェクト単位の `?retention` / `?legal-hold` は、削除と上書き（PutObject / CopyObject / CompleteMultipartUpload / DeleteObjects）をロックで保護します。
- `PUT/GET/DELETE ?policy` は `20KiB` 上限、`MalformedPolicy`、`NoSuchBucketPolicy` に対応します。
- `POST /{key}?select&select-type=2`（SelectObjectContent）は、次に対応しイベントストリームで応答します。
  - 入力: CSV / JSON（DOCUMENT・LINES）、圧縮 NONE / GZIP / BZIP2
  - 出力: CSV / JSON、`RequestProgress`
  - SQL サブセット: `WHERE` / `LIMIT` / 集計関数 / `CAST` ほか
- SSE-C（`x-amz-server-side-encryption-customer-*`、AES-256-CTR）に対応します。
- `Content-MD5` / `x-amz-checksum-*` を検証します。不一致は `400 BadDigest` で、マルチパートは COMPOSITE / FULL_OBJECT です。
- `aws-chunked` ストリーミング本文（`STREAMING-AWS4-HMAC-SHA256-PAYLOAD[-TRAILER]` / `STREAMING-UNSIGNED-PAYLOAD-TRAILER`）に対応します。チャンク署名、trailer 署名、trailer チェックサムを扱います。署名付きチャンクは 16MiB を超えても、ハッシュを逐次計算して流し、最後のバイトを渡す前に署名を検証します。

**未対応・制限**

- `GET ?versioning` は未バージョニングを返します。有効化は `501 NotImplemented` です。
- Parquet 入力と `ScanRange`（SelectObjectContent）は `501` です。
- SigV4A（`AWS4-ECDSA-P256-SHA256` / `STREAMING-AWS4-ECDSA-P256-*`）、trailer チェックサムの複数同時指定、SSE-S3 / SSE-KMS は未対応です。方針と難易度は [`todo.md`](todo.md) にまとめています。
- 配置先が無い場合は `507 InsufficientStorage` を返します。

## 開発

    docker compose -f docker-compose.dev.yml up --build   # air@1.49.0 -buildvcs=false + postgres:16 + NodeSource 24 + .: /src, frontend: npm run build → go:embed all:dist
    docker compose -f docker-compose.dev.yml down -v      # クリーンに消える

### MySQL / MariaDB テスト

`MIRYMESH_TEST_MYSQL_DSN` を設定すると、MySQL 8.4 / MariaDB 11 向けの統合テスト（マイグレーション、S3 サブリソース、バケットポリシー、セットアップ / セッション / pair token）が走ります。未設定ならスキップされます。指定ユーザーには `CREATE` / `DROP DATABASE` 権限が必要で、テストごとに使い捨て DB（`mirymesh_test_*`）を作成し、終了時に削除します。CI の `mysql-integration` ジョブも同じ手順で実行します。

    docker run -d --name mirymesh-mysql-test -e MYSQL_ROOT_PASSWORD=mirytest -p 33306:3306 mysql:8.4
    MIRYMESH_TEST_MYSQL_DSN='root:mirytest@tcp(127.0.0.1:33306)/' go test ./internal/... -count=1
    docker rm -f mirymesh-mysql-test

MariaDB で試す場合は、イメージを `mariadb:11` に、環境変数を `MARIADB_ROOT_PASSWORD` に替えてください。

## ドキュメント

- 設定例: [`examples/`](examples/)
- パッケージング: [`packaging/README.md`](packaging/README.md)
- Contributing: [`CONTRIBUTING.md`](CONTRIBUTING.md)
- Security: [`SECURITY.md`](SECURITY.md)
- License: [`LICENSE`](LICENSE)（`AGPL-3.0-only`）

## License

`AGPL-3.0-only`。詳細は `LICENSE` を参照してください。全てのソースファイルに `SPDX-License-Identifier: AGPL-3.0-only` を付けています。