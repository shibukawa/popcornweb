---
title: pw init
description: 動く Popcorn Web プロジェクトを作成する。
sidebar:
  order: 1
---

```sh
pw init <project-name> [--preset=<name>] [--yes] [--tailwind] [--tinygo] [--no-devbox] [--no-database] [--db=<engine>] [--dynamo] [--firestore] [--no-redis] [--router=<kind>] [--auth=<mode>] [--session=<backend>] [--devidp] [--skills=<dir>]
```

新しいディレクトリに、動作する完全なプロジェクトを作ります。端末で実行すると
プリセットの一覧から始まり、`--yes` を付けるとオプションと既定値だけで動きます。
スクリプトから呼ぶならこちらです。

## プリセット

質問はひとつずつ見れば、どれも答える価値のあるものでした。ただ 10 個並べたとき、
まだ何も作っていない人がその順番を最後まで答えられるかというと、それは別の話です。

プリセットは、実際に使われる数少ない組み合わせに名前を付けたものです。選んだあとに
残る質問は、プロジェクト名ひとつだけになります。

| プリセット | どんなプロジェクトか | 何が決まるか |
| --- | --- | --- |
| `website-login` | ページがサインインした人のものになるサイト | 探索型ルーティング、OIDC、Redis セッション、SQLite、Tailwind |
| `website-aws` | 同じサイトを、運用するリレーショナルデータベースなしで | 探索型ルーティング、OIDC、すべて DynamoDB、Tailwind |
| `website-discovered` | アカウントも保存するものもないサイト | 探索型ルーティング、Tailwind、ログインなし、データベースなし |
| `website-registered` | 同じサイトを Go の登録として書く | 登録型ルーティング。ほかは同じ |
| `api-server` | 呼び出す側がトークンを持ってくる機械向け API | 登録型ルーティング、JWT 検証、ブラウザログインなし |
| `package` | 他のプロジェクトが import するモジュール | プロジェクトの種類が違う。[コンポーネントパッケージ](/ja/guides/deployment/package/)を参照 |
| `manual` | 上の 6 つのどれでもないもの | 何も決めない。すべて自分で答える |

TinyGo と Devbox はどのプリセットも同じ答え、つまり TinyGo は「いいえ」、Devbox は
「はい」です。この 2 つはプロジェクトの中身を変えないので、プリセットを区別する材料に
なりません。プリセットから TinyGo を選ぶこともできます。下の確認画面でその行を開いて
ください。端末なしで同じ答えを与えるなら `--preset=<name>` を使います。プリセットが
すでに答えた質問に答えるオプションを併記すると拒否されます。どちらを優先すべきか、
決める根拠がないためです。

**確認画面はプリセットが選んだ内容の一覧で、その行はすべて編集できます。** 行の上で
enter を押すとその質問が開き、答えると一覧に戻ります。プリセットは出発点であって、
あとから動かせない決定ではありません。`manual` は同じ画面を、既定値の状態で開いた
ものです。

どれを選ぶか。フレームワークを試している段階なら `website-discovered` です。ページを
返す最小のプロジェクトで、断った機能はどれも [`pw add`](/ja/pw/project/add/) ひとつで
入ります。アカウントが要ると最初からわかっているなら `website-login` を取ってください。
あとからログインを足すというのは、データベースとセッションストアとプロバイダを
まとめて足すということで、プリセットはその 3 つを正しく結線した状態で始まります。

決めた答えをあとから変える手順は
[プリセットの選択を変える](/ja/pw/project/presets/)にまとめてあります。

## オプション

| オプション | 効果 |
| --- | --- |
| `--preset=<name>` | 以下の質問すべてに一度に答える。[プリセット](#プリセット)を参照 |
| `--yes` | 質問せず、オプションと既定値で作る |
| `--tailwind` | Tailwind CSS のツールチェインも一緒にスキャフォールドする |
| `--tinygo` | TinyGo も対象にする。`pw.ServeMux` によるルーティング、`devbox.json` のツールチェイン、2 つめの Dockerfile が付く |
| `--no-devbox` | `devbox.json` を作らない。mise、Docker Compose、Nix、Homebrew、Scoop など自分の環境を使う |
| `--no-database` | rdb 設定・マイグレーション・SQL の例を作らない |
| `--db=<engine>` | `sqlite`（既定）, `postgres`, `mysql` |
| `--dynamo` | DynamoDB ストアを追加する。設定・型付きレコード・ローカルサーバー |
| `--firestore` | Datastore mode の Firestore を追加する。設定・型付きエンティティ・クエリ宣言 |
| `--no-redis` | `devbox.json` に Valkey 開発サーバーを入れない |
| `--router=<kind>` | `registered`（既定）, `discovered`, `both`。[探索型ルーティング](/ja/guides/cross-layer/discovered-routing/#コマンド)を参照 |
| `--auth=<mode>` | `none`（既定）, `oidc`, `oidc-passkey`, `passkey`, `oauth` |
| `--session=<backend>` | ログインを作る場合のセッションの置き場所: `rdb`（既定）, `cookie`, `redis`, `dynamo`, `firestore` |
| `--devidp` | OIDC を選んだ場合に、ローカルの認証プロバイダを組み込む |
| `--skills=<dir>` | 同梱のエージェントスキルの置き場所: `claude`（既定）, `agents`, `none`。[エージェントスキル](#エージェントスキル)を参照 |

`--tailwind`、`--no-database`、`--dynamo`、`--firestore`、`--no-redis`、`--auth` はいずれも、
あとから [`pw add`](/ja/pw/project/add/) で追加できる機能の選択です。断っても失うものは
ありません。ただし 2 つは他に依存します。ブラウザログインには、認証処理中のレコードと
アカウント側のレコードを保存するサーバーストアが必要です。`--no-database` と `--auth` を
併用する場合は `--dynamo` または `--firestore` も指定してください。どちらもなければ拒否されます。
Valkey サーバーは Devbox のパッケージです。`--no-devbox` は Valkey も一緒に落とし、
答えても何も適用されない質問はウィザードに
現れません。

`--tinygo` だけは `pw add` で後から変えられません。
[ツールチェインを変更する](#ツールチェインを変更する)を参照してください。既定がホストの
Go なのはそのためです。前提を置かない側であり、ルーティングはどちらでも同じだからです。

## データベースを選ぶ

`--db` は 5 つのことを一度に決めます。`config.dev.toml` の DSN、最初の
マイグレーションを書く方言、`devbox.json` に入る開発サーバー、バイナリが
リンクするドライバ、そして `popcornweb.toml` の `project.database` です。
最後の 1 つは `pw generate` が読み、`.pw.sql` をどのプレースホルダ構文に
コンパイルするかを決めます。既定が SQLite なのは、アプリケーションのほかに
起動するものが何もないからです。

| エンジン | DSN | 開発サーバー |
| --- | --- | --- |
| `sqlite` | `sqlite://<name>.db` | なし |
| `postgres` | `postgres://<name>:<name>@127.0.0.1:5432/<name>?sslmode=disable` | `devbox.json` の `postgresql` |
| `mysql` | `mysql://<name>:<name>@tcp(127.0.0.1:3306)/<name>` | `devbox.json` の `mysql80` |

どのエンジンを選んでも `main.go` にブランクインポートが 1 行入ります。これが
エンジンを登録します。

```go
import _ "github.com/shibukawa/popcornweb/database/postgres"
```

スキャフォールドが書く資格情報は `config.dev.toml` の開発用の値です。そこに書かれた
ロールとデータベースを一度だけ作ってから `pw migrate up` を実行してください。

あとからエンジンを変えることは `pw add` では行いません。DSN もマイグレーションも
`.pw.sql` もすべて書き直しになるからです。デプロイ先のエンジンを選んでください。

### 生成される SQL はエンジンに従う

`project.database` は、生成のためにプロジェクトがエンジンを表明する唯一の場所です。
ジェネレータ側に暗黙の既定はありません。黙って仮定した方言は、エンジンが最初の
クエリで拒否するプレースホルダを出力してしまうからです。`pw generate` は
`popcornweb.toml` に書かれたものだけを渡します。

```toml
[project]
database = "postgres"   # sqlite、postgres、mysql
```

| エンジン | プレースホルダ |
| --- | --- |
| `postgres` | `$1`, `$2`, … |
| `mysql` | `?` |
| `sqlite` | `?` |

このキーを変えると生成済みのクエリがすべて変わります。編集したら `pw generate` を
実行し、DSN の変更と一緒にコミットしてください。

このキーができる前に作られたプロジェクトには `[project] database` がありませんが、
`sqlite` として読まれます。当時存在したエンジンがそれだけだからです。

## ツールチェインを変更する

選んだコンパイラは `popcornweb.toml` の `project.toolchain` に記録され、ハンドラ
パッケージが使う mux の型を決めます。既定であるホスト専用のプロジェクトは
`http.ServeMux` のままで、TinyGo プロジェクトは両方のツールチェインで同じ import が
通るよう `pw.ServeMux` を経由します。生成はどちらも検出するので、違いはスキャフォールド
の中に閉じています。

あとから自動で切り替えるコマンドはありません。変更がアプリケーション側のソースにも及ぶためです。
手作業で行う場合は 4 か所です。

1. `popcornweb.toml` の `project.toolchain` を `tinygo` か `go` にする
2. 各ハンドラパッケージの `index.go` で mux の型とアクセサを差し替える
3. `devbox.json` の `tinygo@latest` を足すか外す
4. TinyGo 専用の netdev 登録 `tinygohelper.go` を足すか外す。これがないと TinyGo
   バイナリは起動時に `Netdev not set` で落ちます

そのあと [`pw generate`](/ja/pw/project/generate/) を実行します。`project.toolchain`
に `tinygo` と `go` 以外の値を書くと、プロジェクトの読み込み時に拒否されます。

## 認証

認証は複数の設定に影響するため、選んだモードが `config.dev.toml` に書かれる
`[auth]` セクションを決めます。

| 回答 | `auth.mode` | 意味 |
| --- | --- | --- |
| None | — | `[auth]` セクションを書かない |
| OIDC | `oidc` | ログインは常に OpenID Provider を経由する |
| OIDC + passkey | `oidc_passkey` | OIDC でアカウントを作り、以降はパスキーでログイン |
| Passkey only | `passkey_only` | 外部プロバイダなし。リカバリ方針は自分で決める |
| OAuth provider | `oauth_only` | X のように ID Token を発行しないプロバイダに、アクセストークンの持ち主を尋ねてログインする |

OIDC 系を選ぶと、**ローカルエミュレータ**か**外部プロバイダ**かを1つだけ追加で質問
します。OAuth の回答には追加の質問がありません。素の OAuth プロバイダにはエミュレータが
ないからです。スキャフォールドは組み込みのプロバイダ定義（`provider = "x"`）を書き、
`client_id` と `client_secret` は `AUTH_OAUTH_CLIENT_ID` と `AUTH_OAUTH_CLIENT_SECRET`
で埋めるために空にしておき、開発用のコールバック URL として
`http://localhost:8080/auth/callback` を書きます。この URL はプロバイダに登録したものと
完全に一致していなければなりません。

ローカルエミュレータは [`pw dev`](/ja/pw/project/dev/) が起動する開発用認証プロバイダ
です。`pw init` は `popcornweb.toml` に `dev.idp.enabled` を設定し、初期ユーザー2人分の
`devidp.toml` を書き出します。issuer とクライアント資格情報は `pw dev` が注入するので、
コミットする設定ファイルにプロバイダの情報は一切書かれません。

外部プロバイダの場合、`config.dev.toml` には空の `issuer`、`client_id`、
`client_secret` が書かれます。これらはプレースホルダであり、**省略可能な設定では
ありません**。ファイルまたは `AUTH_OIDC_*` 環境変数から値を与えるまで、
アプリケーションは起動を拒否します。残る選択肢はエミュレータへの切り替えです。

## セッションの置き場所

`--auth` を選ぶと、もう1つだけ質問があります。サーバに置く状態をどのバックエンドが持つか、です。
ハンドラから見た姿は 5 つとも同じで、`session.Load[T]` も auth のヘルパも変わりません。
つまりこれは API の選択ではなくデプロイの選択です。何がサーバに置かれるかのほうは、
`pw.RegisterSessionStore` の各行が決めます。

| 回答 | `session.backend` | 得られるもの |
| --- | --- | --- |
| データベース | `rdb` | セッションごとに1行。失効可能、掃除あり、マイグレーションを伴う |
| クッキー | `cookie` | レコードを2つ目のクッキーに封入。ストレージ不要、ただし失効不可 |
| Redis / Valkey | `redis` | レコードごとにサーバー側 TTL。失効可能、掃除は不要 |
| DynamoDB | `dynamo` | セッションごとに 1 item。失効可能、テーブル TTL で削除 |
| Firestore | `firestore` | セッションごとに 1 entity。失効可能、デプロイした TTL ポリシーで削除 |

**ストレージは blank import によるオプトインです。** セッションバックエンドはパッケージ
の `init` で自分を登録するので、それを import する1行が、バックエンドとそのクライアント
ライブラリをバイナリに入れる唯一の手段になります。

```go
// cmd/myapp/main.go — pw init が書き出します
import (
	// セッションと、ログインの儀式が使う単回限りのレコード。
	_ "github.com/shibukawa/popcornweb/authstate/sqlite"
	_ "github.com/shibukawa/popcornweb/sessionstore/sqlite"
)
```

SQL ストアはエンジンごとに別パッケージです。あるエンジンの DDL を別のエンジンは
読めないからです。`--db=postgres` なら `sessionstore/postgres` と
`authstate/postgres` を書き、マイグレーションも PostgreSQL の方言で出します。
`sqlite`、`postgres`、`mysql` は同じストア契約テストを通っています。

`pw init` は `cookie` 以外の各バックエンドに対応する import を書きます。クッキーバックエンドは `pw` に
組み込まれているので import は不要です。だからこそ「セッションはあるがストレージは無い」
状態から始められます。`rdb` のプロジェクトが Redis クライアントを抱えることはなく、その逆も
同じです。

import を書かずにバックエンドを設定した場合は、最初のリクエストでログインが失敗するのでは
なく、起動時に足りない行を引用して止まります。

```
session.backend = "redis" needs its plugin; add to the application:
import _ "github.com/shibukawa/popcornweb/sessionstore/redis"
```

回答は書き出される内容も変えます。`rdb` はセッションテーブルのマイグレーションを書き、
ほかのバックエンドは書きません。`dynamo` と `firestore` は、認証レコードも含めて
プロジェクトが選んだストアを使います。`redis` は `--no-redis` を渡していても Valkey の開発サーバーを `devbox.json` に
加えます。設定したセッションが到達先を必要とするからです。

`session.keyring.secret` はどの回答でも書かれます。そのプロジェクト用に生成した値が
`config.dev.toml` に入る。生成したプロジェクトは、秘密を自分で書かずに動くべきだからです。
これは開発環境のもので、それ以外の環境は `SESSION_KEYRING_SECRET` を読みます。
`pw doctor --env=prod` は、そこに直値があればエラーとして報告します。

```sh
export SESSION_KEYRING_SECRET=$(openssl rand -base64 32)
```

失効・サイズ・期限を誰が守るかという観点での比較は
[セッションストレージ](/ja/guides/storage/session-storage/)にあります。

## エージェントスキル

`pw` にはスキルが同梱されています。AI コーディングエージェントが `.pw.html`
テンプレートや `.pw.sql` クエリ、設定に手を入れる前に読み込むガイドラインで、
テンプレートとクエリの構文、プロジェクトの構造、編集を検査する `pw` コマンド群の
リファレンスを含みます。`--skills` は、そのコピーをどのエージェントディレクトリに
置くかを決めます。

| 答え | 書き出されるもの |
| --- | --- |
| `claude`（既定） | `.claude/skills/popcornweb/` — Claude Code が発見するディレクトリ |
| `agents` | `.agents/skills/popcornweb/` — 他のコーディングエージェントが読む共通レイアウト |
| `none` | 何も置かない |

コピーは Markdown だけで、ランタイムには何のコストもかかりません。置くのが既定なのは
そのためです。この答えはプロジェクトそのものではなく、プロジェクトを編集するマシンに
ついての答えなので、プリセットは決して決めません。エージェントで編集しない
プロジェクトは `--skills=none` を渡すか、`.vscode/` を消すのと同じ感覚であとから
ディレクトリごと削除してください。ファイルはプロジェクトを作った `pw` バイナリから
来ており、あとから更新するコマンドはありません。フレームワークを更新して新しい
ガイドラインが欲しくなったら、リポジトリの `skills/popcornweb-skill/` から現行の
ツリーをコピーしてください。

## 検証

プロジェクト名に使えるのは英数字、`-`、`_` です。`.` と `..` は拒否されます。作成先は
空であるか存在しないかのどちらかでなければなりません。既存ツリーでうっかり
`pw init .` を実行してもファイルが散らばることはなく、エラーで止まります。

## 書き出されるもの

```
myapp/
├── popcornweb.toml           プロジェクト名、main パッケージ、生成対象ディレクトリ
├── config.dev.toml            APP_ENV=dev のランタイム設定
├── config.prod.toml           APP_ENV=prod 用の同じ構造。秘密は ${NAME} で参照
├── .env.example               デプロイが供給する変数の一覧。.env.local にコピーして使う
├── go.mod
├── devbox.json / devbox.lock  Go + Valkey（--tailwind なら tailwindcss も）
├── cmd/myapp/main.go          pw.Run を呼ぶ
├── handlers/
│   ├── index.go               パッケージレベルの mux と Handlers()
│   ├── home_handler.go        ルート登録と net/http ハンドラ
│   └── home.pw.html           型付きページテンプレート
├── templates/
│   ├── document.pw.html       共有ドキュメントシェル
│   ├── templates.go           初回生成前から存在するパッケージマーカー
│   └── 400|401|403|404|409|413|500.pw.html   エラーページ
├── queries/users.pw.sql       型付き結果を持つ名前付き SQL（データベース選択時）
├── migrations/00001_init.sql  初期スキーマ、goose 形式（データベース選択時）
├── public/.keep               空ツリーの番兵。配信されない
├── public.go                  public/ を埋め込んで登録する
├── .claude/skills/popcornweb/  同梱のエージェントスキル（--skills で移動または省略）
├── .vscode/settings.json      **/*_pw_gen.go を隠す
└── .gitignore                 *_pw_gen.go、.env.local と .env.*.local などを除外
```

`popcornweb.toml` には、いま作ったディレクトリが `[generate]` の各用途に振り分けて
書かれます。[`pw generate`](/ja/pw/project/generate/) はこれらのリストだけを読み、
既定値を持たないためです。スターターのページテンプレートはハンドラの隣にあるので
`handlers` は `handlers` と `templates` の両方に現れ、アプリケーションが設定を登録する
`cmd/myapp` は `config` に現れます。

OIDC 系のモードで `--devidp` を付けると、選択できる開発用ユーザーの一覧である
`devidp.toml` も書き出し、`popcornweb.toml` に `[dev.idp]` を追加します。

`--tailwind` を付けると、さらに `assets/app.css` と `public/generated/app.css` を書き、
`popcornweb.toml` に `[assets.tailwind]` ブロックを追加し、`devbox.json` に
`tailwindcss` をピン留めし、ドキュメントシェルからスタイルシートをリンクします。
`package.json` も Node のロックファイルも作られません。あとから有効にする方法は
[スタイリング](/ja/guides/frontend/styling/)を参照してください。

各ファイルは一時パスに書いてから所定の場所へリネームされます。コマンドが中断しても、
書きかけのソースファイルは残りません。

## 実行されるもの

ファイルを書くだけでは、スキャフォールドが使えることの証明になりません。そのため
`pw init` は成功を報告する前に `go mod tidy` と
[`pw generate`](/ja/pw/project/generate/) を実行し、すぐにコンパイルできる
プロジェクトを残します。

```
Created myapp

Not included: redis-valkey, auth, tailwind
  pw add <capability> enables one later

  cd myapp
  devbox shell
  pw dev
```

この案内には、その実行で断ったものだけが並びます。非対話で `pw init` を回した場合
でも、ウィザードが各選択肢の横に書いているのと同じことが分かります。

Devbox を断つと、入るシェルが無いので `devbox shell` の行も消えます。その場合
Tailwind のツールチェインもピン留めされないため、何を入れるべきかを表示します。

```
Tailwind CSS needs its own toolchain here:
  install the standalone tailwindcss CLI, version 4 or later
```

Devbox のパッケージ名ではなく要件を書きます。`tailwindcss_4@4.1.18` は nixpkgs の
識別子であり、mise や Homebrew、Scoop を使う人には何も伝えないからです。バイナリが
見つからないときの [`pw build`](/ja/pw/project/build/) も同じ内容を報告します。

生成される `go.mod` は、それを作った `pw` バイナリのバージョンのフレームワークを
require します。`pw` がリリース版ではなく作業コピーからビルドされていた場合は、代わりに
そのチェックアウトを指す `replace` ディレクティブが書かれます。

## 次のステップ

- [1. はじめる](/ja/tutorial/getting-started/) — 生成物の詳しい解説。
- [pw add](/ja/pw/project/add/) — ここで断った機能をあとから追加する。
- [pw new](/ja/pw/project/new/) — 2 つめのハンドラを追加する。
- [プロジェクト構成](/ja/guides/architecture/project-structure/) — 1 パッケージを超えて成長させる。
