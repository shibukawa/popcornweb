---
title: ハンドラ
description: ルーティングとリクエストバインディング。構造体タグ、バリデーション、JSON・フォーム・multipart のボディ。
sidebar:
  order: 1
---

ハンドラは素の `net/http` ハンドラのままですが、入力を毎回手でパースする必要は
ありません。安定したアプリケーション向け API
`github.com/shibukawa/popcornweb/pw` は、ハンドラのシグネチャを変えずに、
ルーティングとの互換性と型付きリクエストバインディングを加えます。

## コード生成

以下のバインディングは、リクエストのたびにリフレクションで組み立てているわけでは
ありません。`pw generate` がこのパッケージの Go ソースを読み、その中のルート登録と
`pw.Parse`、レスポンス呼び出しを見つけて、バインダー、JSON コーデック、OpenAPI の
断片をソースの隣の `_pw_gen.go` に書き出します。生成されたファイルはビルド出力です。
Git は無視しますし、生成し直せば作り直されます。

走らせ方は 3 つあります。`pw dev` はプロジェクトのソースを監視していて、変わるたびに
生成し直し、リビルドして再起動します。`pw build` はコンパイルの前に生成します。
[`pw generate`](/ja/pw/project/generate/) はその同じ作業をコンパイラの手前で止めた
もので、TinyGo や自分で書いた `go build` がコンパイルを持つ場合に使います。手で 1 回
走らせるときも同じコマンドです。

走査の対象はモジュール全体ではありません。`popcornweb.toml` が目的ごとに
ディレクトリを挙げていて、ハンドラは `handlers` の目的です。

```toml
[generate]
handlers = ["handlers"]
```

挙げたディレクトリは再帰的に歩くので、ネストしたパッケージを個別に書く必要は
ありません。どの目的にも挙がっていないディレクトリのハンドラは、報告されません。
普通の Go コードはプロジェクトのあちこちにあり、生成器にはどれが自分向けかを
判別できないからです。結果としてパッケージはコンパイルが通り、その入力型に対する
バインダーだけが書かれず、`pw.Parse` がリクエスト時にその旨のエラーを返します。
ハンドラの中にバグを探す前に、ここにディレクトリを足してください。目的の一覧は
[`pw generate`](/ja/pw/project/generate/)にあります。

## ルーティング

```go
package handlers

import "github.com/shibukawa/popcornweb/pw"

var mux = pw.NewServeMux()

func Handlers() *pw.ServeMux { return mux }
```

通常の Go ビルドでは `pw.ServeMux` は `net/http` の `ServeMux` **そのもの**です。
ラッパーではなく型エイリアスなので、パターン、ワイルドカード、優先順位は標準
ライブラリのままです。TinyGo にも `ServeMux` 自体はありますが、Go 1.22 で入った
パスパラメータとメソッド指定が 0.41 時点では使えないため、同じセマンティクスを
持つ別実装が使われます。

ハンドラのファイルはそれぞれ `init` で自分を登録します。ルートを追加する作業は、
中央のテーブルを編集することではなくファイルを追加することです。

```go
func init() {
	mux.HandleFunc("GET /", home)
	mux.HandleFunc("GET /users/{id}", showUser)
	mux.HandleFunc("POST /users", createUser)
}
```

アプリケーションの成長に合わせてルートを複数パッケージに分割する方法は
[プロジェクト構成](/ja/guides/architecture/project-structure/)を参照してください。

## リクエストのバインディング

ルーティングは馴染みのある形を保ち、リクエストのパースは生成処理が引き受けます。
`pw.Parse[T]` がリクエストから構造体を埋め、生成器がその呼び出し箇所を読んで
`T` のバインディングコードを事前に書き出します。実行時リフレクションは不要です。

```go
type showUserInput struct {
	ID   int    `path:"id"`
	Sort string `query:"sort" default:"name"`
}

func showUser(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[showUserInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	// ...
}
```

### 取得元のタグ

| タグ | 取得元 |
| --- | --- |
| `input:"name"`（またはタグなし） | クエリ文字列。なければボディ |
| `query:"name"` | クエリ文字列のみ |
| `payload:"name"` | リクエストボディのみ |
| `path:"id"` | パスのワイルドカード（`/users/{id}` など） |
| `header:"Authorization"` | リクエストヘッダ |
| `cookie:"session"` | リクエストクッキー |
| `method:"method"` | HTTP メソッド |

ワイヤ名を明示しない場合、フィールド名は lowerCamelCase になります。`DisplayName` は
`displayName` です。

`input` は意図的に寛容です。クエリパラメータが優先され、クエリに値がないときだけ
ボディを読みます。両方を受け付けるとエンドポイントが曖昧になる場合は、`query` か
`payload` を使って取得元を 1 つに限定してください。

### リクエストボディ

同じリクエスト構造体が 3 つのボディ形式を受け付けるため、ワイヤ形式はクライアントが
選べます。

- `application/json`
- `application/x-www-form-urlencoded`
- `multipart/form-data`

したがって、通常の HTML フォーム送信と JSON API 呼び出しで同じハンドラを共有できます。

```go
type createUserInput struct {
	Name  string `payload:"name" check:"required,maxlen=40"`
	Email string `payload:"email" check:"required,email"`
}
```

### ファイルアップロード

multipart のファイルフィールドには `httpbind.File` を使います。

```go
import httpbind "github.com/shibukawa/tinybind-go/httpbind"

type uploadInput struct {
	Title string        `payload:"title" check:"required"`
	Image httpbind.File `payload:"image" check:"required"`
}
```

`File` は `Filename`、`ContentType`、`Size`、`Content` を持ちます。multipart ボディの
上限は既定で 1 MiB で、`httpbind.SetMaxMultipartBodyBytes` で変更します。ただし
フレームワーク側の `server.max_request_body` が先に適用されるため、その既定値
10 MiB を超える multipart 上限は、そちらを動かすまで効きません。
[アプリケーション設定](/ja/guides/architecture/configuration/)を参照。

### 宣言していないフィールド

`payload:"*"` は宣言していないフィールドをまとめて受け取ります。

```go
type eventInput struct {
	Type   string         `payload:"type"`
	Extras map[string]any `payload:"*"`
}
```

デコード済みの値ではなく生の JSON を保持したい場合は `map[string]json.RawMessage`
を使います。

## バリデーション

`check` タグが制約を宣言します。制約は生成時にコンパイルされ、リクエストごとに解釈
されるわけではありません。

| ルール | 対象 | 例 |
| --- | --- | --- |
| `required` | すべて | 空文字列、欠損値、空ファイルを拒否 |
| `min` / `max` | 数値 | `check:"min=1,max=100"` |
| `minlen` / `maxlen` / `len` | 文字列 | `check:"maxlen=40"` |
| `pattern=...` | 文字列 | 正規表現 |
| `email` | 文字列 | RFC 形式 |
| `uuid` | 文字列 | UUID 形式 |
| `date` | 文字列 | `YYYY-MM-DD` |
| `time` | 文字列 | `HH:MM:SS` |
| `datetime` | 文字列 | RFC 3339 |

複数のルールはカンマで区切ります。`pattern` にカンマが含まれる場合は最後に置いて
ください。

### デフォルト値と列挙

次の 2 つの制約は `check` のルールではなく、独立したタグです。

| タグ | 対象 | 例 |
| --- | --- | --- |
| `default:"value"` | スカラー | 値がないときに適用 |
| `enum:"a,b,c"` | スカラー | `enum:"asc,desc"` |

`default` は設定用の構造体がすでに使っているタグなので、フレームワークの両側で同じ
意味になります。`enum` が独立したタグになったのは別の理由です。`check` の中では
カンマがすでにルールの区切りなので、値の区切りには使えませんでした。各値の前後の
空白はトリムされます。

```go
type listInput struct {
	Page int    `query:"page" check:"min=1" default:"1"`
	Sort string `query:"sort" enum:"asc,desc" default:"asc"`
}
```

どちらかを `check` の中に書くとエラーになり、代わりに使うタグがメッセージに出ます。

```
check: enum is not a check rule; use the struct tag enum:"asc,desc" instead
```

カンマを区切りにした代償として、enum の値にカンマは含められません。そのような値が必要な
集合には、タグではなく検証を持つ型が向いています。

チェックに失敗すると `pw.Parse` は該当フィールドの情報を持つエラーを返します。それを
`pw.WriteProblem` に渡すと、フィールド単位の詳細を含む 400 になります。
[レスポンス](/ja/guides/frontend/responses/)を参照。

## リクエストスコープのアクセサ

| 呼び出し | 戻り値 |
| --- | --- |
| `pw.Logger(r)` | リクエストスコープの `*slog.Logger` |
| `pw.Config[T](r)` | 登録済みの設定構造体 |
| `pw.DB(r)` | `(*sql.DB, bool)` — pgx ネイティブプールで動く PostgreSQL では `false` |
| `pw.Transaction(r, fn)` | `fn` をトランザクション内で実行 |

どれもリクエストを取ります。ハンドラが手に持っているのがリクエストだからです。
ハンドラより下の層 — サービス関数、ジョブ、リクエストなしで呼べるもの — では、同じ
アクセサが `Context` サフィックス付きで `context.Context` を取ります。`database/sql`
が `ExecContext` と綴るのと同じ規則で、`pw.LoggerContext(ctx)`、
`pw.ConfigContext[T](ctx)`、`pw.DBContext(ctx)`。その `ctx` をハンドラ側で作るのが
`pw.Context(r)` です。

```go
func createUser(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[createUserInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	err = pw.Transaction(r, func(ctx context.Context) error {
		if _, err := queries.InsertUser(ctx, input.Name, input.Email); err != nil {
			return err
		}
		return queries.RecordAudit(ctx, "user.created")
	})
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}
```

コールバックは、どちらのクエリにもトランザクションハンドルを渡していません。
生成済みクエリ関数が context からトランザクションを取得します。詳しくは
[クエリ](/ja/guides/storage/queries/)を参照してください。

## ライフサイクル

```go
func main() {
	if err := pw.Run(context.Background(), handlers.Handlers()); err != nil {
		log.Fatal(err)
	}
}
```

素のハンドラは、動作するサービスの一部にすぎません。最初のリクエストが届く前に、
`pw.Run` は設定をパースし、`--generate-config` などのアプリケーションフラグを処理し、
設定されたランタイムを検証し、データベースプールを初期化し、自分のルートと運用
エンドポイントの衝突を確認し、ミドルウェアスタックを構築します。そのうえで配信を
始めます。`SIGINT` または `SIGTERM` を受けるとグレースフルにシャットダウンし、
登録済みリソースを逆順にクローズします。

サーバー自体は自分で持ちたい場合 —— 別のリスナーの背後や、テストの中など ——
`pw.Middlewares(handler, options...)` が同じ初期化を行い、同じスタックを素の
`http.Handler` として返します。

`pw.WithPublicFS(fsys)` は埋め込みの public ツリーを明示的に渡します。スキャフォールド
されたプロジェクトでは代わりに `public.go` が登録します。

上に挙げたタグには、よくある使い方より先がまだあります。各ルールがどのフィールド種別を
受け付けるか、`input` が種別ごとにどう解決するか、rest マップが何を除外するか、何が
OpenAPI に載るか——それは[リクエストバインディング定義](/ja/reference/request-binding/)です。
