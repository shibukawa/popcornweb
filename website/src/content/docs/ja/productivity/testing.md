---
title: テスト
description: testutil で、隔離された設定コピーから実際のアプリケーションを起動する。
sidebar:
  order: 1
---

高速なテストも、デプロイするアプリケーションを実際に検証してこそ役立ちます。
`github.com/shibukawa/popcornweb/testutil` は、本物のルート、ミドルウェアスタック、
設定バインディングを、登録済み設定の隔離されたコピーに対して起動します。テストは
手で組み立てた近似物を呼ぶのではなく、そのアプリケーションへ HTTP で到達します。

## 最初のテスト

```go
func TestHome(t *testing.T) {
	server := testutil.TestRun(t, Handlers(), func(config *testutil.Config) {
		config.Update(func(middleware *pw.MiddlewareConfig) {
			middleware.RDB = pw.RDBConfig{
				Enabled: true,
				Connections: []pw.RDBConnectionConfig{{
					DSN:            "sqlite://:memory:",
					ConnectTimeout: time.Second,
					MaxOpenConns:   1,
					MaxIdleConns:   1,
				}},
			}
		})
	}, testutil.WithMigrations("../migrations"))

	response, err := server.Client().Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
```

`TestRun` はまず登録済みのすべての設定をコピーし、コピー側のポートを `-1` にします。
次にカスタマイザを適用し、利用可能なループバックポートを確保し、コピーしたランタイム
リソースを初期化してサーバーを起動します。クリーンアップは `t` に登録されるため、
テスト側で defer するものはありません。

この順序から 1 つの制約が生まれます。カスタマイザがポートを読むと、その時点では
`-1` が見えます。実際のポートは後で決まるため、起動後に `server.URL` か
`server.Port` を使ってください。

## 生成パッケージの登録

本物のサーバーを使う以上、生成された登録処理も本物でなければなりません。ドキュメント
シェルや設定定義はパッケージの `init` で登録されるため、そのパッケージをテスト
バイナリにリンクする必要があります。

```go
import (
	_ "myapp"            // public.go
	_ "myapp/templates"  // ドキュメントシェル
)
```

これがないと、ドキュメントが登録されていない状態でサーバーが起動し、HTML の描画が起動時
に失敗します。

## 設定のカスタマイズ

| 呼び出し | 用途 |
| --- | --- |
| `config.Get[T]()` | コピーされた設定構造体を読む |
| `config.Set(value)` | まるごと置き換える |
| `config.Update(fn)` | その場で編集する |

いずれも設定の型に対してジェネリックなメソッドなので、フレームワークの設定も
アプリケーションの設定も同じ型付きの方法で扱えます。`Set` と `Update` は引数から型を
推論し、推論元のない `Get` だけ型を書きます。

```go
config.Update(func(app *AppConfig) {
	app.EnvLabel = "test"
})
```

## オプション

### `WithMigrations` / `WithMigrationsFS`

```go
testutil.WithMigrations("../migrations")
```

サーバーの起動前にマイグレーションを適用します。`WithMigrationsFS` は代わりに
`fs.FS` を取るので、埋め込みマイグレーションに使えます。

スキーマの届き方は DSN のエンジンによって変わります。

- **SQLite** はキャッシュしたスナップショットをコピー先のデータベースへ再生します。
  `sqlite://:memory:` が動くのはこのためです。プロセス内のデータベースには DSN で
  到達できないので、接続文字列ではなく SQL を渡しています。
- **PostgreSQL と MySQL** は設定されたデータベースへ直接適用します。同じデータベースに
  対する 2 回目の `TestRun` は何も適用せずスキーマを再利用するので、パッケージ内の
  テストが 1 つの準備済みサーバーを共有できます。

サーバーの DSN は、テストスイート専用のデータベースに向けてください。適用済み
バージョンは番号で記録されるため、他のプロジェクトのバージョン 1 をすでに持つ
データベースでは、最初のマイグレーションが適用済みに見えてスキーマが届きません。

### `WithSeed` / `WithSeedDir`

```go
testutil.WithSeed("initial")
```

スキーマの適用後、サーバーの起動前にデータセットを読み込みます。名前はシード
ディレクトリ —— 既定は `testdata/seed`、`WithSeedDir` で変更可能 —— からの相対で、
`.yaml` 拡張子は省略できます。データセットは指定した順に適用されます。

データセットのファイルは、テーブル名に行を対応させた形です。

```yaml
member:
- { id: 1, name: Frank }
- { id: 2, name: Grace }
```

同じファイルを `pw seed` も使います。CLI とテストスイートが別々のフィクスチャを持って
ずれる代わりに、1 つの形式を共有します。同じファイルを期待状態としても使う方法は
[フィクスチャ](#フィクスチャ)、形式そのものは
[シードデータ](/ja/productivity/seed-data/)を参照してください。

### `WithTransaction`

```go
testutil.WithTransaction(true)
```

テストサーバーのすべてのリクエストを 1 つのトランザクションの中で実行し、テスト終了時に
ロールバックします。1 つのデータベースを共有するテストどうしが独立を保ち、並列実行も
できます。アプリケーション自身が開始するトランザクションはセーブポイントとしてその中に
ネストするため、セーブポイント対応のドライバが必要です。

これはどのエンジンでも同じに動きます。PostgreSQL の pgx ネイティブ経路も例外ではなく、
テストトランザクションは接続が持っているプールの種類のまま開かれ、シードもアサーションも
その中で走ります。

### `WithIdentityProvider`

```go
server := testutil.TestRun(t, handlers.Handlers(), nil, testutil.WithIdentityProvider(
	testutil.WithIdPConfig("../devidp.toml"),
	testutil.WithLoginUser("admin"),
	testutil.WithIdPBinding(func(config *testutil.Config, idp testutil.IdPInfo) {
		config.Update(func(auth *handlers.AuthConfig) {
			auth.Issuer, auth.ClientID, auth.ClientSecret = idp.Issuer, idp.ClientID, idp.ClientSecret
		})
	}),
))
```

[`pw dev`](/ja/pw/project/dev/) と同じ開発用認証プロバイダを、専用のループバック
ポートでアプリケーションサーバーより先に起動します。`WithLoginUser` でログインする
ユーザーを事前に指定すると、認可エンドポイントは即座に認可コードを付けてリダイレクト
するので、ブラウザを操作せずにログインが完結します。

```go
response, err := server.Client().Get(server.URL + "/login")
```

ユーザー定義は `WithIdPConfig`（`devidp.toml` ファイル）、`WithIdPRoster`（同じ
TOML をテスト内に持つ）、`WithIdPUsers`（Go の値）のいずれか 1 つだけを指定します。
`WithIdPBinding` は issuer と生成されたクライアント資格情報をコピー済みの設定へ
書き込みます。`customize` の後に走るので、置いておいたプレースホルダより優先されます。
テストの途中でユーザーを切り替えるには `server.LoginAs(t, "guest")`、クライアントを
自前で組み立てる場合は `server.IdPInfo()` から同じ値を取得できます。

## フィクスチャ

データセットは、テストがそれを既知の状態として扱った瞬間から**フィクスチャ**に
なります。同じファイルが両端で働きます。リクエストの前には開始状態として読み込まれ、
その後はデータベースと突き合わせる期待状態になる。その後ろ半分が `server.AssertDB`
です。

```go
func TestArchiveRemovesTheMember(t *testing.T) {
	server := testutil.TestRun(t, Handlers(), nil,
		testutil.WithMigrations("../migrations"),
		testutil.WithSeed("initial"),
	)

	response, err := server.Client().Post(server.URL+"/members/2/archive", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	server.AssertDB(t, "after_archive")
}
```

期待値を `SELECT` の羅列ではなくファイルとして書くと、テーブル全体がテストの対象に
入ります。目的のメンバーを正しくアーカイブし、ついでに別人の行まで消してしまう
ハンドラは、ここで落ちます。`after_archive.yaml` はその行が変わってよいとは
言っていないからです。そしてこれは、メンバー 2 だけを `SELECT` するテストが素通り
させるバグでもあります。

不一致は `Fatalf` ではなく `Errorf` で報告されるので、テストはそのまま進みます。
1 回の実行で、最初の 1 テーブルだけでなく、ずれたテーブルがすべて出ます。

```
Popcorn Web database does not match:
after_archive.yaml:
Table: member
- Expected
+ Actual
id: 1
  name: Frank
id: 2
- name: Grace
```

### テーブルの一部だけを比べる

既定では、テーブルはデータセットと完全に一致していなければなりません。ファイルが
書いていない行がデータベースにあれば失敗です。列はその限りではなく、ファイルが省いた
列は無視されます。連番の `id` や `created_at` を期待値から外しておけるのはこのため
です。

余分な「行」のほうが正当な場合は、テーブルごとに戦略を指定します。

```yaml
_match:
  member: exact          # 既定
  access_log_2026_*: sub # 余分な行は許す。並べた行は存在しなければならない

member:
- { id: 1, name: Frank }
```

既定の `exact` を使ってください。`sub` は、テストが制御していないところから行が来る
テーブル —— 監査証跡、追記専用のログ、同じパッケージの別のテストも書き込むテーブル
—— に限ります。普通のテーブルに `sub` を付けると、予期しない行を静かに捕まえなく
なります。テーブルごと比べる理由は、まさにそこにあったはずです。

事前に書き下せない列 —— 生成されたタイムスタンプ、識別子が埋め込まれたメッセージ ——
は、値の代わりにマッチャで書きます。`[notnull]`、`[currentdate, 2m]`、`[regexp, …]` など
一式は[データセットの書式](/ja/productivity/seed-data/#期待値としてしか意味を持たない値)に
あります。これがなければ、その列のアサーションはファイルを出て Go 側へ移るしか
ありませんでした。

### テストの途中で入れ直す

`server.Seed` は動いているサーバーにデータセットを適用します。1 つのテストのフェーズ
とフェーズの間で状態を戻すのに使えます。

```go
server.Seed(t, "initial")
```

既定では、ファイルに並んだテーブルは truncate されてから挿入し直されるので、前の
フェーズが何をしていようと、データセットはそのテーブルを記述どおりの状態へ戻します。
ファイルに出てこないテーブルには触れません。

`WithTransaction` の下では、どちらのヘルパーもテストトランザクションの中で動きます。
2 つを組み合わせられるのはそのためです。`AssertDB` はリクエストがまだコミットして
いない書き込みを見ますし、`Seed` が入れた行はロールバックとともに消えます。付けない
場合、`AssertDB` が見るのはコミット済みの状態だけで、トランザクションを開いたままの
リクエストはまだ比較されていません。

### 置き換えるのではなく、足す

truncate してから挿入するのは既定であって、唯一の選択肢ではありません。`_operation` が
テーブルごとに `insert`、`upsert`、`truncate`、`delete` を選びます。5 つの綴りは
[データセットの書式](/ja/productivity/seed-data/#_operation-テーブルに最初に何をするか)に
あります。

このうち 2 つは、他の何より先にテストが向き合う制約を持っています。
`upsert` と `delete` はテーブルの主キーを必要とし、その問い合わせはシードを実行して
いるトランザクションではなくプールに向かいます。接続数が 1 のプールはその時点ですでに
空なので、シードはそこで止まって待ち続けます。この 2 つを使うのは、問い合わせをテスト
トランザクション側に載せる `WithTransaction` の下か、2 本目の接続を開けるプールを持つ
データベースに対してだけにしてください。後者は `sqlite://:memory:` を除外します。
2 本目の接続は 2 つ目の空のデータベースになってしまうからです。`insert`、`truncate`、
そして既定の操作は主キーを必要とせず、影響を受けません。

### 行のタグは解析されるが、絞り込まない

行には `_tag` のリストを書けますし、dbtestify の CLI はそれで絞り込みます。Popcorn Web
は include も exclude もフィルタを公開していないため、タグが何と書いてあろうとファイル内
のすべての行が適用されます。部分集合が必要なら、ファイルを分けてください。

## データベースに対するアサーション

フィクスチャが比べるのはテーブル全体です。確認したいものがそれより狭いとき —— 1 つの
カウンタ、1 つの列、Go で計算したい値 —— は直接読んでください。HTTP アサーションは
クライアントが見た結果を示しますが、データベースのアサーションにはその結果を生んだ
ランタイム状態が必要になることがあります。`server.Context()` は、
`WithTransaction` のトランザクションを含む、リクエストと同じリソースを保持します。
そのため生成済みクエリから、ハンドラと同じトランザクション内のデータを準備・検証できます。

```go
counter, err := queries.CurrentAccess(server.Context())
```

生の SQL が必要な場合は `server.DB` がプールを直接公開しています。

## テストにブラウザが必要になったら

このページのすべては、Go の `http.Client` からアプリケーションを観測します。見えるのは
レスポンスであって、ブラウザがそれをどう扱うかではありません。送信するダイアログ、
ページに差し込まれるフラグメント、ユーザーが実際にクリックして通るログイン —— それらには
本物のブラウザが必要で、そこでも同じシードデータセットが働きます。Playwright で
アプリケーションを操作し、ブラウザテストからデータベースを入れ直す方法は
[E2E テスト](/ja/productivity/e2e-testing/)を参照してください。
