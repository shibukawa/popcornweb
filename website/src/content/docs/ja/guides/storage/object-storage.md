---
title: オブジェクトストレージ
description: アップロードなどのファイルを 1 つのバケットインターフェースで扱い、開発・プロセスホスト・Worker で背後のストアだけを切り替える。
sidebar:
  order: 3
---

アップロードの置き場はデータベースでもコンテナのディスクでもありません。置くべき先はほぼすべて
S3 API を話します。AWS S3、Cloudflare R2、MinIO、RustFS、Wasabi。一方 Cloudflare Worker は R2 に
binding 経由で到達します。どれか 1 つに向けて書いたハンドラーはそこに縛られます。Popcorn Web は
ハンドラーに 1 つのインターフェースを渡し、バイト列の行き先は設定に語らせます。同じソースが
ラップトップでも、コンテナでも、エッジでも動くように。

```go
package handlers

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"path"

	"github.com/shibukawa/popcornweb/pw"
	"github.com/shibukawa/popcornweb/storage"
	"github.com/shibukawa/tinybind-go"
)

type uploadInput struct {
	Title string        `payload:"title" check:"required,maxlen=80"`
	File  tinybind.File `payload:"file" check:"required"`
}

type uploadResult struct {
	Key string `json:"key"`
}

func init() { mux.HandleFunc("POST /uploads", upload) }

// newObjectID is a random key segment; the client's file name never becomes one.
func newObjectID() string { return rand.Text() }

func upload(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[uploadInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	bucket, err := storage.Open(r.Context(), "uploads")
	if err != nil {
		pw.WriteProblem(w, r, pw.InternalServerError(err))
		return
	}
	// Filename はクライアントが決めるので、metadata として運び、キーには使わない。
	key := "u1/" + newObjectID() + path.Ext(input.File.Filename)
	err = bucket.Put(r.Context(), key, bytes.NewReader(input.File.Content), storage.PutOptions{
		ContentType:   input.File.ContentType,
		ContentLength: int64(len(input.File.Content)),
		Metadata:      map[string]string{"title": input.Title, "filename": input.File.Filename},
	})
	if err != nil {
		pw.WriteProblem(w, r, pw.InternalServerError(err))
		return
	}
	pw.WriteAPI(w, r, uploadResult{Key: key})
}
```

`storage.Open` は設定で付けた名前でバケットを解決します。返る値には `Get`、`Head`、`Put`、`Delete`、
`List`、`Presign` があります。body は呼び出し側が閉じるストリーム、一覧はカーソルでページングされ、
存在しないキーはどのバックエンドでも `storage.ErrNotFound` なので、`errors.Is` 1 つで済みます。

バイト列がバケットに届く前に 2 つの上限が効きます。`server.max_request_body`（既定 10 MiB）と
multipart body の上限です。本物のファイルを受けるエンドポイントでは両方を上げてください。そして
`ContentType` が意味を持つのはアップロード時ではなくダウンロード時です。ストアは送られた値を
オブジェクトの `Content-Type` としてあとで返すので、オブジェクトをブラウザに返すアプリケーションは
part のヘッダーを信じずに型を自分で決めます。

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
`storage/local`、`storage/s3`、`cloudflare/r2` です。まず `local` から始めてください。何も起動せず、
各オブジェクトをファイルとして保ち、メディアタイプと metadata は sidecar に置きます。`pw dev` と
テストスイートはこれで動かすものです。プロセスホストでは `s3` に移ります。プロバイダー間で違う設定は
endpoint だけで、アドレッシングは `amazonaws.com` なら virtual-host 形式、それ以外は path 形式が既定です。
S3 互換サーバーが期待するのはそちらだからです。Worker の build は `cloudflare/r2` を自分でリンクし、
`local` と `s3` をビルド時と起動時に拒否します。ディレクトリもソケットも届かないからです。同じファイルの
それらのバックエンドはプロセスホストでは正しい設定です。

資格情報は `${NAME}` 参照で書き、直接は書きません。この配列は `STORAGE_BUCKETS` という 1 つの環境変数に
JSON として載せて Worker に届きます。`pw build --target cloudflare-workers` が `config.prod.toml` から
書き出し、どのホストでも手で設定できます。

## presigned URL

大きなアップロードをアプリケーション経由で流すべきではなく、ダウンロードも同じです。`Presign` は
ブラウザが直接使う URL を、メソッドと有効期限を限って返します。

```go
put, err := bucket.Presign(r.Context(), key, storage.PresignOptions{
	Method:      http.MethodPut,
	Expires:     15 * time.Minute,
	ContentType: "image/png", // 署名対象。ブラウザはこれをそのまま送る必要がある
})
get, err := bucket.Presign(r.Context(), key, storage.PresignOptions{})
```

`s3` では endpoint に対する SigV4 のクエリ署名付き URL で、クライアント自身の署名器が作ります。
`r2` では同じ署名器を R2 の S3 API に対して使います。binding には presign がないからです。バケットに
`binding` と並べて `endpoint`、`access_key_id`、`secret_access_key` を与えてください。なければ
`Presign` は足りないものを名指しします。`local` では `/_storage/` 配下の相対パスを返し、アプリケーション
自身がプロセスごとに生成した鍵で検証して配信します。PUT は body を保存し、GET は読み返し、URL は
プロセスか有効期限とともに消えます。この経路は local バケットが設定されているときだけマウントされ、
バケットを外に見せたくないデプロイが取る形でもあります。

## クライアントに直接触る

`s3` バックエンドは [tinygodriver](https://github.com/shibukawa/tinygodriver) の `storage/s3` です。
S3 REST API を自分で話して SigV4 で署名するクライアントで、`aws-sdk-go-v2` と `minio-go` が TinyGo で
コンパイルできないために存在します。TinyGo 専用の部分はありません。インターフェースが運ばない操作、
つまり `GetRange` によるバイト範囲、multipart アップロード、ハッシュするには大きすぎるストリーム向けの
`WithUnsignedPayload` が要るときは、同じ設定からクライアントを組んでインターフェースの先へ行きます。

```go
client, err := s3.New(s3.WithEndpoint(endpoint), s3.WithRegion(region),
	s3.WithCredentials(s3.Credentials{AccessKeyID: id, SecretAccessKey: secret}))
object, err := client.GetRange(ctx, "myapp-uploads", key, 0, 1<<20)
```

インターフェース越しでも知っておく価値のある癖が 1 つあります。SigV4 はペイロードのハッシュに署名する
ので、`Put` は body を 2 回読みます。`io.Seeker` を実装する body（`*bytes.Reader`、`*os.File`）はハッシュ
してから巻き戻され、それ以外は先にメモリへバッファされます。できる限り seek できるものを `Put` に
渡してください。

インターフェース経由のエラーは `storage.ErrNotFound` か、それ以外はバックエンド自身のエラーです。S3 の
センチネル（`s3.ErrAccessDenied`、`s3.ErrBadCredentials`、`s3.ErrNoSuchBucket`）と request ID を持つ
`*s3.Error` には、ログ用に `errors.Is` と `errors.As` で到達できます。`pw.WriteProblem` は知らないエラーを
全文ログ付きの 500 にして `internal error` と報告します。存在しないキー以外のすべてにとって、それが
正しい答えです。

## ローカル開発

`local` バックエンドが既定の答えです。テストが S3 経路そのものを証明しなければならないときは、
S3 互換サーバーならどれでも動き、本番との違いは endpoint の設定だけです。[RustFS](https://rustfs.com/)
はコマンド 1 つで起動します。

```sh
docker run -d --name rustfs -p 9000:9000 \
  -e RUSTFS_ACCESS_KEY=rustfsadmin -e RUSTFS_SECRET_KEY=rustfsadmin \
  -e RUSTFS_VOLUMES=/data rustfs/rustfs
```

## 使わない場面

アプリケーションと一緒に出荷するファイルは public ツリー（埋め込みまたは
[外部](/ja/guides/frontend/static-assets/)）に置くべきで、build がバリデータを計算し、マウントが
キャッシュヘッダー付きで配信します。リクエストごとに読む値は[データキャッシュ](/ja/guides/backend/data-cache/)か
データベースの領分で、リクエストごとのバケット往復はこのインターフェースが隠そうとしない遅い経路です。
multipart アップロードはインターフェースにありません。単発 `Put` の上限を超えるオブジェクトは、上のように
クライアントへ直接届きます。
