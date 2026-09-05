---
title: 認証の組み込み
description: ブラウザのログインを構成するか、API サーバーで Bearer アクセストークンを検証する。
sidebar:
  order: 1
---

ブラウザのログインには、ルート、セッション解決、一連のプロトコルコードが伴います。
API サーバーが解く問題は別です。リクエストごとに Bearer トークンを受け取り、issuer、
audience、署名、有効期間、アクセス規則を検証してからハンドラへ渡します。Popcorn Web は
両方を扱います。アプリケーションがプロトコルのエンドポイントや検証ミドルウェアを
繰り返し実装する必要はありません。

ここでは設定キー、エンドポイント、認証フロー、保存先を説明します。モードの選び方と、
ログイン後のセッションに許可する操作については
[認証の設計](/ja/guides/backend/authentication-design/)を参照してください。

## 有効にする

エントリポイントと設定ファイル、2つです。

```go
// cmd/myapp/main.go — アカウントリゾルバの登録が plugin/auth の import を
// 兼ねます。エンドポイントとセッション解決はその拡張が担当します。ストレージの
// import 2つは SQLite のもの——セッションと、ログインの儀式が使い切る単回限りの
// レコードです。
import (
	_ "github.com/shibukawa/popcornweb/authstate/sqlite"
	_ "github.com/shibukawa/popcornweb/sessionstore/sqlite"
)

func main() {
	handlers.RegisterAccounts()
	if err := pw.Run(context.Background(), handlers.Handlers()); err != nil {
		log.Fatal(err)
	}
}
```

ストレージの import が `plugin/auth` と別なのは意図的です。auth プラグインは
バックエンドを一切リンクしないので、アプリケーションは設定したものだけを持ちます。
SQL ストアはエンジンごとに別パッケージなので、PostgreSQL に移るときは
`sessionstore/postgres` と `authstate/postgres` を import します。
`pw init --auth=oidc --db=postgres` はその行を書き出します。

```toml
# config.dev.toml
[session]
enabled = true
backend = "rdb"          # "cookie"、"redis"、"dynamo"、"firestore" も選べる

[auth]
enabled = true
backend = "rdb"          # "dynamo" または "firestore" も選べる
mode = "oidc_only"

[auth.oidc]
issuer = "https://issuer.example"
client_id = "..."
client_secret = "..."
redirect_url = "https://app.example/auth/callback"
identity_claim = "sub"   # アカウントを識別する検証済み claim
logout_scope = "reconfirm"  # ログアウトがプロバイダのセッションに何をするか
```

`pw init --auth=oidc` は両方を書き出します。リレーショナルバックエンドを選んだ場合は、
フレームワークのテーブルを作るマイグレーションも追加します。DynamoDB と Firestore は
リレーショナルマイグレーションを使いません。必要なデプロイ設定は各ストレージのガイドで
説明します。

### ログインが始まる前に満たしておくもの

4つあります。どれもサインインの途中で判明するのではなく、起動時に検査されます。

- `session.enabled = true`。そうでないとログインの着地先がありません。どのバックエンドが
  それを持つかは別の判断です（[セッションストレージ](/ja/guides/storage/session-storage/)）。
- `auth.backend` が指定するバックエンドがリンクされ、到達できること。`rdb` は
  `middleware.rdb`、`dynamo` は `middleware.dynamo`、`firestore` は
  `middleware.firestore` を必要とします。`session.backend` とは別の選択です。
- バックエンドのデプロイ用リソースが準備されていること。リレーショナルストレージには
  マイグレーション、DynamoDB にはテーブル、Firestore には Datastore-mode データベースと
  必要な TTL ポリシー・インデックスが要ります。
- `issuer`、`client_id`、`client_secret` がすべて非空。ループバック開発以外では、
  `redirect_url` も絶対 URL である必要があります。

issuer は `https` である必要があります。例外はループバックの開発用プロバイダだけで、
`auth.oidc.allow_loopback_http = true` を要求します。それ以外の場所でこのフラグを立てては
いけません。

## 設定できるものすべて

`[auth]` のキーは、フレームワークが何をマウントし、何を保護するかを決めます。

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `enabled` | `false` | true のときだけエンドポイントとガードが存在する |
| `backend` | `"rdb"` | ceremony、許可リスト、credential、bootstrap の保存先: `rdb`, `dynamo`, `firestore` |
| `mode` | `"oidc_only"` | `oidc_only`, `oidc_passkey`, `passkey_only`, `oauth_only`, API 向けの `jwt_only`（[モード](#モード)） |
| `login_path` | `"/auth/login"` | プロバイダへの入口。ルート相対 |
| `callback_path` | `"/auth/callback"` | プロバイダが戻ってくる先。ルート相対 |
| `logout_path` | `"/auth/logout"` | ルート相対。`POST` のみ |
| `post_login_path` | `"/"` | 行き先の指定がないログインの着地先 |
| `protection.include` | `[]` | 認証を要求するパスのパターン |
| `protection.exclude` | `[]` | `include` から除外するパターン |
| `protection.unauthenticated` | `"redirect"` | ログインへ `redirect` するか、`401` の `unauthorized` か |

各パスはルート相対であることと `//` を含まないことが起動前に検証されます。打ち間違いは
「誰も到達できないルート」ではなく起動エラーになります。

`[auth.oidc]` のキーは、リライングパーティの登録内容と、誰を通すかを決めます。

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `issuer` | *(空)* | **必須**。`allow_loopback_http` でなければ `https` |
| `client_id` | *(空)* | **必須** |
| `client_secret` | *(空)* | **必須**。起動サマリではマスクされる |
| `redirect_url` | *(空)* | デプロイではコールバックの絶対 URL。ループバック開発では、空ならリクエストの origin と `callback_path`、ルートパスなら origin とそのパスから導出 |
| `scopes` | `[]` | `openid` に加えるスコープ |
| `identity_claim` | `"sub"` | ローカルアカウントを識別する検証済み claim |
| `admission` | `"authenticated"` | `authenticated` / `claim` / `registered` / `existing` |
| `auto_provision` | `true` | 未知の検証済み identity にアカウントを作らせる |
| `claim.path` | *(空)* | 検証済み claim への JSON Pointer。`admission = "claim"` で必須 |
| `claim.values` | `[]` | その位置で受け入れる値 |
| `claim.match` | `"any"` | `any` または `all` |
| `registered_claims` | *(空)* | 許可リストと突き合わせる claim。既定は `identity_claim` |
| `logout_scope` | `"reconfirm"` | ログアウトがプロバイダのセッションに何をするか。`reconfirm` または `global` |
| `allow_global_logout_request` | `false` | ログアウトフォームからの `global` への引き上げを許す |
| `allow_loopback_http` | `false` | 開発時に `http` のループバック issuer を許す |

3つの秘密は `AUTH_OIDC_ISSUER`、`AUTH_OIDC_CLIENT_ID`、`AUTH_OIDC_CLIENT_SECRET` から、
あるいはファイル内の `${NAME}` 参照から与えます。どちらでも同じ値に届きます。どちらも
コミットするものではありません。

## 誰を通すか

検証済みの identity は、まだ認可された identity ではありません。その2つを分けるのが
`admission` です。

| `admission` | 通る相手 |
| --- | --- |
| `authenticated` | issuer が検証した全員 |
| `claim` | `claim.path` の値が `claim.values` に一致する identity |
| `registered` | 事前に許可リストのテーブルへ登録された identity |
| `existing` | アカウントリゾルバがすでに知っている identity のみ。`auto_provision = false` が必要 |

`claim` は、答えをすでにディレクトリが持っている場合の規則です。グループ、ロール、部署。
`claim.match = "all"` は列挙した値すべてを要求するのでグループの積に、`any` は最初の一致で
通します。

`registered` は、初回ログインより前に利用者が分かっている閉じたデプロイの規則です。許可
リストのテーブルは issuer と claim 名と期待値を取ります。`registered_claims` があるのは
このためで、社員番号で事前登録する運用なら、まだ知りようのない subject ではなくその claim を
突き合わせます。

どの規則で通ったとしても、アカウントとの結び付きは issuer と `identity_claim` が指す claim の
組です。メールアドレスではありません。プロバイダはそれを再割り当てします。`identity_claim` を
変えるのは、アカウントの生涯にわたって安定かつ一意だとディレクトリが保証する値に対してだけに
してください。

## X でログインする

X は OAuth 2.0 で人を認証し、ID トークンは発行しません。GitHub も、業界がソーシャル
ログインと呼んでいるものの大半もそうです。`oidc_only` はここでは手が出せません。署名された
「この人はこの人である」という主張を検証するためのモードなのに、これらのプロバイダはそれを
送ってこないからです。`auth.mode = "oauth_only"` がそのログインです。

書くのはプロバイダ名ひとつです。エンドポイントも、スコープも、アカウント応答の形も、
すべてそこから決まります。

```toml
[session]
enabled = true
backend = "rdb"

[auth]
enabled = true
backend = "rdb"
mode = "oauth_only"
protection.include = ["/mypage"]

[auth.oauth]
provider = "x"
redirect_url = "https://app.example/auth/callback"
```

資格情報は `AUTH_OAUTH_CLIENT_ID` と `AUTH_OAUTH_CLIENT_SECRET` で渡します。X の開発者
ポータル側にも、同じコールバック URL を登録しておきます。ストレージの import とエントリ
ポイントは[有効にする](#有効にする)のものがそのまま使えます。エンドポイントもガードも
セッションも、ブラウザ用のどのモードでも同じものだからです。

違うのはコールバックの中の 1 ステップだけで、あとはそこから派生します。OIDC がトークンを
検証する位置で、このモードは X に「このアクセストークンは誰のものか」を尋ねます。
`/2/users/me` へのリクエストが 1 本、トークンだけを載せて飛びます。

返ってくる答えには署名がありません。裏付けになっているのは、そのトークンがこのデプロイ自身が
始めた PKCE 付きの交換から出てきたこと、そして応答が TLS 越しに X から届いたことです。
ここから先の admission、アカウントリゾルバ、セッションのローテーション、着地パスは、
ID トークンを検証したあとと完全に同じ経路を通ります。

アクセストークンはその場で捨てます。ログインが持っていた唯一の問いには答え終わったので、
保存もしませんし、`offline.access` スコープも要求しません。できあがるセッションは X への
資格情報を一切持ちません。ユーザーの代理でポストしたいアプリケーションは、自分で認可
フローを回して、そのトークンを自分で持ちます。

### X のアカウントを読む

セッションには、ローカルのアカウントと並んでプロバイダ側のアカウントが載ります。

```go
func home(w http.ResponseWriter, r *http.Request) {
	user, signedIn := auth.User(r.Context())
	if signedIn && user.Provider == "x" {
		// user.Subject   — X のユーザー ID。アカウントとの結び付きはこれ
		// user.Username  — @ を除いたハンドル
		// user.AvatarURL — プロフィール画像
		// user.DisplayName
	}
}
```

`Username` と `AvatarURL` はログイン時点の写しです。ユーザーが改名した瞬間に古くなります。
挨拶に使うぶんには構いませんが、一致し続けることが要るものには使えません。`DisplayName` と
`Email` は OIDC ログインのあととまったく同じくアカウントリゾルバが返すもので、既定の
リゾルバはプロバイダの表示名を読みます。X はメールアドレスを返しません。

結び付けに使うのは `Subject`、つまり数値の ID です。ハンドルのほうが読めるので惹かれますが、
そちらは安全ではありません。X はハンドルの改名を許し、手放されたハンドルを別人が取ることも
許しています。`@example` に紐づけたアカウントは、いずれ次に `@example` を持つ人のものに
なります。`identity_claim` の既定値が `sub` なのはこのためです。プロバイダが報告しない
claim 名 — たとえば `preferred_username` ではなく X 自身の綴りの `username` — は、
毎回のログインで拒否されるのではなく、報告される名前を列挙したうえで起動時に拒否されます。

### `[auth.oauth]` のキー

| キー | 既定値 | 意味 |
| --- | --- | --- |
| `provider` | *(空)* | **必須**。定義があるのは今のところ `x` だけ |
| `client_id` | *(空)* | **必須**。`AUTH_OAUTH_CLIENT_ID` |
| `client_secret` | *(空)* | **必須**。`AUTH_OAUTH_CLIENT_SECRET`。起動サマリではマスクされる |
| `redirect_url` | *(空)* | デプロイ先の絶対 URL。ループバック開発では、空にするとリクエストの origin から `callback_path` を導出する |
| `scopes` | `[]` | プロバイダ最小のログインスコープに足すのではなく置き換える。1 エントリ 1 スコープ |
| `identity_claim` | `"sub"` | ローカルアカウントを識別する claim。プロバイダが報告するものに限る |
| `admission` | `"authenticated"` | `authenticated`, `claim`, `registered`, `existing`。[誰を通すか](#誰を通すか)と同じ |
| `auto_provision` | `true` | 未知のプロフィールにアカウントを作らせる |
| `claim.path` | *(空)* | プロフィール claim への JSON Pointer。`admission = "claim"` で必須 |
| `claim.values` | `[]` | その位置で受け入れる値 |
| `claim.match` | `"any"` | `any` または `all` |
| `registered_claims` | *(空)* | 許可リストと突き合わせる claim。既定は `identity_claim` |
| `allow_loopback_http` | `false` | 開発中、ループバックでリクエスト相対の redirect URL を許す |

X が報告する claim は `sub`、`preferred_username`、`name`、`picture` の 4 つです。
X 自身の綴りである `id`、`username`、`profile_image_url` は取り込む段で改名されます。
1 つの値に 1 つの名前だけを残しておかないと、admission を設定する先が 2 つになるからです。

`scopes` を空にしておくと `users.read tweet.read` を要求します。`/2/users/me` に必要なのは
これだけで、これ以上は要りません。キーを書いた時点でリストの責任はデプロイ側に移ります。
1 エントリが 1 スコープなので、`["users.read tweet.read"]` は不正な 1 スコープです。
起動時にそう言われます。

### このモードが諦めているもの

プロトコルに表現手段がないために、3 つのものがありません。いずれも黙って劣化するのではなく、
音を立てて失敗します。

**サインアウトはローカルまで。** end session エンドポイントがないので、`logout_path` は
こちら側のセッションを破棄してそこで止まります。`auth.oidc.logout_scope = "global"` は、
何もしないキーとしてバインドされるのではなく起動時に拒否されます。X にはサインインしたまま
残り、次のログインはそのセッションから何も聞かれずに答えられることがあります。個人の端末ならそれが期待どおりです。
共有端末では情報の露出です。`auth.shared_device` がこのモードで中途半端に受理されるのではなく
拒否されるのはそのためで、あのモードが約束しているのは全体サインアウトと、プロバイダが黙って
答えられないログインの組であり、ここにはどちらもありません。

**再認証によるステップアップは動きません。** 同じ人をもう一度証明させるとは、`max_age` を
送って検証済みの `auth_time` を受け取ることです。ID トークンを出さないプロバイダはどちらも
報告しません。`auth.Confirmed` とゼロ幅のウィンドウは、収束しようのないログインへリダイレクト
する代わりに `503` を返し、モード名をログに出します。ログイン時刻から測る `auth.MaxAge` は
通常どおり働きます。機微なページに鮮度のウィンドウをかけるのはこのモードでもできて、
その場での確認が要る操作は別のモードに置く、という切り分けになります。

**ここには署名がありません。** 信頼の根拠はトランスポートです。ソーシャルログインとしては
それが普通のあり方で、ID トークンより弱いことに変わりはありません。誰が認証したかの暗号的な
証明が要るなら、要るのは OIDC のプロバイダです。

## JWT-only の API サーバー

`auth.mode = "jwt_only"` は、リソースサーバーのためのモードです。
`Authorization: Bearer …` で届いたアクセストークンをリクエストごとに検証し、確認できた
呼び出し元をリクエストコンテキストへ記録します。ログイン、コールバック、ログアウトの
エンドポイントは作りません。セッションも cookie もありません。ブラウザのログインと
Bearer API では信頼モデルが異なるため、この 2 つを同じモードで混在させない設計です。

`jwt_only` は、ブラウザログインを選ぶ `pw init --auth` の候補には含まれません。
API サーバーとして生成する場合は、専用プリセットを使います。

```sh
pw init myapi --preset=api-server
```

既存のプロジェクトへ追加する場合は、アカウントリゾルバを通じて `plugin/auth` をリンクし、
設定を手で追加します。サーバー側に状態を持たない最小構成は次のとおりです。

```toml
[auth]
enabled = true
mode = "jwt_only"
protection.include = ["/api/**"]
protection.unauthenticated = "unauthorized"

[auth.jwt]
issuer = "https://issuer.example"
audience = ["orders-api"]
algorithms = ["RS256"]
admission = "authenticated"
identity_claim = "sub"
max_token_lifetime = "1h"
revocation.mode = "off"
```

issuer、audience、許可するアルゴリズム、admission、有効期間の上限、失効モードには、
寛容な既定値を置いていません。ひとつでも欠ければ起動時に拒否します。鍵の discovery は
issuer の OpenID Connect metadata が既定です。`discovery = "oauth"` なら authorization
server metadata、`manual` なら issuer と同一オリジンの `jwks_uri` を使います。

リクエストを通す前に、設定したアルゴリズムの許可リストと取得した鍵で署名を検証し、
続いて `iss`、`aud`、`exp`、`iat`、`sub`、トークン種別、有効期間、必須 scope を
検査します。既定のトークン種別は `at+jwt` です。同じ issuer が同じ鍵で署名した
ID Token を、アクセストークンとして再利用させないためです。拒否理由にかかわらず、応答は
同じ `401` の problem と `WWW-Authenticate: Bearer` ヘッダになります。

ハンドラからは、型付きの principal とプロトコルに依存しない認証結果のどちらも読めます。

```go
caller, ok := auth.Bearer(r.Context())
if !ok {
	return // 保護していないルートなら、匿名リクエストもここへ到達できる
}

accountID := caller.AccountID
claims := caller.Identity.Claims
authentication := pw.RequestAuthentication(r)
```

`admission = "authenticated"` はサーバー側のストアを使いません。`claim` も検証済み
クレームだけで判定するため、状態を持たずに使えます。`registered` はリレーショナルな
許可リストを読み、`off` 以外の失効モードはリレーショナルな失効テーブルを読みます。
この 2 つは `middleware.rdb` とフレームワークのマイグレーションが必要です。
トークンを有効期限より前に終わらせる — そしてインシデント中にそれを取り消す — のは、
後述の[Bearer トークンを失効させる](#bearer-トークンを失効させる)です。
JWT-only にセッションストレージは要りません。また、権限はブラウザが自動送信しない
明示的なヘッダで届き、検査するセッション秘密も存在しないため、CSRF 保護は無効にします。

## ブラウザログインのエンドポイント

| パス | メソッド | 動作 |
| --- | --- | --- |
| `auth.login_path`（`/auth/login`） | GET | プロバイダへリダイレクトする |
| `auth.callback_path`（`/auth/callback`） | GET | 結果を検証してセッションを開始する |
| `auth.logout_path`（`/auth/logout`） | POST | セッションを終了する |

`auth.protection.include` に列挙したパスだけがセッションを必要とし、それ以外は公開の
ままです。未認証のリクエストはログインを経由して元のパスに戻され、
`auth.protection.unauthenticated = "unauthorized"` なら `401` を返します。

**ログアウトは POST のみです。** リンクやブラウザのプリフェッチでセッションを
終了できると、サインアウトが DoS の入口になります。そのため `GET` には `405` を返し、
操作にはフォームを使います。

```html
<form method="post" action={logoutPath}>
  <button type="submit">Sign out</button>
</form>
```

クロスオリジンのログアウトは拒否されます。セッションクッキーは `SameSite=Lax` で、
さらにエンドポイント側でも `Origin` の不一致を弾きます。

ローカルのセッションを終わらせることと、**プロバイダ側のセッション**を終わらせることは
別の行為です。ログアウトがどこまで届くかは `auth.oidc.logout_scope` が決めます。
「ローカルだけ」は選択肢にありません。上流ではログインしたままなので、次のログインで
同じアカウントが何も聞かれずに返り、サインアウトが効かなかったように見えるからです。

既定の `reconfirm` は、ローカルのセッションを破棄して、プロバイダには何も送りません。
代わりに変えるのは**次の**認可リクエストです。`prompt` を載せるので、プロバイダはこの
ログアウトが届かなかったシングルサインオンのセッションから黙って答えるのではなく、
アカウントピッカーを見せて証明を要求することになります。リライングパーティが自分の
ぶんだけプロバイダのセッションを終わらせる要求は、どの仕様にも存在しません。だから
これが、アプリケーション単位のサインアウトに一番近いものです。

`global` は加えて、プロバイダの RP-initiated logout を経由します。そのプロバイダを
共有する全アプリケーションからサインアウトさせる動きです。

```
POST /auth/logout
  → 303 https://issuer.example/end_session?client_id=…&post_logout_redirect_uri=…
  → 302 ログアウト後のページへ
```

サインアウトが「全部から出る」を意味するべき場合 — 共有端末や、そのプロバイダが
この 1 アプリケーションのためだけにある場合 — を除いて `reconfirm` を選びます。
`auth.shared_device` が `global` を要求するのはそのためです。`end_session_endpoint` を
公開していないプロバイダでは `global` は `reconfirm` に落ちます。黙ってローカル
ログアウトになることはありません。

1 つのアプリケーションで両方のボタンを出したい場合は
`auth.oidc.allow_global_logout_request = true` を設定し、「すべてからサインアウト」の
ボタンから `scope=global` を POST します。引き上げしかできません。デプロイが設定した
より狭いスコープをフォーム側から強制することはできない設計です。ユーザーが出ると言った
あとにプロバイダのセッションが生き残ってしまうからです。

かつてこれを綴っていた `auth.oidc.provider_logout` は削除されました。まだ `true` を
持っている設定は、`logout_scope` を名指ししたうえで起動時に拒否されます。黙って読み替えて
いたら、設定ファイルは全体サインアウトと読めるのに実際にはもっと狭い動きをする、という
状態になっていたはずです。

ログイン後は `auth.post_login_path`、または元々アクセスしようとしていたパスに着地し
ます。受け付けるのは同一サイトのルート相対パスだけなので、ログインリンクを
オープンリダイレクトに仕立てることはできません。

## ブラウザのユーザーを読む

セッションはハンドラが動く前に解決されています。

```go
func home(w http.ResponseWriter, r *http.Request) {
	user, signedIn := auth.User(r.Context())
	if signedIn {
		// user.AccountID, user.DisplayName, user.Email, user.Issuer, user.Key
	}
	// ...
}
```

検証済み identity がアプリケーションのアカウントモデルまで決めるわけではありません。
フレームワークは `auth.SetAccountResolver` で登録した関数を呼び、アカウントを検索し、
`auth.oidc.auto_provision` が許す場合は新規作成も行います。安定したリンクは issuer と
`auth.oidc.identity_claim` が指す claim の組み合わせであり、メールアドレスでは
ありません。

期限切れや不明なセッションクッキーは黙って捨てられます。匿名リクエストは通常の状態
なので、`auth.User` は単に `false` を返します。ユーザーを返す場合も、答えるのは
「誰か」であって「その人が何をしてよいか」ではありません。認可はアプリケーションに
残ります。

## モード

ブラウザ用の各モードは、アカウントが存在する前に何がそれを確立するかで分かれます。
パスキーだけでは、最初のクレデンシャルを結び付ける先がありません。JWT-only はこの
ライフサイクルの外側にあります。authorization server が API の呼び出し元へ、すでに
クレデンシャルを発行しているからです。

| `auth.mode` | アカウントの出どころ | 日常のログイン |
| --- | --- | --- |
| `oidc_only` | プロバイダ | プロバイダ |
| `oidc_passkey` | プロバイダ | パスキー。プロバイダは復旧手段 |
| `passkey_only` | 管理者が発行するログイン ID と使い捨てシークレット | パスキー |
| `oauth_only` | X のような、ID トークンを発行しないプロバイダ | そのプロバイダ |
| `jwt_only` | アクセストークンを発行した authorization server | API リクエストごとの Bearer トークン |

各モードは自分が使う設定だけを読み、扱えない設定は拒否します。`passkey_only` で
`AUTH_OIDC_ISSUER` が残っていれば起動時にエラーになり、プロバイダが関与しているかの
ような見え方にはなりません。黙って無視される設定は、設定済みのセキュリティに見えて
しまうためです。

### パスキーのエンドポイント

儀式をマウントするモードは `auth.passkey.path`（既定 `/auth/passkey`）配下に 5 本を
提供します。`POST` と JSON のみで、bootstrap は `passkey_only` にしか存在しません。

```
POST /auth/passkey/login/begin      POST /auth/passkey/login/finish
POST /auth/passkey/register/begin   POST /auth/passkey/register/finish
POST /auth/passkey/bootstrap        (passkey_only のみ)
```

フレームワークはエンドポイントを提供できますが、ページの代わりに
`navigator.credentials` を呼ぶことはできません。そのため、エンドポイントが話す
Base64url と WebAuthn API が要求する ArrayBuffer を変換する小さなスクリプトが必要で、
`pw init` が `public/passkey.js` として生成します。

パスキーのモードではアカウントの継ぎ目が 1 つ増えます。`auth.SetAccountResolver` は
「この検証済み ID はどのアカウントか」に答え、`auth.SetAccountLookup` は「この識別子は
どのアカウントか」に答えます。パスキーのアサーションが必要とするのは後者の向きです。
クレデンシャル自体がアカウントを名指しするので、結びつける外部 ID が存在しません。

### パスキー構成にはアドレスではなく名前で到達する

WebAuthn の Relying Party は**ドメイン**にスコープされ、IP リテラルは RP ID になれません。
`http://127.0.0.1:8080` ではなく `http://localhost:8080` を使ってください。WebAuthn は
`localhost` を secure origin として扱うので、ローカル開発に証明書もトンネルも要りません。

### `passkey_only` の初回サインイン

管理者が `auth.IssueBootstrapCredential` でログイン ID と使い捨てシークレットを発行します。
生のシークレットが返るのは一度きりで、保存されるのはダイジェストだけです。引き換えると
**登録を 1 回だけ許可するチケット**が得られます。セッションではありません。パスキーが
永続化されるまでリクエストは未認証のままなので、引き換え済みのシークレットをログイン状態と
取り違えるハンドラは存在しえません。

`auth.bootstrap.issue_ttl` は受け渡しの猶予を、`auth.bootstrap.enrollment_ttl` は
その後の儀式を区切ります。既定値が時間単位で違うのはそのためです。

## ブラウザのセッション

クッキーが運ぶのは不透明なトークンだけで、セッション本体がどこに住むかは
`session.backend` が決めます。この選択はここまでの設定から独立しています。5 つの
バックエンド、それぞれに必要なキーと制約は
[セッションストレージ](/ja/guides/storage/session-storage/)にあります。
寿命はあちらではなくこちらで宣言します。
`auth.session.ttl` が絶対有効期限、`auth.session.idle_timeout` が
無操作期限。有効期限は「身元の証明がどれだけ有効か」を述べるものだからです。
ログイン時にトークンは新しくなり、それ以前に
ブラウザが持っていたセッションは失効します——ただし cookie バックエンドだけは、
クライアントがすでに取ったコピーを失効させられません。

保存されるのはアカウントの要約だけで、トークン本体は含みません。プロバイダの
アクセストークンや ID Token がセッションに残ることはありません。

## ブラウザセッションを途中で終わらせる

このページが発行する資格情報にはどれも有効期限がありますが、面白いのはいつも期限のほうでは
ありません。インシデント中に効くのは、期限より*前*に終わらせることです。そして 2 種類の
資格情報は、終わり方が違います。片方はこのアプリケーションが持っているレコードで、
もう片方は誰か別のところが発行したトークンだからです。この節がセッション、次の節がトークンです。

ブラウザセッションが期限前に終わる道は 3 つあり、本人が要求するのは最初の 1 つだけです。

**ログアウト。** `auth.logout_path` への `POST` がセッションレコードを破棄し、そのセッションが
持つクッキーをすべて期限切れにします。何が生き残り、なぜ生き残るのかは
[セッション](/ja/guides/backend/sessions/#ログアウトは全部を終わらせる)にあります。

**新しいログイン。** サインインするとトークンが新しくなるので、それ以前にブラウザが持っていた
ものは受け付けられなくなります。共有端末を次の人に渡せるのはこれのおかげですし、
セッションフィクセーションが何も得られないのも同じ理由です。

**アカウントの停止・削除。** 運用者が行うのはこの操作で、自発的にログアウトされない
セッションに対して効きます。認証済みリクエストは、アプリケーションが設置した `AccountLookup`
を通してセッションの背後にあるアカウントを読み直します。停止済み、あるいは消えたアカウントの
セッションは、自分の有効期限ではなくそこで破棄されます。読み直しには上限があり、1 アカウント
につき 30 秒に 1 回までです。毎リクエスト読めば、認証済みのページすべての手前にデータベースの
往復が 1 回入ってしまいます。

この間隔が、「停止はどれくらい速く効くのか」への正直な答えです。停止を実行する場所から
`auth.ForgetAccount(accountID)` を呼べば、次のリクエストで即座に読み直されます。ただし
この呼び出しはプロセスローカルなので、複数インスタンスで動かしている配備では、ほかの
インスタンスは結局この間隔を待ちます。約束できるのは呼び出しではなく間隔のほうです。

アカウントストアがまったく答えられないときは、リクエストを `503` で拒否します。資格情報は
まだ判定されておらず、リトライで成功する余地があり、障害中に通してしまえば、すべての停止が
そのストアの稼働を前提にしたものになるからです。

1 つだけ、最初の道に参加できないバックエンドがあります。`session.backend = "cookie"` では
セッションレコードがブラウザの中にあるので、ログアウトが期限切れにできるのは手の届く
コピーだけです。それ以前に取られたコピーは封の期限まで動き続けます。停止のほうは効きます。
あちらはレコードではなくリクエストに対する検査だからです。とはいえ、セッションを望んだ
ときに終わらせられることが重要なら、レコードはサーバーが消せる場所に置くべきです。
[セッションストレージ](/ja/guides/storage/session-storage/#cookie--ストレージなし)を
参照してください。

## Bearer トークンを失効させる

検証を通ったアクセストークンは、有効期限が切れるまで有効です。これはリソースサーバーの側からはどうにもできない唯一の性質です。トークンを発行したのはこのアプリケーションではないし、発行者のところで取り消すこともできません。だからトークンが漏れたとき、あるいはアカウントの乗っ取りが分かったとき、その資格情報のコピーはすべて `exp` まで動き続けます — アプリケーションが「もう受け付けないトークンの一覧」を自分で持っていない限り。失効とは、その一覧のことで、対象は [`jwt_only`](#jwt-only-の-api-サーバー) だけです。

:::note[必要になるもの]
一覧はリレーショナルテーブルなので、失効には `middleware.rdb` と `auth.backend = "rdb"`、そして `popcornweb_auth_revocation` テーブルを作るフレームワークのマイグレーションが必要です。`admission = "authenticated"` かつ `revocation.mode = "off"` のデプロイにはデータベースそのものが不要です。失効を有効にすることが、この要件を生みます。
:::

### 有効にする

`revocation.mode` にデフォルトはありません。値がなければ起動を拒否します。失効経路なしで運用するなら、それは設定ファイルの上に書かれた決定であるべきで、書き忘れであってはならないからです:

```toml
[auth.jwt]
issuer = "https://issuer.example"
audience = ["orders-api"]
algorithms = ["RS256"]
admission = "authenticated"
identity_claim = "sub"
max_token_lifetime = "1h"
revocation.mode = "both"      # off、token、subject、both のいずれか
```

`off` はスタブではなく、正当な答えの 1 つです。トークンの寿命が 5 分のデプロイなら、その露出ウィンドウは許容できると判断して、データベース要件ごと省いて構いません。逆に、失効を短い寿命の代わりに使わないでください。一覧が抑えるのは漏洩したときの被害であって、通常のトークンが信用され続けるウィンドウ自体は縮みません。

`off` 以外のモードでは、署名とクレームの検証を通った全リクエストが一覧と照合されます。失効済みトークンへの拒否は、期限切れとまったく同じ `401` です。API を探っている呼び出し元にはどちらの拒否か区別できません。それが狙いです。

### 2 つの形

`token` は資格情報 1 つを、`subject` は身元そのものを失効させます。応えるインシデントが違うので、どちらか一方がもう一方の代わりにはなりません。`both` が普通の選択です。

**token** の失効は狭い処置です。持ち主は無事で、資格情報だけが漏れたときのもの。エントリはトークンの `jti` クレームをキーにします。この形を選ぶと `jti` が必須になるのはそのためです — 誰も名指しできないトークンは、誰にも失効させられません。

**subject** の失効は広い処置で、アカウントの乗っ取りに応えます。その身元に対して*今より前に*発行されたトークンをすべて拒否します。盗まれたアカウントの発行済み `jti` を列挙するというのは、まさに誰にもできないことです。だからエントリはタイムスタンプを保存し、照合は各トークンの `iat` と比較します。アカウントの追放ではありません — 本人が発行者で再認証すれば、新しいトークンはスタンプより後の発行なので通ります。アカウントを締め出したいなら、それは失効ではなく [admission](#誰を通すか) の決定です。

どちらの呼び出しも issuer を明示的に受け取ります。エントリは issuer でスコープされており、設定から推測する作りだと、デプロイに 2 つ目の issuer が加わった瞬間、黙って間違ったスコープに書き込むことになるからです。

### 自分のコードから失効させる

このどれにも HTTP エンドポイントはありません。トークンを失効させるエンドポイントには認可ルールが要りますが、それを書く根拠をフレームワークは持っていません。だから表面は Go の呼び出しの集合で、誰がそこに到達できるかはアプリケーションが決めます:

```go
// internal/admin/revoke.go
package admin

import (
	"net/http"

	"github.com/shibukawa/popcornweb/plugin/auth"
	"github.com/shibukawa/popcornweb/pw"
)

const issuer = "https://issuer.example" // このサーバーが検証する auth.jwt.issuer

// RevokeAccount は 1 つの身元の発行済みトークンをすべて取り下げます。
// ルーティングは運用者が管理操作に使う経路に合わせてください。
// フレームワークは意図的に何もマウントしません。
func RevokeAccount(w http.ResponseWriter, r *http.Request) {
	identityKey := r.PathValue("identity")
	err := auth.RevokeSubject(r.Context(), issuer, identityKey,
		"compromised account, support ticket 4211")
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

トークン 1 つ版は `auth.RevokeToken(ctx, issuer, tokenID, note)` です。note はインシデント対応中にテーブルを読む未来の自分のためのもので、フレームワークが呼び出し元に見せることはありません。

一度も提示されたことのない識別子を失効させても**成功します**。盗まれたトークンがここで使われたかどうかを呼び出し側が知る術はなく、そこでエラーを返すと、安全側の反射 — まず失効、調査はあと — が無駄にうるさくなるからです。

失効エントリに有効期限を与える必要はありません。スタンプから `max_token_lifetime` だけ生き、これは作りからして、拒否すべきすべてのトークンより長生きします。その後はストアが掃除します。

### 間違いを取り消す

`auth.ReinstateToken` と `auth.ReinstateSubject` はエントリを削除します。履歴付きのアンドゥではなく削除です。そのエントリが拒否していた未失効のトークンは、次のリクエストからすべて再び通ります。推測してはいけない管理画面のためには `auth.TokenRevoked` と `auth.SubjectRevoked` があり、保存されたスタンプと有無を、リクエスト単位のキャッシュを介さずストアから直接読んで報告します。

### ストアが答えられないとき

`revocation.on_unavailable` のデフォルトは `refuse` で、その拒否は `401` ではなく `503` です — 資格情報は判定されていないのだから、リトライすれば通るかもしれません。障害時に通す作りにすると、失効テーブルはただの最適化になり、すべての失効が「インフラが生きていれば」という条件付きになってしまいます。1 つ上の節でアカウントストアが同じ振る舞いをするのも、同じ理由です。

`admit` への上書きはインシデント対応のレバーであって、デプロイの姿勢ではありません。設定すると、障害の間じゅう失効済みトークンがすべて復活します。設定アドバイザリがこれをエラーレベルで報告するのは、インシデントを越えて静かに残らないようにするためです。

デフォルトでは通過した全リクエストがストアを読みます。`revocation.max_propagation_delay` は代わりに小さなプロセス内キャッシュを許可します。この値は「失効はどれくらい速く効くのか」への正直な答えでもあります — いま失効させたトークンは、最大でその時間だけ通り続けるかもしれません。読み取り 1 回分がレイテンシ予算に響かない限り、ゼロのままにしてください。この 3 つのキーは、他の `[auth.jwt]` キーと一緒に[アプリケーション設定一覧](/ja/reference/configuration/#authjwt)に載っています。

## 開発中

ローカル開発でログインフローを試すために、本物のプロバイダを必須にする必要はありません。
代わりに `pw dev` が開発用プロバイダを起動できます。

```toml
# popcornweb.toml
[dev.idp]
enabled = true
```

[開発用の認証プロバイダ](/ja/productivity/dev-identity-provider/)を起動し、その実行
専用のクライアントを登録し、`AUTH_OIDC_ISSUER`、`AUTH_OIDC_CLIENT_ID`、
`AUTH_OIDC_CLIENT_SECRET` を注入します。この方式でスキャフォールドしたプロジェクトの
コミット対象ファイルには、プロバイダの値が一切現れません。ログインは一覧から
ユーザーを選ぶだけで、パスワードは検証しません。だからこそ開発以外では動きません。

テストでは `testutil.WithIdentityProvider` が同じプロバイダを起動し、`WithLoginUser`
でユーザーを事前指定できます。`auth.login_path` への1リクエストでフロー全体が完了
します。[テスト](/ja/productivity/testing/#withidentityprovider)を参照してください。

## デプロイ

デプロイ時には、その利便性は外れます。`issuer`、`client_id`、`client_secret` は非空、
`redirect_url` はデプロイ先コールバックの絶対 URL にします。`dev` 以外で空または
パスだけの値を使うと、`pw doctor` はエラーとして報告します。プロバイダの値は
`AUTH_OIDC_ISSUER`、`AUTH_OIDC_CLIENT_ID`、
`AUTH_OIDC_CLIENT_SECRET`、あるいは `${NAME}` 参照から与え、コミットはしません。
cookie バックエンドのセッションはもう1つ独自の秘密鍵を要求します
（[セッションストレージ](/ja/guides/storage/session-storage/#cookie--ストレージなし)）。

`redirect_url` はプロバイダに登録した URL と一字一句一致している必要があります。
フレームワークが `redirect_uri` として送るのがこの値なので、登録と違えばアプリケーションに
届く前にプロバイダが拒否します。

例外はループバック開発だけです。`allow_loopback_http = true` のとき、空の
`redirect_url` は現在のリクエストの scheme と `Host` に `callback_path` を足した URL に
なります。ルートパスを指定した場合は、そのパスを `Host` に足します。リクエストの
`Host` は `localhost`、`*.localhost`、`127.0.0.1` や `::1` などのループバック IP に
限られます。したがって `localhost:8080` から始めたログインは同じ origin に戻り、
`127.0.0.1:8080` から始めたログインも別の origin のまま戻ります。公開ホストや、本番 URL
のプロバイダ登録を省く用途には使わないでください。

ログアウト後の URL もプロバイダに登録してください。未登録の `post_logout_redirect_uri`
は拒否されます。フレームワークが送るのはリクエストオリジンのルートなので、
`https://app.example/` で配信しているならその URL を登録します。

開発用プロバイダだけは例外で、ローカル宛てのログアウト後 URL を登録なしで受け付け
ます。`pw dev` ではどこにも登録しないままログアウトが動きます。
