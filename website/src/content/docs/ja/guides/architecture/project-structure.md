---
title: プロジェクト構成と設計原則
description: pw が作る開発環境、機能単位のパッケージ、生成コードと連携する net/http ハンドラという3つの観点から、プロジェクト構成を説明します。
sidebar:
  order: 2
---

Popcorn Web のプロジェクト構成は、フォルダツリーを見るだけでは理解できません。
プロジェクト全体では `pw` がアプリケーションのビルドと開発環境の起動を担い、
個々の処理ではハンドラがリクエストを受け取ってレスポンスを返します。フォルダ構造は、
この2つの役割をつなぐものです。

どの階層でも方針は変わりません。Go と Web の標準的なインターフェースは隠さず、
その周辺で繰り返し必要になる処理だけを再利用できる形にまとめます。

## 開発環境を抱える `pw`

開発中、アプリケーションバイナリは単独で動いているわけではありません。`pw dev` がソースを監視し、
Go コードを生成し、マイグレーションとアセットのビルドを済ませてからバイナリを起動し、
変更があれば入れ替えます。その周囲では開発用 IdP、構造化ログ、テレメトリ受信、
データベース操作、テンプレートの storybook、診断機能も動いています。

<figure>
<svg viewBox="0 0 700 410" role="img" aria-label="pw プロジェクトツールが開発環境を抱える図。ライフサイクルのコマンドはテンプレート、SQL、Go のソースを読み、アプリケーションバイナリを作る。pw dev の中では監視、生成、マイグレーション、ビルド、再起動がバイナリを動かし、開発用 IdP、テレメトリ、ログ、データベースコンソール、storybook、doctor が支える。popcornweb.toml は pw が読み、実行時設定、環境変数、フラグはアプリケーションバイナリが読む。">
  <defs>
    <marker id="pw-arrow-ja" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" opacity="0.65"/>
    </marker>
  </defs>
  <rect x="155" y="16" width="525" height="324" rx="10" fill="currentColor" fill-opacity="0.035" stroke="currentColor" stroke-width="1.5" opacity="0.7"/>
  <text x="175" y="42" fill="currentColor" font-family="inherit" font-size="15" font-weight="600">pw — プロジェクトツール</text>
  <g fill="currentColor" fill-opacity="0.07" stroke="currentColor" stroke-width="1" opacity="0.9">
    <rect x="180" y="58" width="142" height="44" rx="5"/>
    <rect x="338" y="58" width="142" height="44" rx="5"/>
    <rect x="496" y="58" width="158" height="44" rx="5"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="11" text-anchor="middle">
    <text x="251" y="77">init · add · new</text>
    <text x="251" y="92" opacity="0.6">プロジェクトを作る</text>
    <text x="409" y="77">generate · prepare · build</text>
    <text x="409" y="92" opacity="0.6">生成してパッケージする</text>
    <text x="575" y="77">migrate · seed · doctor</text>
    <text x="575" y="92" opacity="0.6">操作して検査する</text>
  </g>
  <rect x="180" y="120" width="474" height="198" rx="8" fill="currentColor" fill-opacity="0.035" stroke="currentColor" stroke-width="1.5" opacity="0.65"/>
  <text x="198" y="145" fill="currentColor" font-family="inherit" font-size="13" font-weight="600">pw dev — 開発用の cradle</text>
  <rect x="326" y="174" width="176" height="70" rx="6" fill="currentColor" fill-opacity="0.13" stroke="currentColor" stroke-width="1.5"/>
  <text x="414" y="204" fill="currentColor" font-family="inherit" font-size="13" text-anchor="middle" font-weight="600">アプリケーションバイナリ</text>
  <text x="414" y="222" fill="currentColor" font-family="inherit" font-size="10" text-anchor="middle" opacity="0.65">デプロイするものと同じプログラム</text>
  <g fill="currentColor" fill-opacity="0.055" stroke="currentColor" stroke-width="1" opacity="0.85">
    <rect x="198" y="166" width="108" height="92" rx="5"/>
    <rect x="522" y="166" width="112" height="92" rx="5"/>
    <rect x="239" y="270" width="350" height="32" rx="5"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="10" text-anchor="middle">
    <text x="252" y="187">監視</text>
    <text x="252" y="204">コード生成</text>
    <text x="252" y="221">migration · assets</text>
    <text x="252" y="238">build · restart</text>
    <text x="578" y="187">開発用 IdP</text>
    <text x="578" y="204">telemetry · logs</text>
    <text x="578" y="221">data · queries</text>
    <text x="578" y="238">storybook · doctor</text>
    <text x="414" y="291">開発コンソールとビルド失敗時のオーバーレイ</text>
  </g>
  <g stroke="currentColor" stroke-width="1.2" fill="none" marker-end="url(#pw-arrow-ja)" opacity="0.55">
    <path d="M306 211 L326 211"/>
    <path d="M522 211 L503 211"/>
    <path d="M414 270 L414 245"/>
    <path d="M132 76 L179 76"/>
    <path d="M132 201 L197 201"/>
    <path d="M132 319 L154 319"/>
    <path d="M414 374 L414 245"/>
  </g>
  <g fill="currentColor" fill-opacity="0.04" stroke="currentColor" stroke-width="1" opacity="0.8">
    <rect x="10" y="43" width="122" height="66" rx="5"/>
    <rect x="10" y="168" width="122" height="66" rx="5"/>
    <rect x="10" y="286" width="122" height="66" rx="5"/>
    <rect x="277" y="374" width="274" height="30" rx="5"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="10" text-anchor="middle">
    <text x="71" y="65">.pw.html · .pw.sql</text>
    <text x="71" y="82">Go · migrations</text>
    <text x="71" y="99">assets · testdata</text>
    <text x="71" y="194">データベースと</text>
    <text x="71" y="211">ローカルサービス</text>
    <text x="71" y="312">popcornweb.toml</text>
    <text x="71" y="329">project/tool の設定</text>
    <text x="414" y="394">config.{env}.toml · 環境変数 · アプリケーションのフラグ</text>
  </g>
</svg>
</figure>

外側の箱はツールの境界であって、実行時の依存ではありません。`pw build` が
アプリケーションバイナリを作り、本番環境ではそのバイナリだけを動かします。`pw` も、
開発コンソールも、開発用 IdP も、storybook も要りません。開発環境が豊かなのは、
リリースバイナリに道具を隠したからではなく、`pw dev` が周囲の道具をまとめて動かすからです。

### 原則: 1つのコマンドで開発環境を管理する

新しいプロジェクトの最初の仕事を、互いに無関係なツールのインストールと接続先の転記には
しません。`pw init` が動く環境を作り、以後は `pw dev` の1コマンドで戻ってこられます。
コード生成、マイグレーション、seed data、ローカル認証、ログ、trace、テンプレートと
データベースの確認。多くのアプリケーションが必要とするものは、最初からプロジェクトへ
接続されています。

これは Web フロントエンドのツール群が進めてきた考え方を取り込んだものです。コンパイラ、
watcher、stylesheet tool、標準プロトコルを、各チームが README を見ながら毎回つなぐよりも
便利な形にパッケージする。ただし、部品の正体は隠しません。Tailwind は Tailwind、
テレメトリは OTLP、認証は OIDC、そして動くサービスは Go のバイナリのままです。

### 2種類の設定と、2つの読み手

プロジェクトには TOML が2種類あります。見た目が似ていても、読むプログラムが違います。

| 入力 | 読むもの | 決めるもの |
| --- | --- | --- |
| `popcornweb.toml` | `pw` | プロジェクトルート、main package、生成範囲、migration、assets、開発ツール |
| `config.{APP_ENV}.toml` | application binary | server、database、authentication、session、observability、アプリケーション設定 |
| `.env`、`.env.{APP_ENV}` とその `.local` | application binary | このチェックアウトで環境変数レイヤーが読む値。共有するものはコミットされる側に、秘密は git が無視する `.local` のふたつに置く |
| 環境変数とアプリケーションのフラグ | application binary | 実行環境での runtime 設定の上書き |

`dev.logs` が `popcornweb.toml` にあるのは、アプリケーションの隣で動く開発プロセスを
制御するからです。`server.port` は実際に bind するバイナリの設定なので
`config.dev.toml` に置きます。本番でも境界は同じです。リリースバイナリには runtime 設定が
必要ですが、自分をビルドしたプロジェクトのフォルダ構成を読む理由はありません。

## フォルダは feature に従う

cradle の中身は普通の Go module です。`pw init` は、1つの handler package と1つの
query package から浅く始めます。利用者や所有者の異なる領域が生まれたら、技術レイヤー
ではなく feature に沿ってツリーを育てます。

```text
myapp/
├── popcornweb.toml
├── config.dev.toml
├── cmd/myapp/main.go
├── templates/
│   ├── document.pw.html          唯一の document shell
│   └── 400|404|500.pw.html
├── migrations/
├── webroot/
│   ├── index.go                  root mux。feature を mount する
│   ├── home_handler.go
│   ├── home.pw.html
│   ├── admin/
│   │   ├── index.go              admin mux
│   │   ├── dashboard_handler.go
│   │   ├── dashboard.pw.html
│   │   └── queries/reports.pw.sql
│   └── accounts/
│       ├── index.go              accounts mux
│       ├── signup_handler.go
│       ├── signup.pw.html
│       └── queries/accounts.pw.sql
└── queries/
    └── users.pw.sql              複数の feature が共有する query
```

ハンドラと、それが描画するテンプレートは隣に置きます。feature だけが使う query は
feature が所有し、複数の feature が実際に共有するようになってから上へ移す。どこが
どの機能が変更を担うかは、設計資料を開かなくてもパスから判断できます。

### 各 feature が mux を持つ

feature package の形は、小さな scaffold と同じです。

```go
// webroot/admin/index.go
package admin

import "github.com/shibukawayoshiki/popcornweb/pw"

var mux = pw.NewServeMux()

func Handlers() *pw.ServeMux { return mux }
```

```go
// webroot/admin/dashboard_handler.go
package admin

func init() { mux.HandleFunc("GET /dashboard", dashboard) }
```

root が子を import して mount します。

```go
// webroot/index.go
package webroot

func init() {
	mux.Handle("/admin/", http.StripPrefix("/admin", admin.Handlers()))
	mux.Handle("/", accounts.Handlers())
}
```

`admin` 内の path は mount 位置からの相対です。親が子を import するため、子の `init` が
route を登録してから親が mount します。子は親を import しないので循環もありません。
subtree pattern と `http.StripPrefix` は、素の `net/http` による合成です。

:::caution
feature を `/public/` に mount するのは避けてください。埋め込み static assets は既定で
`server.public.mount = "/public"` を使います。feature か asset mount のどちらかを動かして
ください。衝突した場合、起動時に報告されます。
[静的ファイル配信](/ja/guides/frontend/static-assets/)も参照してください。
:::

### コード生成が読む範囲

生成範囲は用途ごとに明示します。

```toml
[generate]
handlers = ["webroot"]
templates = ["webroot", "templates"]
queries = ["webroot", "queries"]
config = ["cmd/myapp"]
```

`webroot/admin/queries` は `webroot` の指定に含まれます。編集が必要なのは、新しい
トップレベルのソースディレクトリを増やしたときだけです。暗黙の既定値はありません。
キーが無ければエラー、意図して生成しない用途は `[]` です。生成物と範囲外ソースの診断は
[`pw generate`](/ja/pw/project/generate/)にあります。

feature package をいくつ増やしても、次の3つは全体で1つです。

- `document.pw.html` の document shell
- `migration.dir` にある順序付き migration 集合
- 登録された設定 prefix が共有する名前空間

### 原則: layer by feature

トップレベルを `controllers`、`services`、`repositories`、`models` に分けると、1つの
feature が一般名の package へ散らばります。境界ごとに request 用、永続化用、domain 用の
型と mapper を作っても、新しく増える知識は値のコピー方法だけかもしれません。レビュー時間、
バイナリ、人間の注意、AI が読むコンテキストには、どれも実在するコストがかかります。

Popcorn Web の既定は逆です。feature の内部は浅く保ち、Go package と mux で feature を
合成し、共有所有が生まれてから共有 package を取り出します。異なる知識を持つ、または実在する
依存の向きを反転させる。layer は、そのどちらかを担って初めて置く理由を得ます。

domain knowledge を SQL から追い出す必要もありません。schema constraint、query の形、index、
transaction boundary は、アプリケーションが何を許し、どのように失敗するかを決めています。
生成された query はそれを見えるままにし、database を遠く見せるためだけの汎用 CRUD repository
を途中に置きません。

## 1つのハンドラは `net/http` のまま

最小のスケールは1リクエストです。ここでも、フレームワークは馴染みのある中心を置き換えず、
その周囲を支えます。

| 関心事 | 動作 |
| --- | --- |
| 処理の単位 | 1つの `http.Handler` |
| リンク | 既定では完全な document request |
| フォーム | 通常の submit と redirect |
| 変更操作 | handler または application service |
| 変更後の browser default | Post/Redirect/Get |
| transaction boundary | `pw.Transaction` で明示 |
| client-side enhancement | 任意 |

<figure>
<svg viewBox="0 0 700 210" role="img" aria-label="リクエストが生成された型付きバインダを通って標準の net/http ハンドラに入る。ハンドラはデータベースへ向けて生成されたクエリ関数を呼び、HTML 出力へ向けて生成されたテンプレート関数を呼ぶ。Popcorn Web のレスポンスヘルパが最終的な HTTP レスポンスを書く。request、response writer、context、redirect、status の判断はハンドラに残る。">
  <defs>
    <marker id="handler-arrow-ja" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" opacity="0.65"/>
    </marker>
  </defs>
  <g fill="currentColor" fill-opacity="0.045" stroke="currentColor" stroke-width="1.2">
    <rect x="12" y="78" width="92" height="48" rx="5"/>
    <rect x="132" y="65" width="130" height="74" rx="5"/>
    <rect x="292" y="54" width="142" height="96" rx="7" fill-opacity="0.12" stroke-width="1.7"/>
    <rect x="466" y="18" width="132" height="55" rx="5"/>
    <rect x="466" y="132" width="132" height="55" rx="5"/>
    <rect x="622" y="18" width="66" height="55" rx="5"/>
    <rect x="622" y="132" width="66" height="55" rx="5"/>
  </g>
  <g fill="currentColor" font-family="inherit" text-anchor="middle">
    <text x="58" y="99" font-size="12">HTTP</text>
    <text x="58" y="115" font-size="12">request</text>
    <text x="197" y="89" font-size="11">pw.Parse[T]</text>
    <text x="197" y="106" font-size="10" opacity="0.65">生成された binding</text>
    <text x="197" y="122" font-size="10" opacity="0.65">と validation</text>
    <text x="363" y="84" font-size="13" font-weight="600">net/http handler</text>
    <text x="363" y="105" font-size="10" opacity="0.65">w · r · context</text>
    <text x="363" y="122" font-size="10" opacity="0.65">status · redirect · policy</text>
    <text x="532" y="41" font-size="11">生成された query</text>
    <text x="532" y="58" font-size="10" opacity="0.65">型付き parameter と row</text>
    <text x="532" y="155" font-size="11">生成された template</text>
    <text x="532" y="172" font-size="10" opacity="0.65">型付き HTML parameter</text>
    <text x="655" y="50" font-size="11">database</text>
    <text x="655" y="155" font-size="11">HTTP</text>
    <text x="655" y="171" font-size="11">response</text>
  </g>
  <g stroke="currentColor" stroke-width="1.3" fill="none" marker-end="url(#handler-arrow-ja)" opacity="0.65">
    <path d="M104 102 L131 102"/>
    <path d="M262 102 L291 102"/>
    <path d="M434 81 L465 54"/>
    <path d="M598 46 L621 46"/>
    <path d="M434 123 L465 151"/>
    <path d="M598 159 L621 159"/>
  </g>
  <text x="350" y="202" fill="currentColor" font-family="inherit" font-size="10" text-anchor="middle" opacity="0.58">application logic は中央に残し、表現間のデータ移動を境界で生成する</text>
</svg>
</figure>

さらに1段引いて見ると、このハンドラは見慣れた server stack の中にあります。
`http.Server` が接続を受け、framework middleware が mux を包み、`http.ServeMux` が
application code を選ぶ。次の図では、標準ライブラリ、framework runtime、application code、
application source から生成された code を色で分けています。

![http.Server、フレームワークのミドルウェア、http.ServeMux を経てハンドラに届き、生成されたバインダ・クエリ関数・コンポーネント関数を呼んだあとランタイムがレスポンスを書くまでの図](../../../../../assets/diagrams/request-parts.svg)

コードも Go 開発者が知っている形のままです。

```go
type createMemoInput struct {
	Body string `form:"body" check:"required,maxlen=1000"`
}

func createMemo(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[createMemoInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	if _, err := queries.CreateMemo(r.Context(), input.Body); err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	http.Redirect(w, r, "/memos", http.StatusSeeOther)
}
```

ハンドラが受け取るのは `http.ResponseWriter` と `*http.Request` です。control flow、status と
redirect、外部システムの呼び出し、transaction boundary はハンドラが所有します。
`r.Context()` も Go のライブラリが理解する carrier のままです。

消えるのは、表現間の移動を手で書く仕事です。`pw.Parse` は生成された binder を使い、path、
query、header、form、JSON の入力を型付き struct へ移して検証します。生成された query 関数は、
型付き parameter と row を SQL との間で運びます。生成された template 関数は型付き parameter
から HTML を作り、`pw.WriteProblem` と HTML response helper が protocol の形を統一します。

生成の境界は有限で、ソースから見えます。

| 自分が持つソース | 生成される Go |
| --- | --- |
| `*.pw.html` | 型付き component 関数と parameter struct |
| `*.pw.sql` | context を取る型付き query 関数と row scan |
| `pw.Parse[T]` の呼び出し箇所 | `T` の request binding と validation |
| `pw.WriteAPI[T]` / `pw.WriteStream[T]` の呼び出し箇所 | `T` の response encoding |
| `pw.RegisterConfig[T]` の呼び出し箇所 | `T` の起動時 configuration binding |
| 上記すべて | OpenAPI 3.1 fragment |

生成ファイルはソースの隣に置かれ、名前は `_pw_gen.go` で終わります。これは build output です。
`pw generate` が上書きし、`pw dev` は関係するソースの変更後に再生成します。query の row 不一致、
template parameter の渡し忘れ、不正な出力 context、binding error は request ではなく build を
止めます。request 時の reflection も消えるため、TinyGo を実用的な target にできます。

### 原則: common sense を残し、境界を生成する

`net/http` を置き換えると、Go 開発者、library、debugger、test が共有している知識まで
捨てることになります。Popcorn Web は mux pattern、handler signature、request context、
middleware model、redirect、status code をそのまま使います。

ただし、馴染みがあることと、表現間のデータ移動を毎回手で書くことは別です。request binding、
SQL row、configuration、template parameter は、生成によって型検査と診断を追加できる機械的な
境界です。framework の abstraction はそこへ使い、中央は普通の Go に残します。

browser runtime も同じ原則に従います。標準 link、form、完全な response、型付き binding、
template、error、configuration、OpenAPI は browser runtime に依存しません。server-driven update
が必要な画面だけ追加の layer を import し、最小の application は component graph、patch protocol、
hydration dependency のコストを払いません。

3つの構造はここで揃います。`pw` は release に入り込まず開発環境を package する。feature package
は Go の合成を隠さず所有権を表す。生成された境界は、request の意味を決める `net/http` handler を
隠さず、データを運ぶためだけのコードを取り除きます。
