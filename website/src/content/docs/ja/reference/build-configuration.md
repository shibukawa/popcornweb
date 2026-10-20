---
title: ビルドツール設定一覧
description: popcornweb.toml の全設定項目。コード生成、pw dev と同時に起動するサービス、マイグレーションやスタイルシートの配置を指定します。
sidebar:
  order: 2
---

`popcornweb.toml` はプロジェクトルートに置かれ、`pw` コマンドのものです。書いてあるのは
*プロジェクト*のこと——生成がどのディレクトリを読むか、ソースがどのコンパイラ向けに
書かれているか、開発中にアプリケーションの横で何が動くか。

ランタイムの設定は一切ありません。ポート、コネクションプール、クッキー、ログレベルは
`config.{APP_ENV}.toml` にあり、
[アプリケーション設定一覧](/ja/reference/configuration/)に一覧があります。この分離は
慣習ではなく強制です。ここに `server` や `session` テーブルを書けばエラーですし、
データベースの接続文字列も同様です。2 つのファイルは、別のプログラムが別のタイミングで
読みます。

このファイルを書くのは [`pw init`](/ja/pw/project/init/) で、機能を追加したときに
編集するのは [`pw add`](/ja/pw/project/add/) です。手で編集することも想定されています。
以下はローダーが実際に検査している規則です。

## `[project]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `name` | *(必須)* | プロジェクト名。`pw dev` が注入する `OTEL_SERVICE_NAME` でもある |
| `kind` | `"application"` | `application` はバイナリを作る。`package` は Go モジュールとして公開する |
| `main` | *(application では必須)* | [`pw build`](/ja/pw/project/build/) がコンパイルするパッケージ。例 `"./cmd/myapp"` |
| `toolchain` | `"tinygo"` | ソースがどのコンパイラ向けに作られたか。`tinygo` または `go` |
| `database` | `"sqlite"` | `.pw.sql` がどの方言で生成されるか。`sqlite`, `postgres`, `mysql` |

`toolchain`、`database`、`kind` は、これ以外の値を拒否します。そしてどの既定値も、好みでは
なく歴史です。キーが存在しなかった頃のプロジェクトは TinyGo でしかありえず、SQLite でしか
ありえず、アプリケーションでしかありえませんでした。

`kind` は、後述の 2 つのセクションのどちらが正当かを決めます。パッケージは `main` を
持ちません。エントリポイントを定義するのは、それを import するアプリケーションです。
そしてアプリケーションが `[package]` セクションを持っているのは、無視されるブロックでは
なくエラーです。[コンポーネントパッケージ](/ja/guides/deployment/package/)を参照してください。

`database` は*生成*への入力です。生成された Go があなたの SQL をどの方言として読むかを
決めます。アプリケーションが実際に接続するエンジンは、いまも
`[[middleware.rdb.connections]]` の DSN のスキームから決まります。この2つはアプリケーション側で一致させてください。保持を禁じられている
DSN を、このファイルが検査できるはずもありません。

## `[generate]`

```toml
[generate]
handlers = ["handlers"]
templates = ["handlers", "templates"]
queries = ["queries"]
config = ["cmd/myapp"]
pages = []
dynamo = []
firestore = []
```

クエリがないプロジェクトでも `queries = []` を明示します。`generate.queries` の省略と、空の対象リストは別の意味です。

各 purpose は、[`pw generate`](/ja/pw/project/generate/) が*その purpose のために*読んで
よいディレクトリを列挙します。それ以外は読みません。`queries` が挙げていないディレクトリの
`.pw.sql` は、生成処理から見えません。そのため、設定された用途のディレクトリ外に置かれた
`.pw.html` や `.pw.sql` を黙って拾うのではなく、警告します。

| キー | 読む対象 | 必須 |
| --- | --- | --- |
| `generate.handlers` | ハンドラのソース。ルートとバインディングの解析対象 | はい |
| `generate.templates` | `.pw.html` テンプレート。ドキュメントシェルを含む | はい |
| `generate.queries` | `.pw.sql` のソース | はい |
| `generate.config` | 設定の登録 | はい |
| `generate.pages` | [ページツリー](/ja/guides/cross-layer/discovered-routing/)のルート | いいえ |
| `generate.dynamo` | `dynamo` タグ付きの Go 型と `.pw.dynamo` 宣言 | いいえ |
| `generate.firestore` | `firestore` タグ付きの Go 型と `.pw.firestore` 宣言 | いいえ |

`pages`、`dynamo`、`firestore` 以外はすべて必須で、その purpose が何も生成しないことを表すのが `[]`
です。キーを書かないことではそれを表現できません。だから空リストと省略は同じではありません。
任意の 2 つは、プロジェクトが先に存在しうるものです。ページツリーが存在する前に作られた
プロジェクトにはキーもツリーも無く、DynamoDB を使わないプロジェクトには
ストア固有のキーは、`pw add dynamo` または `pw add firestore` がそのストアを追加したときに
書かれます。

`dynamo` がテンプレート言語ではなく Go の型宣言を読むので、`queries` の一部ではなく独立した
purpose になっています。[DynamoDB クエリフォーマット](/ja/reference/dynamo-templates/)を参照して
ください。

`firestore` も同じ理由で独立しており、`dynamo` とは別の purpose です。どちらか一方だけでも、
両方でも使えます。[Firestore クエリフォーマット](/ja/reference/firestore-templates/)を参照してください。

エントリ自体の規則は次のとおりです。

- プロジェクトからの相対パスで、存在するディレクトリを指すこと
- 重複しないこと。同じ purpose の別エントリの内側に入れ子にしないこと——内側のソースは
  2 回計画され、2 回目の計画が 1 回目の出力を消してしまいます
- [ドキュメントシェル](/ja/guides/frontend/templates/)を持つ `generate.templates`
  エントリはちょうど 1 つ。2 つ目はエラーです
- `generate.pages` のエントリはツリー全体です。`templates` や `handlers` に重ねて
  挙げることも、それらのエントリと入れ子にすることもできません

ディレクトリ名は既定値であって、識別子ではありません。どの利用者も名前ではなく purpose の
リストを読むので、`handlers/` を `web/` に改名するのは、ディレクトリを移動して 1 行編集する
ことです。生成されるパッケージ名はディレクトリに従うので、ソースはそのままコンパイルできます。

## `[dev.watch]`

```toml
[dev.watch]
includes = []
excludes = []
```

[`pw dev`](/ja/pw/project/dev/) はリビルドの入力を探してモジュールを歩きます。生成と違って
既定の動作があるので、両方のキーとも省略できます。`includes` は歩きが見落とすファイルや
glob パターンを相対パスで追加します。`excludes` はディレクトリのサブツリーを飛ばします。
歩きを遅くするだけの大きなツリーには効きます。

## `[dev.idp]`

```toml
[dev.idp]
enabled = false
config = "devidp.toml"
port = 0
```

`pw dev` がアプリケーションの横で動かす
[開発用の認証プロバイダ](/ja/productivity/dev-identity-provider/)です。`enabled = true`
にはユーザー定義ファイルの存在が必要です。`port = 0` は空いているループバックポートを
確保します。`pw dev` が解決済みの issuer をアプリケーションに注入するので、これが有用な
既定値です。固定の番号が意味を持つのは、このプロジェクトの外に登録されたクライアントが
いる場合だけです。

## `[dev.otel]`

```toml
[dev.otel]
enabled = true
port = 0
max = 0
```

[テレメトリビューア](/ja/productivity/dev-telemetry-viewer/)であり、ここで唯一、既定で
有効なブロックです。`port = 0` がループバックポートを確保し、`pw dev` が解決済みの
エンドポイントを注入するのは `dev.idp` とまったく同じです。`max` はシグナルごとの保持件数の
上限で、`0` ならビューア自身の既定値になります。

`dev.idp` も `dev.otel` も、影響するのは `pw dev` だけです。

## `[migration]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `dir` | `"migrations"` | [マイグレーションファイル](/ja/productivity/migrations/)の場所。プロジェクトからの相対パス |
| `auto` | `true` | `pw dev` の開始時に未適用のマイグレーションを適用する |

`auto` が有効にするのは、開発ループのこの 1 ステップだけです。アプリケーションが起動時に
自分でマイグレーションを実行するようになることは決してありません。それはコードに明示的に
書く呼び出しのままです。リクエストを処理するプロセスが、スキーマを変更すべきプロセスである
ことは滅多にないからです。

`dir` はツール側のパスです。ファイルの場所を `pw` に伝えるだけで、ランタイムの意味は
ありません。

## `[assets]`

```toml
[assets]
public = "cmd/app/public"
derived = "cmd/app/dist/public"
embed = "cmd/app/public.go"
```

| キー | 既定値 | 用途 |
| --- | --- | --- |
| `assets.public` | `"public"` | 編集する入力ツリー |
| `assets.derived` | `"dist/public"` | 配信・埋め込み対象のビルド済みツリー |
| `assets.embed` | `"public.go"` | アセット構築前に検査する、手書きのGo埋め込みファイル |

すべて `popcornweb.toml` があるディレクトリからの相対パスです。ルート外へ出るパス、
途中の要素を含むシンボリックリンク、入力と出力が重なる配置は拒否します。
`go:embed` の制約により、派生ツリーは埋め込みファイルのディレクトリ配下に置きます。
上の例では `cmd/app/public.go` に `//go:embed all:dist/public` と
`fs.Sub(embeddedPublic, "dist/public")` を記述します。

`public_manifest_pw_gen.go` は埋め込みファイルと同じディレクトリ・パッケージに生成します。
`manifest.json`、`routes.json`、中間出力の `derived/`、変換キャッシュの `cache/` は
派生ツリーの隣、上の例なら `cmd/app/dist/` に出力します。コンポーネントから抽出した
ファイルは入力ツリーの `generated/` 配下です。Tailwindの入出力は引き続きプロジェクト相対です。

`pw dev` は `assets.derived` を `SERVER_PUBLIC_LOCAL_ROOT` としてアプリに渡します。
直接 `go run -tags=pwdev` する場合は `server.public.local_root` を指定できます。
既定値は `dist/public` です。プロジェクトルート、生成するimportパス、設定ファイル、
dotenvの探索基準は変わりません。

## `[assets.tailwind]`

```toml
[assets.tailwind]
enabled = true
input = "assets/app.css"
output = "public/generated/app.css"
minify = true
```

プロジェクトを [Tailwind](/ja/guides/frontend/styling/) 付きで作った場合にあり、そうでなければ
ありません。`input` と `output` は別のファイルで、どちらもプロジェクトからの相対パスです。

`minify` だけは変わっています。[`pw build`](/ja/pw/project/build/) はキーの値に関わらず
minify し、`pw dev` は決してしません。この値が実際に効くのは
[`pw doctor`](/ja/pw/project/doctor/) で、デプロイ先の環境に対して minify されていない
スタイルシートを readiness の指摘として報告します。`true` のままにしてください。

Tailwind のプラグインはここでは設定しません。CSS のエントリに書く `@plugin` 宣言であり、
解決するのは Tailwind CLI です。Popcorn Web はエントリをそのまま渡すだけで、プラグインの
レジストリを持ちません。

## `[assets.images]`、`[assets.css]`、`[assets.scripts]`

```toml
[assets.images]
enabled = true
quality = 75
avif = false

[assets.css]
minify = true

[assets.scripts]
enabled = true
```

[アセット変換](/ja/guides/frontend/static-assets/)のスイッチです。すべて既定で無効なので、
何も宣言しないプロジェクトは authored なツリーの複製を埋め込み、これらが存在しなかった頃と
まったく同じものを配信します。

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `assets.images.enabled` | `false` | `img src` が指す `.png` / `.jpg` を WebP に変換する |
| `assets.images.quality` | `75` | JPEG ソースを再エンコードする際の品質。PNG は可逆のままで、この値を見ない |
| `assets.images.avif` | `false` | 配信する画像に AVIF 表現を追加し、`Accept` で選ばせる |
| `assets.css.minify` | `false` | スタイルシートをその場で minify する |
| `assets.scripts.enabled` | `false` | `.ts` / `.tsx` エントリをビルドし、authored な `.js` を minify する |

`assets.images` はホスト側のエンコーダを必要とします。[`pw add images`](/ja/pw/project/add/)
がキーと Devbox パッケージを同時に書くのはそのためです。ツール無しで有効にしてもエラーには
なりません——変換は見送られ、authored な画像がそのまま出荷され、`pw doctor` がそれを報告
します。変換されていない画像は、壊れたページではなく重いページだからです。

## `[assets.verify]`

```toml
[assets.verify]
enabled = true
svg_scan = true
allow = ["vendor/**"]
```

どちらの検査も、ビルドがダイジェストを取るために既に手元に持っているバイトを読むだけです。
だから上の変換群とは違い、既定で**有効**です。

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `assets.verify.enabled` | `true` | authored な public ファイルのうち、中身が拡張子と食い違うものを拒否する |
| `assets.verify.svg_scan` | `true` | authored な `.svg` のうち、`<script` / `on…=` ハンドラ / `javascript:` を含むものを拒否する |
| `assets.verify.allow` | `[]` | 両方の検査を免除するパス。`public/` からの相対で、末尾 `/**` はサブツリー全体を指す |

拒否は `pw build` を失敗させ、ファイル名と、拡張子が主張していた型と、実際のバイトが何である
かを挙げます。免除されたパスはその代わりにビルドが出力します。一つの困ったファイルのために
足した行が消されないまま残る、という形で検査が黙り込まないようにするためです。

[`pw doctor`](/ja/pw/project/doctor/) は同じ二つの条件をビルド無しで報告します。
[PW0130](/ja/appendix/diagnostics/#pw0130-a-public-files-content-does-not-match-its-extension)
と
[PW0131](/ja/appendix/diagnostics/#pw0131-a-public-svg-carries-executable-content)
です。それぞれの検査に何が判定できて、SVG 側がなぜ意図的に不完全なのかは
[静的アセット](/ja/guides/frontend/static-assets/)にあります。

## `[[packages]]` — アプリケーション側

```toml
[[packages]]
module = "example.com/widget"
```

アプリケーションが使う[コンポーネントパッケージ](/ja/guides/deployment/package/)を
1 つにつき 1 エントリ書きます。キーは `module` だけで、`go.mod` にも入っている必要が
あります。

このエントリがパッケージを*リンク*します。[`pw generate`](/ja/pw/project/generate/) が、
管理しているブートストラップファイルに宣言ごとの blank import を書き出すからです。
`go.mod` にあってこの一覧に無いモジュールは、ふつうの Go の依存です。逆に `[package]`
セクションを持たないモジュールを宣言するのはエラーです。そのモジュールが公開していない
機能を主張していることになります。

## `[package]` — パッケージ側

```toml
[package]
module = "example.com/widget"
summary = "自前のストレージを持つメモウィジェット"
assets.declared = true
routes.register = "Register"

[package.requires]
capabilities = ["database"]
engines = ["sqlite"]

[package.generated_with]
pw = "v0.4.0"
tinybind = "v0.3.5"

[package.migrations]
dir = "migrations"
stem = "widget"
engines = ["sqlite"]
```

| キー | 意味 |
| --- | --- |
| `module` | *(必須)* Go のモジュールパス。`go.mod` と一致していること |
| `summary` | 1 行。`pw add` がパッケージを表示するときに使う |
| `import` | モジュールルートと異なる場合に、アプリケーションがリンクするパッケージパス。ルートに Go が無いのに省略すると `PW0144` |
| `requires.capabilities` | パッケージが必要とするプロジェクト機能。`database` など |
| `requires.engines` | 対応する SQL エンジン。空なら SQL に触れない |
| `generated_with.pw`, `generated_with.tinybind` | コミット済み生成物を作ったバージョン |
| `config.section` | パッケージが登録する実行時設定のセクション |
| `migrations.dir` | マイグレーションストリーム。モジュールルートからの相対 |
| `migrations.stem` | *(`dir` があれば必須)* ストリームのバージョンテーブルとパッケージのテーブルを命名する |
| `migrations.engines` | ストリームがどのエンジン向けに書かれているか |
| `routes.register` | アプリケーションがマウントのために呼ぶ公開シンボル |
| `assets.declared` | 埋め込みブラウザ資産を登録するかどうか |
| `components.exported` | 予約。今日書けばロードエラー |

`generated_with` はそれ自体は何も制約しません。解決を行うのは `go.mod` です。これは
[`pw doctor`](/ja/pw/project/doctor/) が、このプロジェクトより新しいフレームワークで
生成されたパッケージを報告するときに突き合わせる証拠です。

`migrations.engines` は `requires.engines` が宣言するものをすべて含んでいなければ
なりません。そうでなければ、スキーマを書いたことのないエンジンへの対応を主張することに
なり、失敗するのは宣言の時点ではなく最初のマイグレーションになります。

パッケージでは `generate.queries` は空でなければなりません。生成されたクエリは 1 つの
エンジンのプレースホルダ構文を持ちますが、パッケージは利用側のエンジンを知りません。

## ファイル全体にかかる規則

- **未知のキーはエラーです。** 打ち間違いが黙って無視されることはありません。
- **相対パスはこのファイルのディレクトリから解決され**、絶対パスは拒否されます。
  プロジェクトはひとまとまりで移動します。
- **コマンドのフラグがファイルより優先されます。** `pw migrate --dir=other` は、何も
  編集せずにその 1 回だけ別のディレクトリを読みます。
- **このファイルがプロジェクトの位置を決めます。** プロジェクトを対象にするコマンドは
  ——[`pw init`](/ja/pw/project/init/) と `pw version` 以外のすべて——作業ディレクトリから
  上に向かってこのファイルを探します。だから `pw` はどのサブディレクトリからでも動きます。
- **ランタイムの値は禁止です。** `server`、`session`、`security`、`middleware`、
  `observability` のテーブルはもう一方のファイルのものですし、データベースの接続文字列も
  そちらです。`project.database` が名指すのはエンジンであって、DSN でも資格情報でも
  ありません。
