---
title: アプリケーション設定一覧
description: 実行中のアプリケーションが使うランタイム設定キー、既定値、TOML・環境変数・コマンドラインでの名前の一覧。
sidebar:
  order: 1
---

ここに並ぶのは*動作中のアプリケーション*が読むキーです。ポート、コネクションプール、
クッキー、ログレベル。そのアプリケーションをビルドするために `pw` 自身が読むファイルは
別物で、スキーマも別です。そちらは
[ビルドツール設定一覧](/ja/reference/build-configuration/)を参照してください。

構造体のフィールド1つが、3つの入力になります。`ServerConfig.ReadHeaderTimeout` は
TOML では `server.read_header_timeout`、環境変数では `SERVER_READ_HEADER_TIMEOUT`、
コマンドラインでは `--server-read_header_timeout` です。しかも後ろ2つは、誰かが
保守している対応表ではなく規則で最初のひとつから導かれます。そこでこのページの
一覧では各キーを1回だけ挙げます。残りの2つは自分で導けます。

規則には例外があり、この規則の例外は表を見る前に知っておく価値がある程度の数です。
設定を*どう使うか* —— 自分の設定を宣言し、ハンドラで読み、ひな形を生成する —— は
[アプリケーション設定](/ja/guides/architecture/configuration/)にあります。

## 残り2つの名前の導き方

TOML のキーの `.` を `-` に置き換えて `--` を付けます。
`observability.query.slow_threshold` なら `--observability-query-slow_threshold`
です。キーの中のアンダースコアはそのまま残り、変わるのは階層を区切るドットだけ
です。

そのオプション名からダッシュを外して大文字にすれば
`OBSERVABILITY_QUERY_SLOW_THRESHOLD` になります。

7つのキーは意図的に規則を外れています。慣例的な名前がすでに存在していて、
アプリケーション側に読み替えを強いる理由がないからです。

| キー | 環境変数 |
| --- | --- |
| `server.port` | `PORT`（オプションは `--port`） |
| `observability.service_name` | `OTEL_SERVICE_NAME` |
| `observability.otel.endpoint` | `OTEL_EXPORTER_OTLP_ENDPOINT` |
| `observability.otel.headers` | `OTEL_EXPORTER_OTLP_HEADERS` |
| `observability.trace.sampler` | `OTEL_TRACES_SAMPLER` |
| `observability.trace.sampler_arg` | `OTEL_TRACES_SAMPLER_ARG` |
| `auth.oidc.issuer`, `client_id`, `client_secret`, `redirect_url` | `AUTH_OIDC_*`。デプロイ時の redirect は固定し、ループバック開発ではリクエストからの導出も可能 |

環境変数を持たないキーも3つあります。
`security.headers.content_security_policy`、その `_report_only` 版、
`security.headers.permissions_policy` です。TOML で設定してください。

`[[middleware.rdb.connections]]` の配列も TOML 専用です。テーブルの配列には
バインドできる平坦な名前がありません。だから配備先の DSN は `${DATABASE_URL}` の参照として
書きます。展開するのはファイル層で、未定義の名前は空の DSN ではなく読み込みエラーです。

## 値の出どころ

`APP_ENV` が実行環境を選び、それがプロジェクトローカルのファイル名を決めます。
Popcorn Web は次の順に読みます。

1. `./config.{APP_ENV}.toml`
2. `./config/config.{APP_ENV}.toml`
3. ユーザおよびシステムの設定ディレクトリ。こちらのファイル名は環境非依存の
   `config.toml`

プロジェクトツリーに置いた素の `config.toml` は読まれません。後のソースが前を
上書きし、環境変数とオプションはすべてのファイルを上書きします。

環境変数そのものは、作業ディレクトリの dotenv ファイルの上にプロセスの環境を重ねて
組み立てられます。`./.env`、`./.env.local`、`./.env.{APP_ENV}`、`./.env.{APP_ENV}.local`、
次に `/run/secrets` の下の 1 ファイル 1 変数、最後にプロセスの順で、同じ名前は後のものが
勝ちます。`.gitignore` が除外するのは `.local` のふたつで、そこやマウントから来た値は
表示されるどこでもマスクされます。`APP_ENV` はプロセス、次に `.env` と `.env.local` から読まれ、
`.env.{APP_ENV}` 系のふたつにあるものは警告付きで無視されます。呼び出し側が環境を渡すロード（Cloudflare Workers の
ビルド）はファイルを読みません。[秘密情報と dotenv ファイル](/ja/guides/architecture/configuration/#秘密情報と-dotenv-ファイル)を参照してください。

期間は Go の duration 文字列（`5s`、`200ms`、`2m`、`24h`）、サイズはバイト数の
整数です。リストは TOML の配列、環境変数ではカンマ区切りの値になります。

## `[server]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `port` | `8080` | HTTP の待ち受けポート |
| `read_header_timeout` | `"5s"` | リクエストヘッダ読み取りのタイムアウト |
| `read_timeout` | `"30s"` | リクエスト読み取りのタイムアウト |
| `write_timeout` | `"0s"` | レスポンス書き込みのタイムアウト。ゼロは長時間のストリームを許容する |
| `idle_timeout` | `"2m"` | keep-alive のアイドルタイムアウト |
| `shutdown_timeout` | `"10s"` | グレースフルシャットダウンのタイムアウト |
| `max_request_body` | `10485760` | リクエストボディの上限（バイト） |
| `cbor_max_body` | `0` | [CBOR リクエストボディ](/ja/guides/backend/cbor/)の上限（バイト）。`0` はデフォルトの 1 MiB |
| `trusted_proxies` | `[]` | 信頼するプロキシの IP または CIDR |
| `health` | *(空)* | liveness エンドポイントのパス。例 `/healthz` |
| `readiness` | *(空)* | readiness エンドポイントのパス。例 `/readyz` |
| `openapi` | *(空)* | OpenAPI ドキュメントのパス。例 `/openapi.json` |
| `api_doc` | *(空)* | API ドキュメント UI。`scalar`, `swagger`, または空 |
| `api_doc_path` | `"/docs"` | その UI のマウント先 |
| `api_catalog` | `false` | `/.well-known/api-catalog`で[RFC 9727のカタログ](/ja/appendix/web-standards/#apiディスカバリ)を返す |
| `api_catalog_origin` | *(空)* | カタログのリンクを組み立てる絶対オリジン。空なら相対参照で書く |
| `public.enabled` | `true` | 埋め込み静的アセットを配信する |
| `public.mount` | `"/public"` | そのマウント先 |
| `public.read_local` | `false` | 埋め込みツリーではなくディスクから読む |

運用系の3エンドポイントに既定パスがないのは意図的です。`/healthz` で応答する
アプリケーションなら、設定を読む運用者の目に入る場所にそう書かれているべきです。
既定値を持たせれば、どのファイルにも書かれていないエンドポイントが3つ動くことに
なります。

アプリケーションのルートが有効な運用エンドポイントと衝突した場合、どちらかが
もう一方を覆い隠す前に起動が失敗します。`api_doc` はさらに `openapi` を必要とします。
誰も配信していないドキュメントの UI には、描画するものがありません。
[API ドキュメント](/ja/productivity/api-documentation/)を参照してください。

`api_catalog` がパスではなくスイッチなのは、RFC 9727 が場所を固定しているからです。
運用者がこのファイルから読み取れない唯一のフレームワークのアドレスでもあります。
有効にするには `openapi` か `api_doc` が要ります。`api_doc` が `openapi` を必要とするのと
同じ理由で、プローブと自分自身しか指さないカタログはどの API も記述しておらず、
起動はそれを配信するのではなく拒否します。

`api_catalog_origin` は任意です。未設定ならカタログのリンクは相対参照になり、クライアントが
取得した URL を基準に解決されます。カタログをたどる相手にとってはこれで正しく動きます。指定が
要るのは、カタログを「保存」する相手が現れたときです。RFC 9264 が相対でない参照を求めるのは、
保存されたリンクセットも解決できるようにするためです。フレームワークがリクエストの `Host` から
オリジンを推測することはありません。呼び出し側が選ぶヘッダから推測した絶対 URL は残り続け、
自分の持たないホストを指しうるからです。開発環境以外での未設定は、`pw doctor` が
PW0429（note）として報告します。

## `[middleware]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `recovery` | `true` | panic したハンドラを 500 に回収する |
| `request_id` | `true` | リクエスト相関 ID を採番して伝播する |
| `access_log` | `true` | リクエストごとに1レコード |
| `compression` | `false` | 受け入れるクライアントにレンダリングした HTML と JSON を符号化して返す |
| `compression_codings` | `["zstd", "gzip"]` | 提供するコーディング。良い順。外したものは提供しない |
| `request_timeout` | `"0s"` | リクエスト単位の期限。ゼロなら設けない |
| `rdb.enabled` | `false` | フレームワーク所有のデータベースプールを開く |
| `rdb.default_group` | *(空)* | どのグループも指定しないステートメント用の接続グループ |
| `rdb.write_group` | *(空)* | フレームワークの書き込み用の接続グループ |
| `rdb.migration_group` | *(空)* | マイグレーションとシード用の接続グループ |

`compression` が有効なとき、`Vary: Accept-Encoding` はどちらの経路でも付きます。
ひとつの表現をキャッシュしたものが、別のものを求めるクライアントへ渡ってはいけない
からです。

`compression_codings` はサーバが選ぶ順序です。クライアントの q 値ではありません。
あちらが言うのは「何を読めるか」だけです。未知の名前は起動エラー、既知だがビルドタグで
エンコーダを外した名前は飛ばされ、起動ログに出ます。圧縮を止めるのは空リストではなく
`compression = false` です。エンコーダのレベルは意図的に設定できません。
[レスポンス圧縮](/ja/guides/backend/compression/)を参照してください。

データベースはすべて下の接続セットで設定します。プール1つにつきテーブル1つで、単一の
データベースならテーブル1つ、リーダ・ライタ構成なら複数です。セクション自体は DSN を
持ちません。DSN を探す場所は1か所だけです。

以前は `rdb.dsn` とその隣のプール系キーでも書けました。この形式は削除しました。DSN と
プールの上限は `[[middleware.rdb.connections]]` のテーブル1つへ移してください。テーブルが
1つもないまま有効にしたデータベースは起動時に失敗し、移行先のテーブルを名指しします。

### `[[middleware.rdb.connections]]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `group` | *(空)* | この接続を指す名前 |
| `dsn` | *(空)* | DSN。表示されるときにマスクされるのは資格情報だけ |
| `readonly` | `false` | 読み取り専用トランザクションを開き、フレームワークの書き込みは引き受けない |
| `connect_timeout` | `"5s"` | 以下、接続ごとに上と同じ |
| `max_open_conns` | `0` | |
| `max_idle_conns` | `0` | |
| `conn_max_lifetime` | `"0s"` | |
| `conn_max_idle_time` | `"0s"` | |

`readonly` の接続が `pw.SelectWriteDB` に選ばれることはありません。だからこそ、
書き込みを行う側はデプロイ構成を知らずに済みます。
[リレーショナルデータベース](/ja/guides/storage/rdb/)を参照してください。

### `[middleware.firestore]`

これらのキーは `database/firestore` を import したときだけ存在します。データベースは
Datastore mode で作成されている必要があります。

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `enabled` | `false` | クライアントを開き、リクエスト context に設定する |
| `project_id` | *(空)* | Google Cloud プロジェクト。空なら `GOOGLE_CLOUD_PROJECT`、次に `DATASTORE_PROJECT_ID` |
| `database` | *(空)* | 名前付きデータベース。空なら既定のデータベース |
| `namespace` | *(空)* | プロセスが読み書きするすべてのキーに適用する namespace |
| `endpoint` | *(空)* | サービスまたはエミュレータの接続先。空なら `DATASTORE_EMULATOR_HOST` |
| `credentials` | `"service_account"` | `service_account`, `metadata`, `oauth2`, `static` |
| `credentials_file` | *(空)* | サービスアカウント鍵。空なら `GOOGLE_APPLICATION_CREDENTIALS` |
| `timeout` | `"10s"` | 1 リクエストの上限 |
| `max_idle_conns` | `4` | クライアントが保持する idle HTTP 接続数 |

`metadata` と `static` は `credentials_file` を読まないため、同時に設定するとエラーです。
ゼロ以下の timeout、負の接続数も起動時に拒否されます。
[Firestore](/ja/guides/storage/firestore/)を参照してください。

## `[html]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `streaming` | `true` | `false` はストリーミング可能なチェーンでもバッファ経路を強制する |
| `async_timeout` | `"3s"` | await 境界1つの上限。ゼロならリクエストコンテキストだけが期限になる |
| `async_concurrency` | `0` | 1回の描画で同時に走る境界処理の数。ゼロ以下は無制限 |
| `bot_detection` | `true` | クローラや CLI クライアントには確定済みのドキュメントを描画する |
| `bot_async_timeout` | `"5s"` | bot と判定したリクエストでの境界の上限 |
| `bot_user_agents` | `[]` | 追加の `User-Agent` 部分文字列。大文字小文字を無視して照合する |
| `scriptless_detection` | `true` | スクリプトを切ったブラウザに、noscript リダイレクト経由で確定済みのドキュメントを返す |
| `live` | `true` | ドキュメント完成後もページを更新し続ける live 接続に応える |
| `live_max_duration` | `"10m"` | live 接続1本の寿命。これを過ぎると閉じ、クライアントが再接続する |
| `live_duration_jitter` | `20` | その寿命を接続ごとにばらつかせる割合（%） |
| `live_idle_timeout` | `"5m"` | 何も配信していない live 接続を閉じるまでの時間 |
| `live_max_boundaries` | `32` | live 接続1本が扱う境界数。ゼロ以下は無制限 |
| `live_max_responses` | `4` | クライアントあたりの同時 live 接続数。ゼロ以下は無制限 |
| `update.enabled` | `false` | ナビゲーション差分・再描画・アクションレスポンスに答える |
| `update.validator_key` | — | 境界ダイジェストにかける鍵。`update.enabled` が true なら必須で、鍵素材が 32 バイト未満なら拒否 |
| `update.max_manifest_bytes` | `8192` | リクエストが運べるダイジェストのヒントの上限 |
| `cache.enabled` | `true` | `@cache` コンポーネントの描画結果を再利用する |
| `cache.max_entries` | `1024` | プロセス内の描画キャッシュが保持する項目数。ゼロ以下は無制限 |

await 境界を開くテンプレートは、`streaming` がどちらでも正しく描画されます。この
キーが決めるのは、その裏の処理が確定する前に fallback がブラウザへ届くかどうか
だけです。だから `streaming = false` は、どのみちレスポンスをバッファするプロキシ
のための逃げ道になります。

`bot_async_timeout` が `async_timeout` より大きいのは、bot 判定されたリクエストが
1バイトも返す前にすべての境界を待つうえ、インデクサはブラウザより長く待つから
です。ここのゼロは無制限ではなく `async_timeout` へのフォールバックです。打ち間違え
たキーが、リクエスト期限いっぱいクローラの接続を掴んだままにしてはいけません。
`bot_user_agents` の項目は組み込みのカタログに追加されるだけで、組み込みのトークンを
置き換えることはありません。

`live_` で始まるキーはすべて `streaming` に依存します。バッファされたドキュメントは
live な境界をその場で確定させ、配信が置き換えるプレースホルダを書かないからです。
`live = false` は障害ではなく負荷のつまみで、ドキュメントは有効なまま live な境界が
コミットした内容を保ち、接続を促されるクライアントもいません。それぞれの上限が何を
買っているかは[ライブレンダリング](/ja/guides/cross-layer/live-rendering/)を参照してください。

`update.validator_key` は、更新が有効なのに欠けていると起動時に拒否されます。鍵なし
のダイジェストを配らないためです。エントロピーの低い内容の鍵なしダイジェストは、
比較するだけで推測を確認できてしまいます。鍵素材が 32 バイトに満たない値も同じ理由で
拒否されます。推測できる鍵は、手間の増えた鍵なしダイジェストでしかありません。base64
の値はデコード後の長さで、それ以外はそのままのバイト数で測られ、セッションの
keyring.secret と同じ下限です。ローテーションは破壊的ではなく、比較が
外れて次の応答が完全なドキュメントになるだけです。大きすぎるマニフェストは拒否では
なく破棄されるので、`update.max_manifest_bytes` を超えたリクエストはエラーではなく
大きめの差分を払います。それぞれの経路が何を買うかは[部分更新](/ja/guides/cross-layer/partial-updates/)にあります。

`cache.enabled` はここで唯一、既定でオンです。オプトインはこのキーではなく
[`@cache`](/ja/reference/template-syntax/#cache) アノテーションのほうだからです。
保存したバイト列が新しい描画の代わりを務められないコンポーネントには、生成が
アノテーションを許しません。つまりアノテーションを持つテンプレートは検証済みで、
すでに要求済みです。アノテーションを書かないプロジェクトはストアに到達せず、何も
払いません。古い内容が残っている疑いを切り分けるときはオフにしてください。
`cache.max_entries` は1プロセスが抱える量の上限です。キーは宣言されたパラメータ
すべてを含むので、任意の文字列を取るコンポーネントは呼び出し元の数だけ項目を持ちます。

`scope: "private"` でキャッシュするものが出てきたら、この上限を上げてください。private
なキーはパラメータに加えて読み手の識別子を持つので、項目数が読み手の数だけ倍になります。
すべてが共有キーだった頃に決めた上限のままだと、再利用される前に追い出されます。

## `[cache]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `enabled` | `false` | 同じキーに対して取得結果を再利用する |

### `[[cache.stores]]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `name` | *(空)* | 呼び出し側がこのストアを指す名前 |
| `backend` | `"memory"` | エントリの置き場所。実装済みは memory のみ |
| `ttl` | `"1m"` | エントリが新鮮でいる時間 |
| `stale` | `"0s"` | 再取得が走る間、古いエントリが答え続けてよい時間。ゼロで無効 |
| `scope` | `"private"` | `private` は読み手ごとにエントリを分け、`public` は1つを共有する |
| `max_entries` | `1024` | このストアが保持する項目数。ゼロ以下は無制限 |
| `fetch_timeout` | `"30s"` | 待ち手から切り離して走る取得の上限 |

このストアが持つのはハンドラが**取得した**ものであり、上の `html.cache` とは別の問いに
答えます。あちらは描画済みのバイト列、こちらはそのバイト列の材料になったデータです。
別々に見積もるのは、埋まる速さも供給元も違うからで、1つの上限で両方を覆えば、忙しいほうが
もう一方を追い出します。

`html.cache.enabled` がオンなのにこちらが既定でオフなのは、オプトインの形が対称ではない
からです。コンポーネントは自分のアノテーションでキャッシュを要求するので、描画ストアは
オンのまま何もしないでいられます。データキャッシュに読むべきアノテーションはありません。
ストアの `Get` はハンドラの中のただの呼び出しで、このセクションをオンにすることだけが「保存して
よい」という唯一の宣言です。オフのままなら、どの呼び出しも自分の取得を走らせて返します。
コードを触らずにデプロイからキャッシュを取り下げる方法でもあります。

`scope` が `private` 既定なのは描画キャッシュと同じ理由です。読み手ごとの結果を `public` と
宣言すれば次に尋ねた人へ配られ、しかも何も報告されません。共有できる結果を `private` の
ままにした代償はヒット率だけです。[取得したデータのキャッシュ](/ja/guides/backend/data-cache/)
を参照してください。

`fetch_timeout` があるのは、同時のミスが1回の取得にまとめられ、その取得が待ち手全員から
切り離されて走るからです。誰のキャンセルもそれを終わらせられないので、自前の上限がないと、
応答しない上流1つが冷えたキーの数だけ goroutine を抱え込みます。

## `[security]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `headers.enabled` | `true` | 以下すべてが従うスイッチ |
| `headers.content_type_options` | `true` | `X-Content-Type-Options: nosniff` |
| `headers.frame_options` | `"deny"` | `X-Frame-Options` |
| `headers.referrer_policy` | `"strict-origin-when-cross-origin"` | `Referrer-Policy` |
| `headers.content_security_policy` | *(空)* | `Content-Security-Policy`（環境変数なし） |
| `headers.content_security_policy_report_only` | *(空)* | その report-only 版（環境変数なし） |
| `headers.permissions_policy` | *(空)* | `Permissions-Policy`（環境変数なし） |
| `headers.hsts.enabled` | `false` | `Strict-Transport-Security`。検証済み HTTPS のリクエストにのみ付く |
| `headers.hsts.max_age` | `"0s"` | |
| `headers.hsts.include_subdomains` | `false` | |
| `headers.hsts.preload` | `false` | |

HSTS が付くのは検証済みの HTTPS リクエストだけです。平文で送るのは、その接続では
保証できないポリシーの記憶をブラウザに頼むことになります。

### `[security.cors]`

| キー | 既定 | 意味 |
| --- | --- | --- |
| `cors.enabled` | `false` | 以下のキーすべてが従うスイッチ |
| `cors.include` | `["/**"]` | ポリシーが覆うパス。セグメント文法 |
| `cors.exclude` | `[]` | 覆わないパス。`include` より優先 |
| `cors.allowed_origins` | `[]` | 完全一致の `scheme://host[:port]`、または単独の `"*"` |
| `cors.allow_credentials` | `false` | Cookie 付きで送られたレスポンスをリストのオリジンに読ませるか |
| `cors.allowed_methods` | `["GET", "HEAD", "POST"]` | preflight が許可するメソッド |
| `cors.allowed_headers` | `["Content-Type", "Authorization"]` | preflight が許可するリクエストヘッダ |
| `cors.exposed_headers` | `["X-Request-ID", "Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"]` | スクリプトが読めるレスポンスヘッダ |
| `cors.max_age` | `"10m"` | ブラウザが1回の preflight をキャッシュしてよい時間 |

起動に失敗する組み合わせが4つあります。有効なのにオリジンが無い、`allow_credentials`
と `"*"`、`allow_credentials` と `allowed_headers` の `"*"`、`allow_credentials` と
`include` の `"/**"`。最初の3つはブラウザがレスポンスを捨てる設定で、最後はどのデプロイも
意図しないほど広い付与です。[クロスオリジンリクエスト](/ja/guides/backend/cors/)を参照。

`max_age` はブラウザ自身が上限を持つので — Safari は10分、Chrome は2時間 — それより
大きい値は尊重されず切り詰められます。生成された OpenAPI ドキュメントは、このセクションの
有無にかかわらずクロスオリジンで読めます。

## `[observability]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `minimum_level` | `"info"` | 重要度の下限。`trace`, `debug`, `info`, `warn`, `error`, `off` |
| `stdout_format` | `"json"` | 端末でのレコード表現。`json` または `plaintext` |
| `service_name` | *(空)* | `OTEL_SERVICE_NAME` からも読む |
| `resource_attributes` | `[]` | サービス名とともに報告する追加の `key=value` |
| `boot_log` | `"auto"` | 起動サマリ。`auto`, `tree`, `record`, `off` |

`auto` は対話的な端末ではツリーを、それ以外では構造化レコード1件を出します。
[設定サマリ](/ja/productivity/startup-summary/)を参照してください。

### `[observability.query]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `enabled` | `"auto"` | 生成された全ステートメントを記録する。`auto`, `on`, `off`。`auto` は `dev` で on |
| `level` | `"info"` | 通常のステートメントレコードの重要度 |
| `slow_threshold` | `"200ms"` | これを超えると slow 扱い。ゼロで slow 検出を無効化 |
| `slow_level` | `"warn"` | slow なステートメントレコードの重要度 |
| `bind_values` | `"auto"` | 引数の値を記録する。`auto`, `on`, `off` |
| `explain` | `true` | slow なステートメントの EXPLAIN（プランのみ）を取得する |
| `reproduction` | `true` | slow なステートメントの再実行スニペットを出す |
| `max_sql_length` | `4096` | 記録するステートメント本文の上限 |
| `max_value_length` | `256` | 記録する引数値ごとの上限 |

`auto` は設定を実行環境に結びつけます。開発中の実行は無設定で計測され、それ以外の
環境は誰かが明示的に有効化するまで黙ったままです。`explain` と `reproduction` が
依存するのは `enabled` ではなく `slow_threshold` です。しきい値をゼロにすると、
この3つが同時に止まります。
[スロークエリー診断](/ja/productivity/query-diagnostics/)を参照してください。

### `[observability.trace]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `enabled` | `"auto"` | フレームワークのスパンを開く: `auto`, `on`, `off`。`auto` はトレースを送出しているかどうかに従う |
| `render` | `true` | HTML レスポンスごとに1つ、初回ビルドを内側に持つスパン |
| `boundary` | `true` | 確定した非同期境界ごと、ライブ配信ごとのスパン |
| `database` | `true` | 実行された文ごとのクライアントスパン |
| `statement` | `true` | そのスパンに載る文のテキスト |
| `sampler` | *(環境で決まる)* | どのトレースを記録するか: `always_on`, `always_off`, `traceidratio`, または `parentbased_` 形 |
| `sampler_arg` | *(環境で決まる)* | sampler の引数。比率形なら残すトレースの割合 |

`auto` が読むのは環境ではなく送出のスイッチです。誰も送出しないスパンは、
コストだけを払うことになるからです。`on` はリクエストのルートスパンも設置する
ので、自前のトレーサプロバイダを持つプロジェクトはここでエンドポイントを設定
しなくても完全な木を得られます。

`boundary` は `render` に、`statement` は `database` に依存します。親を切ると
子も一緒に止まります。`statement` の長さは
`observability.query.max_sql_length` で制限され、これはクエリレコード上の同じ
テキストを制限するのと同じキーです。

`statement` が何であれ、バインド値がスパンに載ることはありません。値はクエリ
レコードのほうに残り、そのレコードはリクエストのルートではなく文のスパンを
名指します。[リクエストトレーシング](/ja/guides/architecture/telemetry/#リクエストトレースを読む)を参照して
ください。

`sampler` はこの節で唯一、固定の既定値を持ちません。`APP_ENV` が `dev` なら
`parentbased_always_on`、それ以外の環境なら——このフレームワークが知らない名前も
含めて——`parentbased_traceidratio` の `0.1` に解決されます。キーを設定すれば
どちらも置き換わり、どちらが効いたかは起動サマリが報告します。解釈できない引数は、
全記録へ戻るのではなくプロセスを停止します。
[どのトレースを残すか](/ja/guides/architecture/telemetry/#どのトレースを残すか)を参照してください。

### `[observability.metrics]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `enabled` | `"auto"` | フレームワークのメトリクスを記録する: `auto`, `on`, `off`。`auto` はメトリクスを送出しているかどうかに従う |
| `interval` | `"60s"` | 収集して送出する間隔 |
| `temporality` | `"delta"` | `delta` は区間ごと、`cumulative` はプロセス起動からの累計 |
| `http` | `true` | `http.server` のリクエスト所要時間、同時実行数、ボディサイズ |
| `db` | `true` | `db.client` の操作所要時間とコネクションプールの状態 |
| `runtime` | `true` | `go.*` のメモリ、goroutine、GC |
| `render` | `true` | `pw.render` の所要時間とバイト数、境界の確定、live配信 |
| `cache` | `true` | コンポーネント出力キャッシュとデータ結果キャッシュのヒットとミス |

`enabled` は `trace.enabled` と同じく送出のスイッチを読みますが、`trace.enabled`
とは独立です。メトリクスを on にしてトレースを off にする構成は、サンプリング比率を
下げたデプロイが行き着く先であり、ここの数はサンプリング比率で変わりません。

`interval` はこれらの計器のグラフの分解能そのものです。スパンのバッチが出ていく
間隔である `otel.flush_interval` とは別の数字になります。プラットフォームの
エージェントが `go.*` をすでに集めているなら `runtime` を切ってください。ほかの
グループに二重取得はありません。
[メトリクス](/ja/guides/architecture/telemetry/#メトリクス)を参照してください。

### `[observability.otel]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `enabled` | `false` | トレースとログをエクスポートする |
| `endpoint` | *(空)* | OTLP/HTTP のベース URL。`/v1/traces`、`/v1/logs`、`/v1/metrics` が付加される |
| `headers` | *(空)* | カンマ区切りの `key=value`。値がログに出ることはない |
| `request_timeout` | `"10s"` | エクスポート1回の上限 |
| `queue_size` | `2048` | メモリに保持するレコード数。満杯ならリクエストを止めずに捨てる |
| `max_export_size` | `512` | 1バッチの上限 |
| `flush_interval` | `"5s"` | 端数のバッチを送る間隔 |

これらの既定値は、ゼロ値に対してエクスポータが適用する上限をそのまま書き写した
ものです。生成されたファイルが「誰かに聞け」という意味のゼロではなく、プロセスの
実際の挙動を語るようにするためです。

`OTEL_EXPORTER_OTLP_TIMEOUT` は意図的に `request_timeout` へ**バインドしていません**。
標準の変数はミリ秒を数えますが、ここの期間はすべて Go の duration 文字列です。
ひとつのキーが両方を意味することはできません。

## `[session]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `enabled` | `false` | |
| `backend` | `"rdb"` | サーバに置くスロットが使うバックエンド: `rdb`, `cookie`, `redis`, `dynamo`, `firestore` |
| `retention` | `"720h"` | ストアがレコードを保持してよい期間。`[auth]` の寿命が短ければそちらが効く |
| `cookie.name` | `"pw_session"` | |
| `cookie.path` | `"/"` | |
| `cookie.domain` | *(空)* | |
| `cookie.secure` | `true` | `false` にしてよいのはループバックの開発時だけ。`dev` 以外では起動を拒否する |
| `cookie.http_only` | `true` | |
| `cookie.same_site` | `"lax"` | |
| `rdb.source` | `"middleware"` | `middleware` は `middleware.rdb` のプールを再利用し、`dedicated` は `session.rdb.dsn` を開く |
| `rdb.group` | *(空)* | セッションテーブルを持つ接続グループ。空なら `middleware.rdb.write_group` |
| `rdb.dsn` | *(空)* | 専用セッションデータベース。表示されるときにマスクされるのは資格情報だけ |
| `rdb.table` | `"popcornweb_session"` | |
| `redis.dsn` | *(空)* | `redis://` または `rediss://` のサーバー。表示されるときにマスクされるのは資格情報だけ |
| `redis.key_prefix` | `"pw:session:"` | セッションストアが使用するキーの名前空間 |
| `redis.connect_timeout` | `"5s"` | 起動時の ping と各コマンドの期限 |
| `cookie_store.name` | `"pw_session_data"` | 封をしたレコードを運ぶクッキー |
| `keyring.secret` | *(空)* | ブラウザが運ぶもの全部に署名し封をする base64 の秘密鍵（マスクされる） |
| `keyring.previous_secrets` | `[]` | ローテーション中も読める引退した秘密鍵（マスクされる） |
| `dynamo.table` | `"popcornweb_session"` | 宣言上のセッションテーブル名。実際の名前へは `middleware.dynamo` が対応づける |
| `dynamo.consistent_read` | `false` | セッションを強整合で読む。読み取り容量は倍 |
| `firestore.kind` | `"popcornweb_session"` | セッションレコードを保存する entity kind |

読まれるのは選んだバックエンドのキーだけです。`cookie` 以外のバックエンドは、それ自身の
blank import でバイナリに入ります。書き忘れたときは起動時のエラーが追加すべき行を引用
します。4つの比較と、それぞれに必要な設定は[セッションストレージ](/ja/guides/storage/session-storage/)に
あります。

このセクションは期間を1つも宣言しません。有効期限は身元の証明がどれだけ有効かを述べるもの
なので、`session.ttl`、`session.idle_timeout`、`session.renewal_interval` は `[auth]` の下に
あります。

ブラウザにあるトークンはどのバックエンドでも不透明なので、それ自体に署名はしません。
CSRF の秘密もここの鍵ではありません。登録されたセッションスロットなので、同じ keyring が
封をし、`security.csrf` は自前の秘密を持ちません。

`keyring.secret` が守るのはトークンの隣を運ばれるものです。`session.ReadOnly` のスロットには
署名、`session.Private` のスロットには封。どちらも同じ1つの秘密から導きます。だから
`backend` が何であれ、全スロットが `session.Shared` か `session.RequestScope` でない限り必須です。private なスロットは
訪問者が匿名のあいだ封をしたクッキーに載るからです。`pw init` が `config.dev.toml` に生成し、
それ以外の環境は `SESSION_KEYRING_SECRET` を読みます。

## `[ratelimit]`

| キー | デフォルト | 意味 |
| --- | --- | --- |
| `enabled` | `false` | |
| `backend` | `"memory"` | カウンターの置き場所: `memory` または `redis` |
| `window` | `"1m"` | 以下の全カウントの計測期間。`X-RateLimit-Reset` が報告する時刻でもある |
| `per_subject` | `600` | 認証済みサブジェクト 1 つがウィンドウ内に送れるリクエスト数。`0` でこのバケットを無効化 |
| `per_address` | `300` | セッションのない呼び出し元 1 つがウィンドウ内に送れるリクエスト数。正の値が必須 |
| `process` | `0` | ウィンドウあたりの総到着数の上限、キーなし。`0` なら身元バケットだけが残る |
| `redis.dsn` | *(空)* | `redis://` または `rediss://` のカウンターサーバー。報告時は資格情報部分だけマスクされる |
| `redis.key_prefix` | `"pw:ratelimit:"` | このリミッターが使用するキーの名前空間 |
| `redis.connect_timeout` | `"5s"` | 起動時 ping とコマンドごとのデッドライン |

`redis.*` のキーが読まれるのは `backend = "redis"` のときだけで、このバックエンドは
`ratelimitstore/redis` のブランク import でバイナリに届きます — 起動エラーが追加すべき
行を提示します。`memory` バックエンドのまま `redis.dsn` を設定すると、無視ではなく
拒否になります。`backend = "redis"` で DSN がない場合も拒否です。正の `process` は
`per_address` と `per_subject` 以上でなければなりません。1 人に全体より多くを許す
設定は、決して効かない制限の記述だからです。

カウントにルート単位の形はありません。予算 1 つがアプリケーション全体を覆い、
フレームワークの運用エンドポイントと公開アセットのマウントは、変更するキーなしで
除外されます。3 つのカウントがどう組み合わさるか、ストアに到達できないとき何が
起きるかは[レートリミット](/ja/guides/backend/rate-limiting/)が説明します。

## `[auth]`

これらのキーが存在するのは、`plugin/auth` がバイナリにリンクされているときだけ
です。アカウントリゾルバを登録すると、そうなります。認証まわりを何もインポートして
いないアプリケーションには、設定すべき `[auth]` prefix そのものがありません。

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `enabled` | `false` | |
| `backend` | `"rdb"` | ceremony、許可リスト、credential、bootstrap の保存先: `rdb`, `dynamo`, `firestore` |
| `mode` | `"oidc_only"` | ブラウザ用の各モードと、Bearer API 用の `jwt_only` |
| `login_path` | `"/auth/login"` | プロバイダのフローを開始する |
| `callback_path` | `"/auth/callback"` | 結果を検証してセッションを開始する |
| `logout_path` | `"/auth/logout"` | セッションを終了する。`POST` のみ |
| `post_login_path` | `"/"` | ログイン完了後に着地するローカルパス |
| `session.ttl` | `"24h"` | セッションの絶対寿命 |
| `session.idle_timeout` | `"0s"` | 無操作での失効。ゼロで無効 |
| `session.renewal_interval` | `"0s"` | 無操作失効の更新間隔の下限 |
| `protection.include` | `[]` | 認証を必要とするパスパターン |
| `protection.exclude` | `[]` | 公開のままにするパスパターン |
| `protection.unauthenticated` | `"redirect"` | `redirect` または `unauthorized` |

### `[auth.oidc]`

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `issuer` | *(空)* | `AUTH_OIDC_ISSUER` |
| `client_id` | *(空)* | `AUTH_OIDC_CLIENT_ID` |
| `client_secret` | *(空)* | `AUTH_OIDC_CLIENT_SECRET`（起動サマリではマスクされる） |
| `redirect_url` | *(空)* | `AUTH_OIDC_REDIRECT_URL`。`allow_loopback_http` とループバック `Host` の組み合わせに限り、空またはルートパスをリクエスト origin から導出。デプロイでは絶対 URL が必要 |
| `scopes` | `[]` | |
| `identity_claim` | `"sub"` | ローカルアカウントを識別する検証済みクレーム |
| `admission` | `"authenticated"` | `authenticated`, `claim`, `registered`, `existing` |
| `auto_provision` | `true` | 未知の検証済み identity にリゾルバ経由でのアカウント作成を許す |
| `claim.path` | *(空)* | 検証済みクレームへの JSON Pointer。`admission = "claim"` 用 |
| `claim.values` | `[]` | 受け入れる値 |
| `claim.match` | `"any"` | `any` または `all` |
| `registered_claims` | `[]` | 許可リストと突き合わせるクレーム。既定は `identity_claim` |
| `logout_scope` | `"reconfirm"` | `reconfirm` はローカルのセッションを破棄し、次の認可リクエストに `prompt` を載せる。`global` は加えて `end_session_endpoint` でプロバイダのセッションも終了する |
| `allow_global_logout_request` | `false` | 同一オリジンのログアウトフォームが `scope=global` を POST して `global` へ引き上げることを許す。引き上げしかできない |
| `allow_loopback_http` | `false` | 開発時に `http` のループバック issuer と、リクエストから導出するループバック redirect を許可する |

ローカルだけ、というスコープは意図的にありません。ローカルのセッションだけを破棄すると
次のログインが無言で通るので、サインアウトが何もしなかったように読めます。`reconfirm` は
まさにそれを直すために存在します。`auth.shared_device` は `global` を要求し、そもそも
プロバイダのセッションに届かないモード（`passkey_only`、`oauth_only`）は、何もしないキーとして
バインドするのではなく、明示的に書かれた `logout_scope` を拒否します。かつてこれを綴っていた
`provider_logout` は削除済みで、まだ `true` を持つ設定は `logout_scope` を名指しして起動時に
拒否されます。

`identity_claim` はアカウントとの結びつきそのものになるため、そこに指定する値は
アカウントの生涯にわたって安定し、かつ issuer の中で一意でなければなりません。
再発行や使い回しがあれば、ある人に別人のアカウントを渡すことになります。利用者を
事前に用意しておくデプロイでは subject をまだ知りようがないことが多く、社員番号の
ような自前のディレクトリ識別子をここに指すのが普通です。

有効な OIDC モードで `issuer`、`client_id`、`client_secret` のいずれかが空なら、
最初のログイン時ではなく起動時に失敗します。エラーは不足しているキーと、その環境
変数の両方を名指しします。ローカルのエミュレータ向けに生成されたプロジェクトが
プロバイダの値を一切持たないのはそのためで、[`pw dev`](/ja/pw/project/dev/) が
注入します。[認証の組み込み](/ja/guides/backend/authentication/)を参照してください。

### `[auth.oauth]`

`auth.mode = "oauth_only"` が読むキーです。人を認証しながら ID トークンを発行しない
プロバイダを通したログインで、`provider` が組み込みの定義を選びます。認可・トークン・
アカウントの各エンドポイント、最小スコープ、claim 名はその定義が供給するので、デプロイ側で
設定するものはありません。

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `provider` | *(空)* | **必須**。定義があるのは `x` だけ |
| `client_id` | *(空)* | **必須**。`AUTH_OAUTH_CLIENT_ID` |
| `client_secret` | *(空)* | **必須**。`AUTH_OAUTH_CLIENT_SECRET`（起動サマリではマスクされる） |
| `redirect_url` | *(空)* | デプロイ先の絶対 URL。`allow_loopback_http` とループバック `Host` の組み合わせに限り、空またはルートパスをリクエスト origin から導出 |
| `scopes` | `[]` | プロバイダ最小のログインスコープに足すのではなく置き換える。1 エントリ 1 スコープトークン |
| `identity_claim` | `"sub"` | ローカルアカウントを識別するプロフィールクレーム。プロバイダが報告しない名前は拒否される |
| `admission` | `"authenticated"` | `authenticated`, `claim`, `registered`, `existing` |
| `auto_provision` | `true` | 未知のプロフィールにリゾルバ経由でのアカウント作成を許す |
| `claim.path` | *(空)* | プロフィールクレームへの JSON Pointer。`admission = "claim"` 用 |
| `claim.values` | `[]` | 受け入れる値 |
| `claim.match` | `"any"` | `any` または `all` |
| `registered_claims` | `[]` | 許可リストと突き合わせるクレーム。既定は `identity_claim` |
| `allow_loopback_http` | `false` | 開発時に、リクエストから導出するループバック redirect を許可する |

`x` プロバイダが報告するのは、X のユーザー ID である `sub` に加えて
`preferred_username`、`name`、`picture` です。X 自身の綴りは取り込む段で改名されるため、
`identity_claim = "username"` は毎回のログインで拒否されるのではなく、報告される名前を
列挙したうえで起動時に拒否されます。

`identity_claim` はアカウントとの結びつきそのものになり、既定ではプロバイダ自身の識別子を
指します。ここをハンドルに向けると、プロバイダが改名と手放しを許している値にアカウントを
結び付けることになり、いずれある人に別人のアカウントを渡します。

このモードは `auth.shared_device` と、明示的に書かれた `auth.oidc.logout_scope` を拒否し、
その場での確認を要求するガードには `503` を返します。全体サインアウトも検証可能な再証明も、
プレーンな OAuth には無いためです。
[X でログインする](/ja/guides/backend/authentication/#x-でログインする)を参照してください。

### `[auth.jwt]`

これらのキーを読むのは `auth.mode = "jwt_only"` です。Bearer アクセストークンを
リクエストごとに検証し、ブラウザのセッションも認証エンドポイントも作りません。

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `issuer` | *(空)* | **必須**。`iss` と完全一致。`AUTH_JWT_ISSUER` からも読む |
| `audience` | `[]` | **必須**。この API を表す `aud` の値 |
| `audience_match` | `"any"` | 設定した audience の `any` または `all` を要求する |
| `algorithms` | `[]` | **必須**。`["RS256"]` など RSA 検証アルゴリズムの許可リスト |
| `required_token_type` | `"at+jwt"` | 要求する `typ`。空なら、型がないトークンを明示的に許可する |
| `required_scopes` | `[]` | すべてのトークンに要求する scope |
| `discovery` | `"oidc"` | 鍵の取得方法。`oidc`, `oauth`, `manual` |
| `jwks_uri` | *(空)* | `manual` では**必須**。issuer と同一オリジンであること |
| `leeway` | `"30s"` | 時計ずれの許容幅。上限 5 分 |
| `max_token_lifetime` | *(空)* | **必須**。`exp - iat` の上限。最大 24 時間 |
| `max_token_bytes` | `8192` | compact token のサイズ上限。最大 64 KiB |
| `jwks_refresh_cooldown` | `"1m"` | 未知の `kid` による鍵の再取得を行う最短間隔 |
| `allow_loopback_http` | `false` | 開発時に HTTP のループバック issuer を許可する |
| `identity_claim` | `"sub"` | ローカルアカウントを識別する検証済みクレーム |
| `admission` | *(空)* | **必須**。`authenticated`, `claim`, `registered`, `existing` |
| `auto_provision` | `false` | 未知の検証済み identity にアカウント作成を許可する |
| `claim.path` | *(空)* | `admission = "claim"` で使う検証済みクレームへの JSON Pointer |
| `claim.values` | `[]` | その位置で受け入れる値 |
| `claim.match` | `"any"` | `any` または `all` |
| `registered_claims` | `[]` | 許可リストと比較するクレーム。既定は `identity_claim` |
| `revocation.mode` | *(空)* | **必須**。`off`, `token`, `subject`, `both` |
| `revocation.on_unavailable` | `"refuse"` | 失効ストアが応答できないとき `refuse` または `admit` |
| `revocation.max_propagation_delay` | `"0s"` | 失効結果のキャッシュ期間。ゼロならキャッシュしない |
| `dev.trust_unverified_tokens` | `false` | `pw dev` かつ loopback 専用。staging と production では拒否 |

`registered` admission と `off` 以外の失効モードには、リレーショナルな auth テーブルと
`middleware.rdb` が必要です。それ以外の admission は状態を持たずに使えます。
`protection.unauthenticated` は `unauthorized`、`security.csrf.enabled` は false にします。
このモードには、CSRF の検査に使うセッション秘密がないためです。
[JWT-only の API サーバー](/ja/guides/backend/authentication/#jwt-only-の-api-サーバー)を
参照してください。

## 設定したのに起動サマリに出ないキー

上のキーの多くは、親のスイッチに従います。`server.api_doc_path` は
`server.api_doc` に、`[auth.oidc]` のすべては `auth.enabled` に、
`observability.otel.endpoint` は `otel.enabled` に依存します。

親が空か false のとき、サマリは従属するキーを省き、親のほうは残します。従属キーが
消えた理由が親だからであり、何も決めていないキーを7つ並べれば、実際に効いている
1つが埋もれるからです。バインド自体には影響しません。値は読まれていて、ただ作用
する先がないだけです。つまり、設定したのにサマリに見つからないキーは、綴りではなく
親についての問いだというわけです。

## そのバイナリが実際に持つ一覧

ここの表が扱うのはフレームワークと認証プラグインです。あなたのビルドには自分の
パッケージが登録したものも入っており、その和集合を知っているのはバイナリだけです。

```sh
./myapp --generate-config toml > config.dev.toml
./myapp --generate-config env > .env.example
```

ひな形はそのビルドに存在する登録から組み立てられるので、そのバイナリにとっては
これが正典です。自分の `[app]` prefix は含まれ、インポートしていないフレームワーク
機能は含まれません。構造体を足してコマンドを再実行すれば新しいキーが現れます。
このページに手を入れる必要はありません。
[カスタムコマンド](/ja/guides/architecture/custom-commands/)を参照してください。
