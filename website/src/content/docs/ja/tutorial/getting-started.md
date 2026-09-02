---
title: 1. はじめる
description: プロジェクトを作成してページを書き換え、テンプレートの不整合をコンパイラで検出するまでを試します。
sidebar:
  order: 1
---

このチュートリアルは全5章で、`memoapp` というメモアプリケーションを育てていきます。
最後にはフォームとテーブルがあり、ログインした人ごとにメモが分かれます。
出発点は、挨拶を1行返すだけのページです。

この章では、そのページを作り、動かし、書き換えます。所要時間はおよそ15分。
その大半は最初のビルドを待つ時間です。

:::note[はじめる前に]
必要なのは Go 1.27 以降と `pw` コマンドだけです（[インストール](/ja/start/installation/)）。
Devbox は任意で、どちらでも進められます。違いが出るところはその都度書きます。
残りは `pw init` が用意します。
:::

## 1. プロジェクトを作る

```sh
pw init memoapp
```

普段プロジェクトを置いているディレクトリで実行してください。`memoapp/` が作られます。
すでにファイルがあるディレクトリへの書き込みは拒否されるので、既存の作業の上に
雛形をばらまいてしまう事故は起きません。

名前を書いてもウィザードは出ます。名前は10個ある質問のうちの1つでしかなく、
それを知っているからといって残りに答えたことにはならないからです。矢印か `jk` で移動、
数字で直接ジャンプ、`Enter` で確定、`Esc` で1つ戻る、`Ctrl-C` で中止。最後に全部の回答が
並ぶので、書き込みが始まる前に見直せます。

このチュートリアルでは次のように答えてください。

| 質問 | 回答 | 理由 |
|---|---|---|
| Project name | `memoapp` | |
| TinyGo support | No | 既定のまま。このチュートリアルでは使いません |
| Router | Registered | 既定のまま |
| Tailwind CSS | **No** | 2章で `pw add` で入れます |
| Authentication | None | 4章で `pw add` で入れます |
| Database | **No** | 3章で `pw add` で入れます |
| DynamoDB | No | このチュートリアルでは使いません |
| Devbox environment | Yes | 既定のまま |
| Redis or Valkey | Yes | 既定のまま |

Tailwind とデータベースを断るのは、後の章で足すためです。初期化で断った機能が
[`pw add`](/ja/pw/project/add/) でそのまま入ることを、説明で読むのではなく手元の
プロジェクトで一度やってみます。

認証は先に聞かれます。ログインを入れるかどうかが、ストアが任意かどうかを決めるからです。
ここで None を選ぶと、そのあとは「データベースを入れますか」という形で聞かれます。
ログインを選んだ場合は「どのストアに置きますか」に変わり、断る選択肢はありません。
セッションはどこかに置くしかないからです。

スクリプトから非対話で実行したいときは `--yes` を付けます。フラグと既定値だけで
最後まで進みます。端末が無い環境（CI など）では最初からウィザードは出ません。

ファイルを書いて終わりではありません。`pw init` は続けて `go mod tidy` と `pw generate` を
実行します。成功を報告する時点で、生成されたプロジェクトはコンパイルできる状態です。

```
Created memoapp

  .              .editorconfig  .gitignore  config.dev.toml  devbox.json  go.mod  popcornweb.toml  public.go
  .vscode/       extensions.json  settings.json
  cmd/memoapp/   main.go
  handlers/      home.pw.html  home_handler.go  index.go
  public/        .keep  app.css
  templates/     400.pw.html  401.pw.html  ... document.pw.html  templates.go

12 generated files, rebuilt any time by pw generate

Not included: database, dynamo, auth, tailwind
  pw add <capability> enables one later

  cd memoapp
  devbox shell
  pw dev
```

名前が出ているのは手で書くファイルだけです。生成されるファイルは件数だけ。
`*_pw_gen.go` はビルド入力で、同じ `pw init` が書いた `.gitignore` が除外していますし、
消えても `pw generate` が作り直します。開いて編集するものではありません。

## 2. 動かす

```sh
cd memoapp
devbox shell
pw dev
```

Devbox を使わない場合は `devbox shell` を飛ばして `pw dev` を直接実行します。
必要なのは `PATH` の通った Go だけです。

開発ループはこの1コマンドに収まっています。[`pw dev`](/ja/pw/project/dev/) は
`devbox.json` のサービスを起動し、コードを生成し、未適用のマイグレーションを適用し、
そのうえでアプリケーションをビルドして実行します。監視対象のファイルが変わるたびに
再起動し、`Ctrl-C` で全部まとめて止まります。

そのサービスの1つが Valkey です。このチュートリアルでは一度も接続しません。
あとからキャッシュやレート制限が必要になったときにサーバーがすでに動いているように、
`devbox.json` にピン留めされているだけです。使う予定がなければパッケージごと外せます。

アプリケーションは起動内容を1回だけ報告し、最後に受け付けたアドレスを出します。

```
   .-.   .-.
 .(   ) (   ).    Popcorn Web v0.1.0
(   o     o   )   started at 2026-07-28 09:12:31 JST
(    \___/    )   env dev · config.dev.toml
 '-.__.___.__-'

configuration
├─ observability
│  ├─ minimum_level  debug       ← file
│  └─ stdout_format  plaintext   ← file
└─ server
   └─ port  8080  ← file

listening on http://localhost:8080
```

実際の木はもっと長く、フレームワークとアプリケーション双方の解決済みキーがすべて並びます。
既定値以外から来た値には印が付き、接続の DSN のような秘密は伏せられます（3章で
データベースを足すと、その `rdb` の枝もここに出てきます）。この出力が
何のためにあり、端末につながっていないときに何になるかは
[何が効いたのかを見る](/ja/guides/architecture/configuration/#何が効いたのかを見る)にあります。

<http://localhost:8080/> を開いてください。**Hello, World** と表示されます。
雛形のハンドラはクエリパラメータも読むので、<http://localhost:8080/?name=Popcorn> なら
名前入りの挨拶になります。

![Popcorn への挨拶、導入済みの機能、次に行うことが表示された、生成直後の memoapp ランディングページ](../../../../assets/screenshots/tutorial-getting-started.png)

### もう1つのアドレス

アプリケーションが何か言うより前に、ループはもう1行出していました。

```
pw dev: console http://127.0.0.1:18081
```

[開発コンソール](/ja/productivity/dev-console/)です。このチュートリアルの残りは、
書いたものをここで確かめながら進みます。各章で書くのはテンプレート、マイグレーション、
SQL 文といったもので、そのそれぞれに、他のコードが揃う前に単体で動かせるペインが
あります。ストーリーブックはテンプレート1つを、その型から作った引数で描画します。
data ペインはアプリケーションが開いたテーブルをそのまま見せます。queries ペインは
宣言した文を実行して、返ってきた行を出します。

いま開いておく価値はあります。概要にはループが今どの段階にいるかが出ていて、
ビルドが失敗したことも、すでにスクロールして消えた端末ではなくここでわかります。
アプリケーションが返すどのページにも隅に小さなボタンがあり、同じコンソールを開くので、
上のアドレスを覚えておく必要はありません。

![プロジェクト名、環境、開発ループの現在の段階、reseed ボタン、ペイン一覧が並ぶコンソールの概要画面](../../../../assets/screenshots/dev-console-overview.png)

コンソールはアプリケーションの一部ではありません。どのペインも `pwdev` ビルドタグの
下でコンパイルされ、[`pw build`](/ja/pw/project/build/) はそのタグを立てません。
リリースバイナリには、有効にできるものが最初から入っていません。

## 実際に編集するファイル

`pw init` は20数個のファイルを書きました。今日触るのは3つです。

| ファイル | 中身 |
| --- | --- |
| `handlers/home.pw.html` | ページテンプレート。ブラウザに出るもの |
| `handlers/home_handler.go` | ルートと、リクエストから読む入力 |
| `templates/document.pw.html` | すべてのページが描画される外枠 |

`popcornweb.toml` は名前だけ覚えておいてください。ツールチェイン、データベースエンジン、
生成の各目的がどのディレクトリを読むかを記録しています。`config.dev.toml` は
`APP_ENV=dev` のランタイム設定で、さきほどのポートとログ形式はここから来ています。

データベースを断ったので、`queries/` も `migrations/` もまだありません。3章で
`pw add database` が両方を持ってきます。ツリー全体は
[`pw init`](/ja/pw/project/init/#書き出されるもの) にあります。

全体に効くルールが1つあります。すべての `.pw.html` と `.pw.sql` は、**ソースの隣**の
`_pw_gen.go` にコンパイルされます。これらはビルド生成物です。Git は無視し、
VS Code は隠し、`pw generate` が作り直します。編集するのはソースであって、
生成された Go ではありません。

### ページ

`pw init` が書いたのは挨拶1行ではなく、このプロジェクトに何が入っていて次に何が
できるかを並べたトップページです。全文は手元のファイルで見てください。ここで見るのは
先頭の2行です。

```html
// handlers/home.pw.html
package handlers

export component Home(name: string, project: string): html {
  <div class="page">
    <header>
      <p class="eyebrow">Popcorn Web</p>
      <h1 class="title">{project}</h1>
      <p class="lead">Hello, {name}. This page is yours to delete; nothing in the framework reads it.</p>
    </header>
    <!-- 入っている機能、次にやること、ドキュメントへのリンクが続きます -->
  </div>
}
```

消して構いません。フレームワークはこのページを読んでいません。3章で丸ごと
書き換えます。

これは Go ではありませんし、実行時テンプレートでもありません。`.pw.html` は小さな
型付き言語で、`pw generate` がこのファイルから Go の関数 `Home` とパラメータ構造体
`HomeParams` を生成します。型の誤りや危険な HTML の埋め込みは、リクエストが来たときではなく
コンパイル時に落ちます。言語そのものは[テンプレート](/ja/guides/frontend/templates/)にあります。
ここで効いてくるのはパラメータの並びです。

Tailwind を断ったので、`class` の中身は `pw init` が書いた `public/app.css` が
定義しているクラス名です。断って困るのはユーティリティが使えないことであって、
ページが素っ気なくなることではありません。2章で `pw add tailwind` を入れると、
同じ構造が Tailwind のユーティリティで書かれます（[スタイリング](/ja/guides/frontend/styling/)）。

### ハンドラ

```go
// handlers/home_handler.go
package handlers

import (
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
)

// homeInput is what this route reads from the request.
type homeInput struct {
	// Name is who the page greets. Anything the request does not carry falls
	// back to the declared default.
	Name string `query:"name" default:"World"`
}

func init() { mux.HandleFunc("GET /{$}", home) }

// home renders the starter landing page.
//
// The greeting is whoever the request names, and the project the page was
// scaffolded for otherwise.
func home(w http.ResponseWriter, r *http.Request) {
	input, err := pw.Parse[homeInput](r)
	if err != nil {
		pw.WriteProblem(w, r, pw.BadRequest(err))
		return
	}
	pw.WriteHTML(w, r, Home(HomeParams{Name: input.Name, Project: "memoapp"}))
}
```

ふつうの `net/http` ハンドラに、フレームワークの呼び出しが2つ入っています。
`pw.Parse` はリクエストから `homeInput` を埋めます。ここでは `?name=` から、
無ければ宣言された既定値から。`pw.WriteHTML` は `Home` が返したフラグメントを描画します。

godoc は飾りではありません。`pw generate` はハンドラのコメントを OpenAPI 文書に
書き写します。最初の1文が操作の要約、残りが説明になり、`homeInput` の型と
フィールドのコメントはスキーマとパラメータの説明になります。2章の最後で、
それがどこに出るかを見ます。

`mux` は `handlers/index.go` にあり、3行です。

```go
// handlers/index.go
package handlers

import "net/http"

var mux = http.NewServeMux()

func Handlers() *http.ServeMux { return mux }
```

各ハンドラファイルが `init` で自分のルートを登録します。ルートを増やすことは
ファイルを増やすことであって、機能を足すたびに全員が触る表を編集することではありません。

標準ライブラリのルーターそのもので、このファイルにフレームワークの型は1つも
出てきません。`pw init` で TinyGo を選んだプロジェクトはここが `pw.ServeMux` に
なります。ホストの Go ではそれもこの `net/http.ServeMux` の型エイリアスで、
包んでいるのではなくそのものです。TinyGo でビルドしたときだけ、同じパターン構文を
実装した互換実装に入れ替わります。`import` を1つ書き換えずに両方のターゲットへ
出せることがこの型の役目で、ホストだけを狙うプロジェクトが払う理由はありません。

登録するパターンは Go 1.22 のもの、つまり `"GET /users/{id}"` のままで、
`r.PathValue` も標準どおりに動きます。持っているのはルートとメソッドのマッチと
パスパラメータだけです。ミドルウェアもルートのメタデータもここにはありません。

ハンドラが言及して*いない*ものにも注意してください。`doctype`、`html`、`head`、`body` は
`templates/document.pw.html` にあり、`pw.WriteHTML` がページのフラグメントをその外枠の中に
描画します。ページテンプレートが持つのは葉の部分だけです。

## 3. ページを書き換える

`pw dev` は動かしたままにしてください。`handlers/home.pw.html` を編集します。

```html
// handlers/home.pw.html
package handlers

export component Home(name: string, project: string): html {
  <h1 class="title">Hello, {name}</h1>
  <p>Served by Popcorn Web.</p>
}
```

保存すると、`pw dev` が `home_pw_gen.go` を再生成し、ビルドし直し、アプリケーションを
再起動します。ブラウザをリロードすれば段落が増えています。

## 4. わざと壊す

マークアップを足すぶんには壊れようがありません。壊れうるのはコンポーネントの
*インターフェース*を変えたときで、生成された境界が仕事をするのもそこです。
やってみましょう。パラメータ名を変えます。

```html
// handlers/home.pw.html
package handlers

export component Home(visitor: string, project: string): html {
  <h1 class="title">Hello, {visitor}</h1>
}
```

`pw dev` は再生成し、ビルドしようとして、止まります。

```
handlers/home_pw_gen.go
# memoapp/handlers
handlers/home_handler.go:21:37: unknown field Name in struct literal of type HomeParams
```

パラメータ名を変えたことで `HomeParams` のフィールド名も変わり、ハンドラはまだ `Name` を
埋めようとしています。テンプレートと呼び出し側が名前について食い違い、その食い違いが、
直すべき行を名指ししたコンパイルエラーになりました。しばらく経ってからページの一部が
空白になる、という形ではなく。（`pw dev` はこの下にもう1つエラーを出します。ビルドできなかった
バイナリから設定を読もうとして失敗した、というものです。原因は1つ、メッセージは2つ。）

開いたままのブラウザのタブを、何もせずに見てください。同じ失敗がページの上に
かぶさっています。アプリケーションが返すページには小さなスクリプトが入っていて、
コンソール側のループ状態を見ているからです。壊れたビルドは、後ろに隠れた端末ではなく、
すでに見ている場所に出ます。

ただしリロードはしないでください。`pw dev` はリビルドの前に動いていたアプリケーションを
止めるので、ビルドが通るまで応答するものは何もありません。リロード無しで出てこなければ
ならなかった理由がそれです。ビルドを直せば、ページは自分で戻ってきます。

ハンドラを直します。

```go
	pw.WriteHTML(w, r, Home(HomeParams{Visitor: input.Name}))
```

保存すればビルドが通り、アプリケーションが再起動し、ページが戻ります。

そのうえで、両方の編集を元に戻してください。パラメータ名を `name` に戻し、
`HomeParams{Name: input.Name}` に戻します。次の章は雛形のハンドラから始まります。

## 起動しないとき

| 出ているもの | 対処 |
| --- | --- |
| `listen tcp :8080: bind: address already in use` | 別のプロセスがポートを握っています。前の `pw dev` である可能性が高い。止めるか、`config.dev.toml` の `server.port` を変える |
| `devbox: command not found` | [Devbox](https://www.jetify.com/devbox/) を入れるか、`devbox shell` を飛ばして `PATH` の Go で `pw dev` を実行する |
| `go mod tidy` がダウンロードで失敗する | モジュールキャッシュが空でネットワークに拒否されています。再試行するか、到達できる `GOPROXY` を設定してください。雛形はもう書かれているので `pw init` をやり直す必要はありません |
| 行と桁が付いた `.pw.html` のエラー | 生成がテンプレートを拒否しました。メッセージが位置と規則を名指しします（[テンプレート](/ja/guides/frontend/templates/#エラー)） |

## ここまでで手元にあるもの

- 動いて、保存のたびにリロードし、`/` のほかに `/healthz`、`/readyz`、`/openapi.json`、
  `/docs` にも応答するプロジェクト。
- 型付きの Go 関数にコンパイルされたページテンプレートと、それを呼ぶハンドラ。
- 両者が食い違ったときに失敗するビルド。

2章では、このページを送信する価値のあるものにします。フォームと POST ハンドラ、そして
人に向かって自分を説明しなければならないバリデーションです。

- [2. フォームとバリデーション](/ja/tutorial/forms/) — 次の章。
- [プロジェクト構成と設計原則](/ja/guides/architecture/project-structure/) — いま動かしたツール、パッケージ、ハンドラのモデル。
- [pw コマンド](/ja/pw/overview/) — サブコマンドの全体。
