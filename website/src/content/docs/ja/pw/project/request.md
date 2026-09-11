---
title: pw request
description: 起動中のアプリケーションへリクエストを 1 つ送る。curl のフラグを OpenAPI 文書で振り分ける。
sidebar:
  order: 8
---

```sh
pw request --list
pw request /api/users -d name=Alice -d age=3
pw request ShowUser -d id=7 -d verbose=true --format=json
```

`pw request` はこのプロジェクト自身のアプリケーションへリクエストを 1 つ送り、
応答を表示します。フラグは curl のものなので、シェル履歴から持ってきた行はその
まま動きます。curl に無いのは、アプリケーションがすでに配信している文書です。
`pw request` は `server.openapi` の OpenAPI 文書を読み、パスが指す操作を見つけ、
`-d key=value` をそのハンドラが読む場所へ送ります。パスセグメント、クエリ、
ヘッダ、Cookie、ボディのどれかで、値はスキーマの型に合わせて変換されます。
人と同じくらいエージェントを念頭に書かれています。`--list` が操作を列挙し、
`--format=json` が解析用のオブジェクトを 1 つ出力します。

## アプリケーションの見つけ方

接続先は次の順で決まり、レポートがどれで決まったかを述べます。

1. `--url=http://localhost:8080` を渡したとき。ホストは loopback に限ります。
   `pw request` が届くのはこのプロジェクトのアプリケーションだけです。
2. 起動中の [`pw dev`](/ja/pw/project/dev/) ループが告知したアドレス。ループの
   コンソールに問い合わせるので、使用中のポートを避けて動いたポートシフト
   後のアドレスもそのまま得られます。
3. その環境の設定にある `server.port` を推測として使う。ここで接続拒否になれば
   その旨を述べ、`pw dev` の名前を挙げます。

`--env=<token>` はどの設定ファイルを読むかを選びます。既定は
[`pw doctor`](/ja/pw/project/doctor/) と同じで、`APP_ENV`、なければ `dev` です。

## 操作の指定

対象はパス、メソッドとパス、または `--list` に出る operationId です。

```sh
pw request /api/users/7                # GET。/api/users/{id} に照合される
pw request POST /api/users -d name=x   # メソッドは単語でも -X POST でも
pw request CreateUser -d name=x        # id で指定。セグメントはすべて -d から
```

リテラルなパスは `net/http` と同じ流儀でテンプレートに照合されます。
`/api/users/7` は `GET /api/users/{id}` を選び、`id` を埋めます。`-X` が無ければ
メソッドは curl と同じ既定です。`GET`、データがあれば `POST`。そのメソッドで何も
一致せず、別のメソッドでそのパスを扱う操作がちょうど 1 つあれば、それを採ります。
2 つの操作が答えられるパスは、`-X` か id で決まるまで拒否されます。

カタログに無いパスにも送ります。そのときのペアは curl の意味そのままです。
フォームエンコードの `POST`、`-G` でクエリへ、`--json` でオブジェクトとして。
レポートは操作が一致しなかったこととその理由を述べるので、フォールバックが
黙って起きることはありません。

## ペアの行き先

操作が一致すると、`-d key=value` は文書がその名前に宣言している場所へ行きます。
JSON と宣言されたボディは 1 つのオブジェクトに組み立てられ、整数・数値・真偽値は
解析され、繰り返したキーは配列になります。

```sh
pw request CreateUser -d name=Alice -d age=3 -d admin=true -d tags=a -d tags=b
```

```
pw request: POST /api/users via http://localhost:8080 (dev) as CreateUser
  name -> body
  age -> body
  admin -> body
  tags -> body
```

これは `{"name":"Alice","age":3,"admin":true,"tags":["a","b"]}` を送ります。
スキーマに収まらない値は与えた文字列のまま送られ、レポートに注記されます。
`age=three` が整数でなかったと知るのに、ハンドラの `400` を読むのは遠回りだから
です。

操作が名前を持たないペアはボディの位置に送られ、注記されます。`--strict` を
付ければ使い方エラーになり、スクリプトにはそちらが向いています。埋めなかった
テンプレートセグメントは常にエラーです。`/api/users/{id}` へ文字どおり送ること
が意図であることはないからです。

`-F field=value` と `-F file=@path` は操作の宣言にかかわらず multipart で送り、
`-d @file` はそのファイルを宣言されたメディアタイプのボディとして送ります。
明示した `-H 'Content-Type: …'` は文書より優先されるので、間違ったコンテンツ
タイプを試したい呼び出しも書けます。

## 出力

既定ではボディが curl と同じく標準出力へ、上の振り分けレポートが標準エラーへ
出ます。`-i` でステータスとヘッダが前に付き、`-o file` でボディをファイルに書き、
`-s` でレポートを消せます。

`--format=json` はかわりにオブジェクトを 1 つ出力します。

```json
{
  "status": 201,
  "headers": { "Content-Type": ["application/json"] },
  "body": { "id": 8, "name": "Alice" },
  "request": { "method": "POST", "url": "http://localhost:8080/api/users" },
  "operation": { "operationId": "CreateUser", "method": "POST", "path": "/api/users" },
  "routing": [ { "key": "name", "in": "body", "value": "Alice" } ],
  "target": "dev",
  "origin": "http://localhost:8080"
}
```

ボディは応答が JSON だと言っていれば解析され、そうでなければ文字列のままです。
読む側が扱う形は 1 つで済みます。

## 操作の一覧

```sh
pw request --list
```

```
3 operations at http://localhost:8080

POST   /api/users  CreateUser
       name:string*  body
       age:integer  body
       body: application/json

GET    /api/users/{id}  ShowUser
       id:integer*  path
       verbose:boolean  query

GET    /account  Account  [protected]
```

`*` は必須パラメータです。`[protected]` は、読んだ環境の `auth.protection.include`
がガードするパスです。ガード自身のマッチャで評価した設定の射影なので、ハンドラ
内部でガードしているルートには付きません。`--format=json` は同じカタログを配列で
出力します。

## 資格情報

端末から実行できるログイン儀式はありません。ガードされたルートは curl と同じ
やり方で呼びます。`-H 'Authorization: Bearer …'`、`-u user:password`、あるいは
Cookie です。`-c jar.txt` は応答が設定した Cookie を curl の cookie ファイル形式で
書き、`-b jar.txt` がそれを再送するので、ログイン応答を次の呼び出しに渡せます。
`-b 'name=value'` は Cookie 文字列を 1 つ直接送ります。

どれも無ければ、保護されたルートは `401` を返します。それが答えです。

## 終了ステータス

コードは curl のものです。curl 向けに書いたスクリプトの分岐がそのまま使えます。

| 終了 | 条件 |
| --- | --- |
| `0` | ステータスにかかわらず応答を受け取った |
| `22` | `-f` があり、ステータスが `4xx` または `5xx` |
| `1` | アプリケーションが見つからない、loopback でない `--url`、接続失敗、タイムアウト |
| `2` | 未知のフラグ、曖昧な対象、未充填のセグメント、`--strict` 下の未知のペア、一覧する文書が無いのに `--list` |

## しないこと

届くのはこのマシン上のこのプロジェクトのアプリケーションだけです。別ホストの
`--url` は接続を開く前に拒否されます。`-L` が無ければリダイレクトを追いません。
ログインページへのリダイレクトこそ知りたかった答えであることが多いからです。
セッションは cookie ファイル以上には持たず、応答へのアサーションもしません。
それはテストであり、[`testutil`](/ja/productivity/testing/) の仕事です。サーバー
なしでアプリケーションを呼ぶこともしません。今のところアプリケーションは
起動している必要があり、`pw dev` の下ではすでにそうなっています。

ページルートも手の届く範囲外です。`.pw.html` のページは
[discovered routing](/ja/guides/cross-layer/discovered-routing/) で、設計上 OpenAPI
文書に入りません。ページはパスで curl の意味そのままに呼ばれ、何も振り分けられ
ません。
