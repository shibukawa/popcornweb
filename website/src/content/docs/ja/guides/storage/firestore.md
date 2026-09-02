---
title: Firestore
description: Firestore の Datastore mode を使い、リレーショナルデータベースなしで型付きエンティティ、セッション、認証を保存する。
sidebar:
  order: 3
---

Firestore には、アプリケーションのエンティティだけでなく、フレームワークのセッションや
認証レコードも保存できます。Popcorn Web が使うのは Datastore API です。データベースは
必ず **Datastore mode** で作成してください。Firestore Native mode は別の API であり、
起動時に拒否されます。

Google Cloud 上ですでに Datastore mode の Firestore を運用している場合や、その
名前空間とトランザクションモデルがデータに合う場合に選びます。SQL や DynamoDB の
移植可能な代替ではありません。`.pw.sql` は Firestore に対して実行できず、既存データを
移行する機能もありません。

:::note[はじめる前に]
[`pw init`](/ja/pw/project/init/) で作ったプロジェクトを前提にします。
`pw add firestore` を実行すると、`entities/` パッケージ、クエリのひな形、
`generate.firestore` の設定が追加されます。
:::

## クライアントを開く

クライアントは blank import で有効にします。

```go
package main

import (
	"context"
	"log"

	_ "github.com/shibukawa/popcornweb/database/firestore"
	"github.com/shibukawa/popcornweb/pw"
)

func main() {
	if err := pw.Run(context.Background(), pw.NewServeMux()); err != nil {
		log.Fatal(err)
	}
}
```

ローカル開発では Datastore エミュレータを起動し、接続先を設定します。

```sh
gcloud beta emulators datastore start --host-port=127.0.0.1:8081
```

```toml
[middleware.firestore]
enabled = true
project_id = "demo-popcornweb"
endpoint = "127.0.0.1:8081"
```

このエミュレータは Google Cloud SDK に含まれるため、`pw dev` からは起動しません。
Devbox に追加できる独立したパッケージでもありません。

## エンティティとクエリを宣言する

Go の型に、プロパティとキーをまとめて宣言します。Datastore のキーはプロパティとは
別に保存されるため、下の `ID` が二重に保存されることはありません。

```go
// entities/note.go
package entities

import "time"

type Note struct {
	ID        string    `firestore:"-,name"`
	Author    string    `firestore:"author"`
	Body      string    `firestore:"body,noindex"`
	CreatedAt time.Time `firestore:"created_at"`
	ExpiresAt time.Time `firestore:"expires_at,ttl"`
}
```

```text
// entities/notes.pw.firestore
export statement NotesByAuthor(author: string): firestore.many<Note> {
  where author == {author}
}
```

`pw generate` は宣言内のプロパティを構造体タグと照合し、コーデック、キービルダー、
`NotesByAuthor` 関数をソースの隣に生成します。片方のファイルだけで `author` を
変更すると、空の結果が返り続けるのではなく生成エラーになります。

生成されたクエリはプロセスのクライアントを自分で解決するので、呼び出し側は
context だけで済みます。

```go
for note, err := range entities.NotesByAuthor(r.Context(), accountID) {
	if err != nil {
		return err
	}
	use(note)
}
```

エンティティを直接操作する場合は、まず `firestore.Handle(ctx)` でプロセスのハンドルを
受け取り、そのハンドルの `Store`、`Insert`、`Load`、`Update`、`Remove` メソッドを
呼びます。クライアントはリクエストの context の値ではなくプロセス状態なので、呼び出し
ごとに context を探索するコストはありません。タグ、クエリの戻り値、生成されるシグネチャは
[Firestore クエリフォーマット](/ja/reference/firestore-templates/)にまとめています。

## サービス側のポリシーをデプロイする

マイグレーションやスキーマ適用はありません。kind は最初の書き込みで作られ、
複合インデックスや TTL ポリシーの有無を起動時に検証することもできません。これらは
デプロイ側で管理します。

`ttl` タグは期限に使うプロパティを示すだけで、削除を有効にはしません。Google Cloud の
ツールでポリシーを適用してください。

```sh
gcloud firestore fields ttls update expires_at \
  --collection-group=Note --enable-ttl
```

複合インデックスが必要なクエリはコンパイルできますが、インデックスをデプロイするまで
実行時に `FAILED_PRECONDITION` になります。クエリに `index` 句を添え、生成された定義と
デプロイする定義を揃えてください。

## 本番用の資格情報を選ぶ

Cloud Run、GKE、GCE では、通常はメタデータサーバーを使います。

```toml
[middleware.firestore]
enabled = true
project_id = "my-project"
credentials = "metadata"
timeout = "10s"
max_idle_conns = 16
```

既定の `service_account` は、`credentials_file` または
`GOOGLE_APPLICATION_CREDENTIALS` を読みます。`oauth2` はその鍵をアクセストークンと
交換します。`static` は Go 側でトークンソースを渡す場合に使います。既定のデータベースだけで
なく、名前付きの Datastore-mode データベースも指定できます。`namespace` は、この
プロセスが読み書きするすべてのキーを分離します。

## フレームワークの状態を保存する

セッションと認証の保存先は、アプリケーションのエンティティとは別に選びます。

```go
import (
	_ "github.com/shibukawa/popcornweb/authstate/firestore"
	_ "github.com/shibukawa/popcornweb/authstore/firestore"
	_ "github.com/shibukawa/popcornweb/database/firestore"
	_ "github.com/shibukawa/popcornweb/sessionstore/firestore"
)
```

```toml
[session]
enabled = true
backend = "firestore"

[auth]
enabled = true
backend = "firestore"
mode = "oidc_only"
```

各 kind は最初の書き込みで作られます。期限を過ぎたセッションや認証処理中のレコードは
すぐに無効として扱われますが、保存済みのデータを削除するには `expires_at` への TTL
ポリシーが必要です。失効やコストの違いは
[セッションストレージ](/ja/guides/storage/session-storage/)を参照してください。
