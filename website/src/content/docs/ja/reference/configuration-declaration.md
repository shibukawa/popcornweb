---
title: アプリケーション設定定義
description: 設定構造体の書き方、タグとフィールド型の一覧、各フィールドに対応する3種類の名前、設定値を解決する順序。
sidebar:
  order: 6
---

設定構造体を1回宣言すると、`default` タグ、TOML ファイル、環境変数、
コマンドラインオプションという4つの入力元を利用できます。`pw generate` が登録の呼び出しを読んでバインディング
のコードを書くので、実行時のリフレクションはありません。設定が TinyGo でも使えるのはそれが
理由で、以下の規則のいくつかがリフレクション方式のバインダより厳しいのも同じ理由です。

ここでは設定構造体の宣言方法を説明します。フレームワーク自身のキーと既定値は
[アプリケーション設定一覧](/ja/reference/configuration/)に、例を追った説明は
[アプリケーション設定](/ja/guides/architecture/configuration/)にあります。

## 構造体を登録する

```go
type AppConfig struct {
	EnvLabel string `default:"local" help:"ページのバッジに出す環境名"`
}

func RegisterConfig() { pw.RegisterConfig[AppConfig]("app") }
```

| 規則 | 内容 |
| --- | --- |
| プレフィックスは文字列リテラル | 生成が静的に読む。計算した値ではバインディングが生成されない |
| 呼び出しは `generate.config` が挙げるディレクトリに置く | そうでなければ何も生成されない |
| 登録は全 `init` の後、パースの前 | 生成された定義は `init` で登録される。`ParseConfig` の後の登録は panic |
| プレフィックスは1つの名前空間を共有する | 領域ごとに別名を——`app`, `billing`, `search` |
| プレフィックスにはドットを含められる | `pw.RegisterConfig[CacheConfig]("middleware.cache")` |

読み出しにエラー処理は要りません。

```go
app := pw.Config[AppConfig](r)
```

`pw.Config` はリクエストの外では `nil` を受け付けます。登録済みだが一度もパースされていない
プレフィックスは宣言された既定値を、未登録の型はゼロ値を返します。設定を読むハンドラは
すでにレスポンスの経路にいて、そこで nil を検査しても、同じ欠落を後の行へ先送りするだけ
だからです。

## フィールドの型

| 使える型 | |
| --- | --- |
| `string` | |
| `bool` | |
| `int` | |
| `time.Duration` | どのソースでも Go の duration 記法 |
| `[]string` | TOML の配列、繰り返した CLI オプション、カンマ区切りの環境変数 |
| それらを含む名前付きのネスト構造体 | ネストした TOML テーブルになる |
| 同じパッケージの名前付き構造体 `T` の `[]T` | テーブル配列から埋まる |

float、map、ポインタ、それ以外の要素型のスライスはバインドできません。使える表現で受け取り、
パース後に変換してください。`float64` を直接宣言すると
`unsupported basic type float64` という生成エラーになります。

### duration

```go
type MailerConfig struct {
	SendTimeout time.Duration `default:"5s" help:"送信のタイムアウト"`
}
```

```toml
[app.mailer]
send_timeout = "1h30m"
```

裸の数値はどのソースでも拒否されます。`5` では秒なのかナノ秒なのか言えないからです。これは
`default` タグにも当てはまり、解析できない値は起動時ではなく `pw generate` を失敗させます。
ひな形は duration を引用符付きの文字列として出力し、`default` の無いフィールドは `"0s"` から
始まります。

この扱いを受けるのは `time.Duration` そのものだけです。基底型が `time.Duration` の自前の
名前付き型は整数としてバインドされます。

### 繰り返す設定

```go
type AppConfig struct {
	Routes []RouteConfig `help:"静的ルート"`
}

type RouteConfig struct {
	Path    string
	Dir     string
	Listing bool `default:"false"`
}
```

```toml
[[app.routes]]
path = "/"
dir = "./public"

[[app.routes]]
path = "/files"
dir = "./files"
listing = true
```

要素の個数はデータなので、要素には **CLI オプションも環境変数もありません**。ファイルだけが
ソースです。`default` は要素ごとに1回、引き続き適用されます。規則は次のとおりです。

- 要素の構造体は同じパッケージの名前付き構造体で、値として保持します。`[]*RouteConfig` と、
  自分自身へ到達する構造体はどちらも拒否されます。
- 要素のフィールドへの `opt` と `env` は、黙って何もしないタグではなく生成エラーです。
  `falsy`・`dependon`・`secret` も同じです。どれも安定した設定キーを必要としますが、要素の
  キーは設定ではなく1つの要素に属するからです。
- サブコマンドは構造体のスライスをそもそも取れません。
- 要素に資格情報やマシン固有のパスを入れるには、その値に `${NAME}` の参照を書きます。
- ひな形はスライス1つにつき `[[…]]` ブロックの例を1つ出力します。

## タグ

キーに名前を与えるタグが5つあります。

| タグ | 効果 |
| --- | --- |
| `default:"value"` | どのソースも与えないときの値 |
| `key:"name"` | 安定した TOML／設定キー名を上書きする |
| `opt:"long"` / `opt:"long,s"` | CLI のロングオプションを上書きし、必要なら1文字のショート形も付ける |
| `env:"NAME"` / `env:"-"` | 環境変数の正確な名前、または環境変数からの入力を止める |
| `help:"text"` | usage とひな形に出る説明 |

キーに名前ではなく性質を与えるタグが3つあります。

| タグ | 効果 |
| --- | --- |
| `secret:"mask"` / `"hide"` / `"show"` | 起動サマリでの値の見え方 |
| `dependon:"key"` / `dependon:".sibling"` | このキーが従うキー。先頭のドットは囲む構造体からの相対 |
| `falsy:"value"` | このキーに依存するものにとって「未設定」にあたる値 |

`dependon` と `secret` はネストした構造体のフィールドにも置け、そこでは部分木全体を覆います。
`falsy` は置けません。値を1つ名指すタグであり、構造体は値を持たないからです。

### help のもとになる godoc

`help` タグの無いフィールドは doc コメントから説明を取り、生成器がそのテキストを構造体タグへ
書き戻します。

```go
type MailerConfig struct {
	// FromAddress is the envelope sender.
	FromAddress string `default:"noreply@example.com"`
}
```

1回走らせるとソースは
`` `default:"noreply@example.com" help:"FromAddress is the envelope sender"` `` になります。
以後はタグが唯一の正となり、既存の `help` が常に勝ち、再実行しても何も変わりません。使われる
のは最初の段落だけで、`//go:` と lint のディレクティブは落とされ、末尾のピリオド1つは
削られます。行末コメントも使えます。同じテキストが生成される CLI の usage に流れ、help
文字列を空で登録したサブコマンドは構造体の godoc に落ちます。

## 1つのフィールドが答える3つの名前

フィールド名はスネークケースになり、登録したプレフィックスの下に入れ子になります。
プレフィックスが `app` なら次のとおりです。

```go
type AppConfig struct {
	Mailer MailerConfig
}

type MailerConfig struct {
	FromAddress string
}
```

| 面 | 名前 |
| --- | --- |
| 安定した設定キー | `app.mailer.from_address` |
| TOML | `[app.mailer]` の `from_address = …` |
| CLI | `--app-mailer-from_address` |
| 環境変数 | `APP_MAILER_FROM_ADDRESS` |

安定キーの `.` を `-` に置き換えて `--` を付ければオプションです。そのオプションから
ダッシュを外して大文字にすれば環境変数です。キーの中のアンダースコアは残り、変わるのは
階層を区切るドットだけです。

`opt` は両方を動かします。環境変数の名前がキーではなくロングオプションから導かれるからです。

```go
Port int `key:"listen_port" default:"8080" opt:"port,p" help:"HTTP listen port"`
```

| 面 | 名前 |
| --- | --- |
| 安定キー | `app.listen_port` |
| TOML | `[app] listen_port = 8080` |
| CLI | `--port 8080` または `-p 8080` |
| 環境変数 | `PORT=8080` |

`opt` があると、導かれるはずの `--app-listen_port` は登録されません。`env` が動かすのは環境
変数の名前だけで、値はそのまま使われます。先頭は英字か `_` である必要があり、1つの名前を
2つのフィールドに割り当てると生成エラーです。`observability.service_name` が自分の TOML キーと
オプションを保ったまま `OTEL_SERVICE_NAME` に答えるのはこの仕組みです。

## 値がどこから来るか

```
default  <  TOML ファイル  <  環境変数  <  コマンドラインオプション
```

優先順位は固定で、設定できません。あるレイヤにキーが無くても、下のレイヤが与えた値は消え
ません。キーがあれば必ず上書きします。

環境を選ぶのは `APP_ENV` で、それがプロジェクト内のファイル名を決めます。`dev`、`stg`、
`prod`、あるいは小文字・数字・`-`・`_` からなる任意のトークンを受け付けます。不正なトークンは
`ParseConfig` を失敗させ、未設定か空なら `dev` です。Popcorn Web は次のうち最初に読めた
ファイルを読みます。

1. `./config.{APP_ENV}.toml`
2. `./config/config.{APP_ENV}.toml`
3. ユーザー設定ディレクトリ。ここでは環境に依存しない `config.toml`
4. システム設定ディレクトリ。同じく

プロジェクトツリーの素の `config.toml` は決して読まれません。この非対称は意図したものです。
すべての環境に適用されるファイルは、1人の作業マシンでは妥当でも、リポジトリの中では
「これはどの環境向けなのか」に答えが無くなり、誤解を招きます。

**ファイルはマージされません。** 最初に読めた候補だけが読まれるので、ローカルのファイルは
システムのファイルと混ざるのではなく置き換えます。`--config-path` は探索そのものを置き換え、
フォールバックしません。存在しない・読めない・ディレクトリであるパスを渡すと読み込みが
失敗します。

```sh
./myapp --config-path ./deploy/staging.toml
```

待ち受けるかどうかを決める前に設定が必要なバイナリ——CLI サブコマンド、マイグレーションの
実行、単発のジョブ——では、`pw.SetConfigLoadOptions` が `ParseConfig` の前に探索を調整します。
パースの後に呼ぶと panic します。

### TOML のサブセット

設定が読むのは TOML 仕様の全体ではなく、制限されたサブセットです。

| 受け付ける | 受け付けない |
| --- | --- |
| テーブル、ネストしたテーブル、素のドット付きキー | 引用符付きのキー |
| string、bool、整数、浮動小数のスカラ | インラインテーブル |
| プリミティブなスカラの配列 | 入れ子の配列 |
| テーブル配列 | |
| コメント | |

ここには限界が2つあります。パーサが受け付けるものと、フィールドが受け取れるものです。後者の
ほうが狭く、TOML の浮動小数は解析できてもフィールドにはバインドできません。

`[[…]]` ヘッダの後のキーはすべてその要素に属するので、囲むテーブル自身のキーは最初の要素より
前に置く必要があります。

打ち間違いについては TOML が非対称な側です。未知のキーは解析され、どのフィールドにも一致
せず、黙って適用されません。綴りを間違えた CLI オプションは大きな音を立てて失敗します。

### ファイルから環境変数を参照する

TOML の**文字列**の中の `${NAME}` は、読み込み時に環境から展開されます。参照が値全体を覆う
必要はありません。

```toml
[[middleware.rdb.connections]]
group = "primary"
dsn = "postgres://app:${PRIMARY_DB_PASSWORD}@db1.internal:5432/app"
```

これが主に存在するのは、自分のオプションも変数も持たないテーブル配列の要素へ資格情報を
入れるためです。

- 展開されるのは文字列だけです。キー、テーブルヘッダ、数値、真偽値は展開されません。配列の
  要素と `[[…]]` 要素のフィールドは展開されます。
- 未定義の名前は**読み込みを失敗させます**。ファイルのレイヤは default より上なので、空文字列
  へ展開すると `default` を黙って消してしまいます。起動時に落ちるほうが気づきやすい。空文字列
  が設定された変数は「定義済み」として `""` に展開されます。
- `$$` はリテラルの `$` 1つになります。`{` でも `$` でもないものが続く `$` はそのままです。
- 展開された値もファイルのレイヤに属するので、環境変数と CLI の優先順位は変わりません。
- 環境変数や CLI の値の中に書かれた `${…}` はそのままです。
- 参照が名指すのは生の環境変数です。フィールドごとの `env` 名や `env:"-"` は影響しません。
- `${NAME:-default}` のようなフォールバック形はありません。

### コマンドラインの書き方

```sh
./myapp --app-mailer-from_address noreply@example.com
./myapp --app-mailer-from_address=noreply@example.com
./myapp --app-tls-enabled              # 値の無い bool は true
./myapp --app-tls-enabled=false
./myapp --app-origins a.example --app-origins b.example   # []string は累積する
```

未知のオプション、値の欠落、不正な真偽値は、いずれも読み込みを失敗させます。

## ひな形

登録済みのプレフィックスはすべて自分自身を出力できます。`default` の値が埋まり、`help` の
テキストがコメントになります。

```sh
./myapp --generate-config toml > config.dev.toml
./myapp --generate-config env > .env.example
```

ひな形はそのビルドに存在する登録から組み立てられるので、そのバイナリにとっての正式なキー
一覧になります。自分のプレフィックスを含み、一度も import しなかったフレームワークの機能は
含みません。`[prefix]` テーブルの中のキーは構造体の宣言順に並び、テーブル自体はプレフィックスと
型名の順に並ぶので、出力がパッケージの初期化順に左右されることはありません。`.env` のひな形は
テーブルという括りが無く宣言順を掛ける先が無いので、代わりに変数名でソートされ、`opt`・
`env:"NAME"`・`env:"-"` を尊重します。

構造体の godoc は TOML テーブルのコメントになります。どちらの形も書き終えると終了し、サーバーは
起動しません。

パースが読むのは、作業ディレクトリの `.env`・`.env.local`・`.env.{APP_ENV}`・
`.env.{APP_ENV}.local` の上にプロセスの環境変数を重ねたものです。ひな形を `.env.dev.local`
という名前で置けば次の起動で読まれます。出力先はコミット
されるテンプレートの `.env.example` にしてください。そうしないと、ひな形に並ぶ既定値がすべて
環境変数として扱われます。[秘密情報と dotenv ファイル](/ja/guides/architecture/configuration/#秘密情報と-dotenv-ファイル)を参照してください。

## CLI 専用のサブコマンド

```go
type MigrateOptions struct {
	Path   string   `arg:"required" help:"マイグレーションのディレクトリ"`
	Label  string   `arg:"optional" help:"マイグレーションのラベル"`
	DryRun bool     `default:"false" help:"適用せず変更を表示する"`
	Extra  []string `arg:"*" help:"追加の入力"`
}

func init() { pw.RegisterSubCommand[MigrateOptions]("migrate", "run database migrations") }
```

```go
options, ok := pw.Command[MigrateOptions]()
```

サブコマンドの構造体は **TOML も環境変数も読みません**。フィールドはコマンドラインだけから
来ます。

| タグ | 意味 |
| --- | --- |
| *(無し)* | オプション。名前の付き方は設定のフィールドと同じ |
| `arg:"required"` | 必須の位置引数 |
| `arg:"optional"` | 省略できる位置引数 |
| `arg:"*"` | 残りの位置引数 |

```sh
./myapp migrate ./migrations
./myapp migrate ./migrations --dry_run release extra-a extra-b
```

オプションは位置引数の前でも後ろでも書けます。`pw.Command` が値を返すのは、選択された
サブコマンドだけです。必須引数の欠落、未知のコマンドやオプション、`--help` は、いずれも
生成された usage を伴ってパースを失敗させます。`pw.SubCommand` は `RegisterSubCommand` の
非推奨の別名として残っています。

[カスタムコマンド](/ja/guides/architecture/custom-commands/)を参照してください。

## 起動サマリに出るもの

解決済みの設定は起動時に1度、値が勝ったソースとともに報告されます。端末なら設定の木、
コンテナやパイプなら同じ内容の構造化ログです。`secret` が開示を制御し、`dependon` が無効な
枝を除き、`falsy` が boolean 以外の「オフ」をそのフィルタに教えます。

### `secret`：起動ログにどこまで見せるか

設定サマリはログコレクタまで届くことがあります。有効な設定を表示しただけで資格情報まで
記録されてはいけません。名前から自動判定できない秘密を隠す場合、エントリ自体を落とす場合、
または自動判定の誤検知を戻す場合に `secret` を使います。

```go
type DeliveryConfig struct {
	Password        string
	SigningMaterial string `secret:"mask"`
	TokenBucketSize int    `default:"128" secret:"show"`
	InternalNote    string `secret:"hide"`
}
```

`delivery` というプレフィックスで登録すると、起動時に見える部分は次のようになります。

```text
delivery
├─ password           *****
├─ signing_material   *****
└─ token_bucket_size  128
```

`password` は自動でマスクされます。`signing_material` は自動判定の対象外なので、
`secret:"mask"` でポリシーを補います。`token_bucket_size` は名前に `token` を含みますが
秘密ではないため、`secret:"show"` で保守的な判定を戻しています。`internal_note` は `hide` が
エントリごと落とすので表示されません。`mask` ならキーと取得元は残り、空でない値だけが
`*****` に置き換わります。

自動判定の正確な規則は、**安定した設定キーパス全体を小文字化したうえでの部分文字列一致**です。
`secret` タグの無いフィールドは、パスに次のいずれかを含むとマスクされます。

```text
password  secret  apikey  api_key  credential  access_key  accesskey  token  dsn  private_key
```

安定したキーが `.dsn` で終わる場合だけ、表示に例外があります。秘密として扱う点は同じですが、
起動サマリと `pw doctor` はスキーム、ホスト、ポート、データベースのパスを残し、ユーザー情報を
`*****` に置き換えてクエリ文字列を落とします。安全に解析できない DSN は値全体をマスクします。

明示した `secret` タグが常に優先され、`mask` はマスク、`hide` は省略、`show` は値を表示します。
`show` は `token_bucket_size` のような名前の誤検知を直すためのもので、資格情報を露出するための
ものではありません。

### `dependon`：無効な機能の枝をサマリから消す

認証のスイッチを例にすると、`dependon` が消すノイズが見えます。認証が無効でもプロバイダの
パスや資格情報には既定値や設定値が残り得ますが、実行中のプロセスが使わない値まで起動時に
並べる必要はありません。

```go
type AuthConfig struct {
	Enabled bool       `default:"false"`
	Mode    string     `default:"oidc_only" dependon:".enabled"`
	OIDC    OIDCConfig `dependon:".enabled"`
}

type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string `secret:"mask"`
}
```

`auth.enabled` が `false` なら、起動サマリは判断そのものだけを表示します。

```text
auth
└─ enabled  false
```

`dependon` がなければ、無効な認証について `mode` と OIDC の全設定も表示されます。先頭のドットは
囲んでいる構造体の兄弟を指すので、上の2つはどちらも `auth.enabled` に解決されます。
`dependon:"server.tls_enabled"` のような絶対キーなら、構造体の境界も越えられます。`OIDC` の
構造体フィールドに付けたタグは、その部分木にあるすべての葉へ適用されます。依存は推移するため、
途中の親が1つでも空なら、その下も表示されません。

これは表示のフィルタであり、機能を無効にするスイッチではありません。隠れたフィールドにも
TOML、環境変数、フラグから値がバインドされ、CLI オプション、help、ひな形も変わりません。
アプリケーションの動作は、引き続き `Enabled` などの値を読んで決めます。

### `falsy`：`dependon` に「オフ」の値を教える

`dependon` は、値の不在、空文字列、boolean の `false` を最初からオフとして扱います。一方、
文字列の enum では `none` や `off` のような空でない選択肢をオフにすることがあります。
`falsy` は、その選択肢を表示上のオフとして教える補助です。

```go
type ExportConfig struct {
	Mode     string `default:"none" enum:"none,otlp,stdout" falsy:"none"`
	Endpoint string `dependon:".mode"`
	Headers  string `secret:"mask" dependon:".mode"`
}
```

`mode = "none"` なら、サマリには `export.mode` だけが残り、`endpoint` と `headers` は消えます。
`falsy:"none"` がなければ、`none` は単なる空でない文字列なので、依存する2項目も表示されたままです。

数値と duration にも明示的な判断が必要です。ゼロが有効な設定である場合もあるからです。次の例では、
ゼロが遅いステートメントの検出を無効にし、それに伴って `EXPLAIN` の項目も隠します。

```go
type QueryConfig struct {
	// ゼロで遅いステートメントの検出が止まり、EXPLAIN も止まる。
	SlowThreshold time.Duration `falsy:"0s" help:"slow statement threshold"`
	Explain       bool          `dependon:".slow_threshold" help:"run EXPLAIN on slow statements"`
}
```

falsy の値は、ほかに何も設定されなかったときにフィールドを埋める働きも持ちます。

- `default` タグが無く、どのソースもキーを設定しない——フィールドは falsy の値になる。
- あるソースがキーを `""` に設定する——falsy の値になり、そのソースを出どころとして保つ。
- `default` タグがある——default が勝ち、`falsy` が代わりに入ることはない。

比較はテキストではなく値なので、`0`・`0s`・`0ms` はどれもオフと読まれます。`falsy` タグが
使えるのは文字列、整数、duration だけです。boolean にはすでに `false` があり、リストには
安全にオフと決められる単一の値がありません。`falsy` タグが無ければ、数値や duration は
`dependon` の親になれません。ゼロが無効を意味すると推測せず、生成が失敗します。

[起動サマリ](/ja/productivity/startup-summary/)を参照してください。

## よくあるエラー

- 文字列リテラルでないプレフィックス、または空のプレフィックス
- `ParseConfig` の後に呼ばれた `pw.RegisterConfig`
- 使えない型のフィールド——float、map、ポインタ、`string` と名前付き構造体以外を要素とする
  スライス
- 解析できない `default`。`time.Duration` への裸の数値も含む
- テーブル配列の要素のフィールドへの `opt`・`env`・`falsy`・`dependon`・`secret`
- ネストした構造体のフィールドへの `falsy`
- 自分の `falsy` タグを持たないまま `dependon` に名指された数値や duration
- 2つのフィールドに割り当てられた1つの環境変数名
- プロセスが持たない変数を名指す `${NAME}`
