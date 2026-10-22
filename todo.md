<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<!-- Copyright (C) 2014-2026 Laurier Sylph. All rights reserved. -->
# TODO

未対応・保留の項目をここに集約する。完了したら `[x]` にして、README.md の「S3 互換」の節に反映する。

## aws-chunked / 認証まわり

### 16MiB を超える署名付きチャンク

- [x] 署名付きチャンクを 16MiB 超でも受け付ける（`internal/s3api/aws_chunked.go`）
  - 従来は署名検証のためにチャンク全体をメモリに載せ、`awsChunkMaxSigned = 16 << 20` を超えると `InvalidRequest` で拒否していた
  - 実装: 16MiB 以下（`awsChunkBufMax`）は従来どおり「検証してから渡す」。超える分はハッシュを逐次計算しながらハンドラへ流し、チャンク末尾の署名を、最後のバイトを渡す前に検証する（不一致なら `SignatureDoesNotMatch` を Read のエラーで返す）
  - `sigv4.StreamSigner` に、ハッシュ済みの値を受け取る `ChunkSum` を追加
  - 注意: 検証前のバイトがハンドラに渡るため、ハンドラは Read エラー時に保存を確定しないこと（`TestAWSChunkedKeepsExistingObjectOnFailure` と `TestAWSChunkedLargeSignedChunk` が担保）

### 複数 trailer チェックサムの同時指定（対応しない方針）

- [ ] 方針の最終確認
  - 現状は `x-amz-trailer` に `x-amz-checksum-*` が複数あると `InvalidRequest` で拒否している
  - 本家 S3 も、1 リクエストに複数の checksum 種別を指定すると拒否すると記憶している（要確認）。その場合、現状の挙動が互換であり実装しない
  - 本家が許可すると確認できた場合のみ、複数アルゴリズムの保存形式（`objects` / multipart メタデータ）から設計し直す。`checksum.go` が単一アルゴリズム前提のため変更範囲は広い

### SigV4A（`AWS4-ECDSA-P256-SHA256` / `STREAMING-AWS4-ECDSA-P256-*`）

難易度: 高。`internal/sigv4` に ECDSA 系の検証経路を新設する必要がある。以下は記憶ベースのメモなので、実装前に AWS の公式仕様と照合する。

- [ ] 認証ヘッダ `AWS4-ECDSA-P256-SHA256` の解析と `X-Amz-Region-Set` の検証（`*` やカンマ区切り。credential scope に region は含まれない）
- [ ] シークレットキーから ECDSA P-256 鍵ペアを導出（HMAC-SHA256 ベースのカウンタ KDF）し、公開鍵で署名（DER）を検証
- [ ] presigned URL（`X-Amz-Algorithm=AWS4-ECDSA-P256-SHA256`）
- [ ] ストリーミング: `STREAMING-AWS4-ECDSA-P256-SHA256-PAYLOAD[-TRAILER]`。チャンク署名・trailer 署名も ECDSA になる（`StreamSigner` の ECDSA 版が必要）
- [ ] テスト: 公式ベクタ、または SDK で生成した実リクエスト。自前の署名器だけで検証しない
- [ ] 利用する主な場面は S3 Multi-Region Access Point。単一クラスタ構成では優先度は低い

## 暗号化

### SSE-S3（`x-amz-server-side-encryption: AES256`）

難易度: 中。`sse.go` の SSE-C（AES-256-CTR）と、今回入れた `metadata.Keyring` を再利用できる。

- [ ] オブジェクトごとにデータキー（DEK）を生成し、マスター鍵（Keyring の現行鍵）で wrap して `objects` のメタデータに保存
- [ ] PutObject / CopyObject / multipart（`CreateMultipartUpload` で指定、全パートに適用）/ Range GET / HEAD の応答ヘッダ
- [ ] バケットのデフォルト暗号化（`?encryption`）
- [ ] マスター鍵ローテーション時の DEK の rewrap（`rotate-master-key` の対象拡張）
- [ ] 判断が必要: DataNode 上の保存形式（平文か暗号文か）、チェックサムは平文に対して計算する（SSE-C と同じ扱い）

### SSE-KMS（`aws:kms`）

難易度: 高。外部 KMS をどうするかが決まらないと着手できない。

- [ ] 判断が必要: AWS KMS / Vault などの外部連携か、ローカル Keyring を KMS 代替にするか
- [ ] `x-amz-server-side-encryption-aws-kms-key-id`、暗号化コンテキスト、bucket key の扱い
- [ ] SSE-S3 の DEK 管理を先に作り、その上に KMS を載せる（SSE-S3 → SSE-KMS の順）