<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# examples/

MiryMesh を運用するときの設定例です。そのまま使うものではなく、コピーして自分の環境に合わせて書き換えます。ファイルの先頭コメントに、配置先と手順を書いています。

| ファイル | 内容 |
| --- | --- |
| `nginx/mirymesh-s3.conf.example` | S3 (:9000) を NGINX の TLS 終端の裏に置く。SigV4 を壊さないヘッダ設定入り |
| `nginx/mirymesh-public-read.conf.example` | Public Read のバケットを公開ドメインから GET / HEAD だけで配る |
| `systemd/datanode-data-disks.conf.example` | DataNode のデータ置き場を書き込み可にし、マウント完了まで起動を待つ drop-in |
| `systemd/controller-secrets.conf.example` | Master 鍵などを環境変数ファイルで渡す drop-in |
| `systemd/controller.env.example` | 上の drop-in が読む環境変数ファイルの雛形 |
| `systemd/after-tailscale.conf.example` | Tailscale の IP で待ち受けるとき、tailscaled の後に起動する drop-in |

Admin (:9001) を NGINX の裏に置く例と HAProxy の例は、従来どおり `packaging/nginx/` と `packaging/haproxy/` にあります。

## systemd の drop-in について

`packaging/systemd/*.service` は直接編集せず、`/etc/systemd/system/<unit>.service.d/*.conf` に差分だけを置きます。パッケージを更新しても設定が消えません。

    sudo systemctl edit mirymesh-datanode      # エディタで drop-in を作る方法
    sudo systemctl cat mirymesh-datanode       # 反映後のユニット全体を確認する
    sudo systemctl daemon-reload && sudo systemctl restart mirymesh-datanode

## NGINX の例を使う

    sudo cp examples/nginx/mirymesh-s3.conf.example /etc/nginx/conf.d/mirymesh-s3.conf
    # server_name と証明書のパスを書き換える
    sudo nginx -t && sudo systemctl reload nginx