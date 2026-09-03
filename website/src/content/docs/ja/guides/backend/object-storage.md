---
title: ファイルを保存する
description: アップロードなどのファイルのための 1 つのバケットインターフェース。開発ではディレクトリ、プロセスホストでは S3 互換ストア、Cloudflare Worker では R2 binding。
sidebar:
  order: 8
---

アップロードを受け取る、レポートをファイルに書き出す、誰かが添付した文書を配信する。こうした
ハンドラーには、データベースでもバイナリでもない「バイト列の置き場」が要ります。Popcorn Web は
そのために 1 つのインターフェースを用意し、バイト列の行き先は設定に語らせます。

```go
import "github.com/shibukawa/popcornweb/storage"

func upload(w http.ResponseWriter, r *http.Request) {
	bucket, err := storage.Open(r.Context(), "uploads")
	if err != nil {
		pw.WriteProblem(w, r, pw.InternalServerError(err))
		return
	}
	file, header, err := r.FormFile("file")
	// ...
	err = bucket.Put(r.Context(), "u1/"+header.Filename, file, storage.PutOptions{
		ContentType:   header.Header.Get("Content-Type"),
		ContentLength: header.Size,
	})
}
```

`Open` は設定で付けた名前でバケットを解決します。返る値には `Get`、`Head`、`Put`、`Delete`、
`List` があり、body は呼び出し側が閉じるストリーム、一覧はカーソルでページングされ、存在しない
キーはどのバックエンドでも `storage.ErrNotFound` なので `errors.Is` 1 つで済みます。

## 3 つのバックエンド、1 つの設定の形

```toml
[storage]
enabled = true

# config.dev.toml — プロジェクト内のディレクトリ。何も起動しない
[[storage.buckets]]
name = "uploads"
backend = "local"
directory = "uploads"
```

```toml
# プロセスホストの config.prod.toml — S3、MinIO、あるいは S3 API 経由の R2
[[storage.buckets]]
name = "uploads"
backend = "s3"
endpoint = "https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com"
region = "auto"
bucket = "myapp-uploads"
access_key_id = "${R2_ACCESS_KEY_ID}"
secret_access_key = "${R2_SECRET_ACCESS_KEY}"
```

```toml
# Cloudflare Worker の config.prod.toml — バケット binding
[[storage.buckets]]
name = "uploads"
backend = "r2"
binding = "UPLOADS"
```

アプリケーションは使うバックエンドをデータベースエンジンと同じく blank import でリンクします。
`storage/local`、`storage/s3`、`cloudflare/r2` です。Worker の build は `cloudflare/r2` を自分で
リンクし、それ以外のバックエンドをビルド時と起動時に拒否します。ディレクトリもソケットも届かない
からです。同じファイルの `local` や `s3` はプロセスホストでは正しい設定です。資格情報は `${NAME}`
参照で書き、直接は書きません。

この配列は `STORAGE_BUCKETS` という 1 つの環境変数に JSON として載せて Worker に届きます。
`pw build --target cloudflare-workers` が `config.prod.toml` から書き出し、どのホストでも手で
設定できます。

## 使わない場面

アプリケーションと一緒に出荷するファイルは public ツリー（埋め込みまたは
[外部](/ja/guides/frontend/public-assets/)）に置くべきで、build がバリデータを計算し、マウントが
キャッシュヘッダー付きで配信します。リクエストごとに読む値は[データキャッシュ](/ja/guides/backend/data-cache/)か
データベースの領分で、リクエストごとのバケット往復はこのインターフェースが隠そうとしない遅い経路です。
presigned URL と multipart アップロードはまだなく、ハンドラー自身がバイト列を流します。
