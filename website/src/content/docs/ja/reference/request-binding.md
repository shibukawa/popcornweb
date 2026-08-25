---
title: リクエストバインディング定義
description: pw.Parse で使える構造体タグとフィールド型、各フィールドの値をリクエストのどこから取得するかを決める規則の一覧。
sidebar:
  order: 1
---

`pw.Parse[T](r)` は1つのリクエストから1つの構造体を埋めます。そのどこにも実行時の
判断はありません。`pw generate` が呼び出し箇所を読んで `T` を知り、そのバインディングと
検証のコードを書きます。この仕組み全体が TinyGo でも使えるのはそのためで、以下の規則の
いくつかがリフレクション方式のバインダより厳しいのも同じ理由です。

ここでは、構造体タグとフィールド型の仕様をまとめます。ルーティング、レスポンス、
リクエストスコープのアクセサなど、ハンドラ側の使い方については
[ハンドラ](/ja/guides/frontend/handlers/)を参照してください。

## 生成が呼び出し箇所に求めるもの

```go
input, err := pw.Parse[showUserInput](r)
```

型引数は、呼び出し箇所に書かれた具体的な名前付き型である必要があります。ジェネリックな
ラッパ越しの `Parse` や、型引数が型パラメータである `Parse` は静的にどの型も特定できず、
バインディングも生成されません。生成は1回につき1つのパッケージディレクトリを解析するので、
呼び出しとそれが名指す型は `generate.handlers` が挙げるディレクトリに置きます。

## ソースタグ

フィールドごとに1つのタグが、値の出どころを名指します。タグの無いフィールドは、自分の
名前を持つ `input` フィールドです。

| タグ | ソース | 備考 |
| --- | --- | --- |
| *(タグ無し)* または `input:"name"` | クエリ文字列、次にボディ | 既定。両方にある場合はクエリが勝つ |
| `query:"name"` | クエリ文字列のみ | |
| `payload:"name"` | リクエストボディのみ | JSON、フォーム、マルチパート |
| `payload:"*"` | 他のどのフィールドも消費しなかったボディのキー全部 | 構造体に1つだけ。下の rest マップを参照 |
| `path:"id"` | パスのワイルドカード | `GET /users/{id}` のようなパターンから |
| `header:"Authorization"` | リクエストヘッダ1つ | |
| `cookie:"session"` | クッキー1つ | |
| `method:"method"` | HTTP メソッド | ワイヤ名は使われず、フィールドは `GET`, `POST` などを受け取る |

明示的なワイヤ名が無ければ、フィールド名が lower camel case になります。`DisplayName` は
`displayName` をバインドします。

`input` の解決はリクエスト単位ではなくフィールドの種別単位です。**スカラ**の `input`
フィールドはクエリを先に読み、クエリがその名前を持たないときだけボディへ落ちます。
**ネストした構造体・スライス・マップ**は常にボディから来ます。クエリ文字列にはそれを運ぶ
形が無いからです。曖昧さが便利ではなくバグになりうる時点で、`query` と `payload` を明示
してください。フィルタをどちらからでも受け取るエンドポイントは呼び出し方が2通りあり、
たいていは片方が間違いです。

`path`、`header`、`cookie`、`method` はボディのキーを消費しません。rest マップがそれらを
見ずにボディを網羅できるのはそのためです。

`json` タグはここまでの何ひとつ決めません。`json:"-"` はレスポンス側で JSON ドキュメント
からフィールドを外し、`pw.WriteAPI` に書かせなくしますが、バインダはそのフィールドを
ワイヤ名でリクエストから埋めます。`json:"-"` だけを付けた `Hidden` フィールドは依然として
`input` フィールドであり、`?hidden=x` で設定されます。`Parse` の構造体にバインド不能な
フィールドはありません。呼び出し側に握らせてはいけない値は、そもそもそこに置かない
ことです。リクエストが運んでよいものをバインドし、残りはハンドラで導出してください。

## フィールド型

| 種別 | 型 |
| --- | --- |
| スカラ | `string`, `int`, `int64`, `bool`, `float64` |
| ファイル | マルチパートボディでの `httpbind.File` |
| 複合 | 名前付き構造体、ネストした無名構造体、`[]scalar`, `[]struct`, `map[string]scalar`, `map[string]struct` |
| rest | `payload:"*"` の後ろの `map[string]any` または `map[string]json.RawMessage` |

ネストは JSON が前提です。JSON ボディは構造体をオブジェクトへ、スライスを配列へ、マップを
文字列キーのオブジェクトへ、いくらでも深く対応づけます。
`application/x-www-form-urlencoded` と `multipart/form-data` のボディはフラットなキーしか
運ばないので、ネストした形を宣言するのはクライアントが JSON を送る場合だけにしてください。

複合型のうち2つは例外です。ドキュメントの外に綴りを持っているからです。`[]byte` は
どの値ソースからも base64 としてバインドされます。`[]scalar` は **繰り返しクエリキー**
（`?tag=a&tag=b`、チェックボックス群が送る形）からバインドされます。明示的な `query`
タグが必要です。

```go
Tags []string `query:"tag"`
```

タグを省いたスライスは JSON の場合として扱われ、これまで通りボディから来ます。キーが
無い場合と空値しか持たない場合はどちらも nil のままです。要素がひとつでもパースできな
ければ、部分的なリストをバインドせずリクエスト全体を失敗させます。`?tag[]=a` は
`tag[]` という名前のキーにバインドされ、`?tag=a,b` は1要素です。ブラケットもカンマも
配列としては読みません。`path`・`header`・`cookie` の繰り返しは従来どおり拒否されます。

ポインタ型のフィールドはバインドされません。値のフィールドを使い、その代償については
下の「値の存在とゼロ値」を読んでください。

### リクエストボディ

3つのメディアタイプが同じ構造体を埋めます。だから普通の HTML フォーム POST と JSON API
呼び出しが、1つのハンドラと1つのモデルを共有できます。

- `application/json`
- `application/x-www-form-urlencoded`
- `multipart/form-data`

[`generate.api.cbor`](/ja/guides/backend/cbor/) を有効にしたプロジェクトでは
`application/cbor`（および `+cbor` サフィックス型）が 4 つ目に加わります。ボディは
テキストキーの CBOR map 1 つで、JSON ボディが埋めるのと同じフィールドを埋めます。
上限は、下のマルチパート制限がアップロードを縛るのと同じ形で `server.cbor_max_body` が
縛ります。`payload:"*"` の rest map には対応する CBOR の形がないため、この組み合わせは
生成時にエラーとして報告されます。

### マルチパートのファイル

```go
import httpbind "github.com/shibukawa/tinybind-go"

type uploadInput struct {
	Title string        `payload:"title" check:"required"`
	Image httpbind.File `payload:"image" check:"required"`
}
```

`File` は `Filename`、`ContentType`、`Size`、`Content` を公開します。ファイルに適用できる
`check` ルールは `required` だけで、それ以外は生成エラーです。

上限は2段あり、外側はフレームワークのものです。`server.max_request_body` がリクエスト全体を
既定 10 MiB で抑え、その内側のマルチパートボディ上限は既定 1 MiB です。

```go
httpbind.SetMaxMultipartBodyBytes(8 << 20) // 8 MiB
```

マルチパートの上限を `server.max_request_body` より上げても、そちらが動くまで何も変わり
ません。[アプリケーション設定一覧](/ja/reference/configuration/)を参照してください。

### rest マップ

`payload:"*"` は、兄弟フィールドが消費しなかったボディのキーを集めます。

```go
type eventInput struct {
	Type   string         `payload:"type"`
	Extras map[string]any `payload:"*"`
}
```

- マップのキー型は `string` である必要があります。`map[string]any` はデコード済みの JSON 値を、
  `map[string]json.RawMessage` は生のバイト列を保ちます。
- rest フィールドは構造体につきちょうど1つ。2つ目は生成エラーで、マップでない型も同じです。
- 兄弟の `payload` フィールド、およびボディを読む `input` フィールドが消費したキーは除かれます。
  `path`、`header`、`cookie`、`method` は何も消費しないので、同名のボディキーはここに来ます。
- フォームとマルチパートのボディは、残ったファイル以外の値を文字列として渡します。
- オブジェクトでない JSON ボディは、空マップではなくバインドエラーになります。
- 残ったキーが無ければ、nil ではなく空のマップになります。

## 検証

値を拒否できる規則を持つのが `check` です。`enum` と `default` はそれぞれ独立したタグで、
`check` の中に書くと、代わりに使うタグを名指した生成エラーになります。

```go
type listInput struct {
	Keyword string `query:"keyword" check:"required,minlen=2,maxlen=64"`
	Page    int    `query:"page" check:"min=1" default:"1"`
	Sort    string `query:"sort" enum:"asc,desc" default:"asc"`
}
```

### `check` のルール

| ルール | 書き方 | 対象 |
| --- | --- | --- |
| `required` | 単独 | すべてのフィールド種別 |
| `min`, `max` | `min=1` | `int`, `int64`, `float64` |
| `minlen`, `maxlen`, `len` | `minlen=3` | `string` |
| `pattern` | `pattern=^[A-Z]{3}$` | `string` |
| `email` | 単独 | `string` |
| `uuid` | 単独 | `string` |
| `date` | 単独 | `string`, `YYYY-MM-DD` |
| `time` | 単独 | `string`, `HH:MM:SS` |
| `datetime` | 単独 | `string`、RFC 3339 |

ルールの区切りはカンマです。だから `pattern` はタグの最後のトークンでなければなりません。
正規表現の中のカンマが分割してしまうからです。他の位置に書かれた `pattern` は、式を黙って
切り詰めるのではなく、そのメッセージを出して生成に失敗します。

`min` と `max` は境界を含み、どちらも比較の前に浮動小数として解析されます。だから `int` に
`min=1` と書けます。

書式ルールは、`required` も付いていない限り空の値を素通しします。オプショナルなメール
アドレスのフィールドは、値が無いことは受け入れ、`not-an-address` は拒否します。

ファイル、rest マップ、ネストした構造体、スライス、マップに適用できるのは `required` だけ
です。それ以外のルールは、そう告げる生成エラーになります。

### 値の存在とゼロ値

`required` は「渡された」ではなく「空でない」を意味し、何が空かは種別によって違います。

- `string` は空文字列でないこと。
- スライスは長さが 0 でないこと。
- 取り出せなかった `path` と `header` のフィールドは違反。
- `int`、`int64`、`float64`、`bool` は、値が省かれたのか明示的な `0`／`false` なのかを
  区別できないので、`required` を付けてもゼロ値を受け入れます。

最後の1行はバグではなく値フィールドの限界です。どちらが起きたかをどうしても知る必要がある
契約にはセンチネルが要り、それを成り立たせるのが次の順序です。

### `default`

```go
Page int `query:"page" check:"min=1" default:"1"`
```

- スカラのみ。ファイル、rest マップ、構造体、スライス、マップに付けると生成エラーです。
- 生成時に型付きのリテラルへ解析されるので、解析できない値はリクエストではなくビルドを
  失敗させます。
- 適用は検証の**あと**で、値が無かったときだけです。渡されて拒否された値は拒否されたまま
  で、既定値は修復手段ではありません。
- 空白は値の一部です。区切り文字が無く、その周りを削る根拠が無いので、何も削られません。
- `default:""` は明示的な空文字列の既定値として尊重されます。タグが無いのとは別物です。

センチネルが成り立つのはこの順序のおかげです。`check:"min=1" default:"-1"` なら、値が
無ければ `-1` が届き、明示的に渡された `-1` は拒否されます。ポインタでない `int` でも
ハンドラが両者を区別できます。

### `enum`

```go
Sort string `query:"sort" enum:"asc,desc" default:"asc"`
```

- スカラのみで、各値はフィールドの型として解析できる必要があります。
- カンマ区切りで、各値の前後の空白は削られます。カンマを含む値は表現できません。それが要る
  集合が欲しいなら、タグではなく検証付きの型を作ってください。
- オプショナルな値が無い場合、検査は飛ばされます。一覧に無い値が渡されると
  `must be one of: asc, desc` というフィールドエラーになります。
- `enum` だけを持つフィールドも検証コードを生成し、`400` レスポンスを文書化します。
- `default` は一覧に含まれていなくてかまいません。上のセンチネルが保たれるのはそのためです。

## 失敗が返すもの

`pw.Parse` は、拒否したフィールドすべてを載せた1つのエラーを返します。それを
`pw.WriteProblem` に渡せば、フィールド単位の詳細を持つ RFC 9457 の problem ドキュメントに
なります。

```go
input, err := pw.Parse[createUserInput](r)
if err != nil {
	pw.WriteProblem(w, r, pw.BadRequest(err))
	return
}
```

フィールドの失敗はそれぞれ、値が来るはずだった場所——`input`、`query`、`payload`、`path`、
`header`、`cookie`——を記録します。problem を読むクライアントがどこを直せばよいか分かる
ためです。タグで表せない規則には
`pw.Validation(pw.Field(name, location, message))` が同じ形を手で組み立てます。
[レスポンス](/ja/guides/frontend/responses/)を参照してください。

## OpenAPI に載るもの

バインディングを書くのと同じ解析がオペレーションを書くので、スキーマがコードから離れる
ことはありません。上のすべての規則に対応するキーワードがあります。

| タグ | OpenAPI |
| --- | --- |
| `check:"required"` | `required`、またはパラメータの必須指定 |
| `check:"min"`, `check:"max"` | `minimum`, `maximum` |
| `check:"minlen"`, `check:"maxlen"` | `minLength`, `maxLength` |
| `check:"len"` | `minLength` と `maxLength` の両方 |
| `check:"pattern"` | `pattern` |
| `check:"email"`, `"uuid"`, `"date"`, `"time"`, `"datetime"` | `format` |
| `enum` | `enum` |
| `default` | `default` |

説明文の置き場所は増えません。リクエスト構造体の doc コメントがスキーマの説明になり、
フィールドの doc コメントや行末コメントがプロパティとパラメータの説明になります。
`Deprecated:` で始まる段落は `deprecated: true` を立てます。
[API ドキュメント](/ja/productivity/api-documentation/)を参照してください。

## よくある生成エラー

- 呼び出し箇所で型引数が具体的な名前付き型でない `Parse`
- `check` タグの中に書かれた `enum=` または `default=`
- タグの最後のルールになっていない `pattern=`
- 数値でないフィールドへの `min`／`max`、文字列でないフィールドへの長さ・書式ルール
- ファイル、rest マップ、構造体、スライス、マップのフィールドへの `required` 以外のルール
- 2つ以上の `payload:"*"` フィールド、または `map[string]…` でない rest フィールド
- フィールドの型として解析できない `default` または `enum` の値
- `omitempty` でも `omitzero` でもない `json` タグのオプション。落としたつもりの
  フィールドを出力してしまう綴り間違いは、ここで止まります
