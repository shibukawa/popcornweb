---
title: 1.0 への道
description: Go の言語機能を待っていた API 変更が Go 1.27 で着地しました。呼び出しの前後を並べて記録します。
sidebar:
  order: 4
---

1.0 未満のバージョン番号は、普通どおりの意味です。表面はまだ動きますし、リリースが
名前を引っ込めることも許されています。早くから設計が終わっていて、待っている相手が
このプロジェクトではなく言語だった変更が 1 束ありました。それが着地しました。この
ページはその記録を残します。以前どう書いたかと、今どう書くかを並べて。以前の綴りで
書かれたプロジェクトが、何が動いて何が動いていないかを正確に知るためです。

## 何が、何を待っていたのか

Go 1.27 より前、メソッドは自分の型パラメータを宣言できませんでした。型付きの読み込み、
型付きの書き込み、型付きの設定アクセサ。フレームワークが型パラメータを必要とする
ところは、設計上の好みが何であれ、パッケージ関数にするしかありませんでした。

```go
// 構文以外のすべてにおいて、レシーバはストアだった。
quote, err := pw.Memo(ctx, store, QuoteKey{Pair: pair}, fetchQuote)
```

Go 1.27 はメソッドに型パラメータを許し、TinyGo 0.42 がそれをビルドします。後者が
トリガーの後半でした。ホストの Go でしか使えない変換は、整理ではなくビルドの分断に
なるからです。モジュールが `go 1.27.0` に移った時点で Popcorn Web は両方を要求し、
これらの操作はいずれも、最初から言おうとしていたメソッドになりました。

**動いたのは呼び出しの形だけです。** 保存されたもの、生成されたもの、ワイヤ上のものは
何も変わっていません。このフレームワーク自身が持つエントリでは、古いパッケージ関数は
消えました。どれもメソッドの 1 行の代役で、ハンドルはすでにあったので、呼び出し箇所の
編集は機械的です。以下にそれを示します。tinybind 側のエントリでは古い関数が
`// Deprecated:` 付きのラッパーとして残り、呼び出し箇所ごとに移ってもいいし、移らなく
てもいい。

## データキャッシュ

[`pw.MemoStore`](/ja/guides/backend/data-cache/) は言語より先にストアをハンドルへ解決して
いました。意図的です。ストアが値として手元にあったので、操作はそれを取得した行に
触れないままハンドルの上へ移りました。

```go
// 以前
store, err := pw.MemoStore(r, "rates")

quote, err := pw.Memo(ctx, store, QuoteKey{Pair: pair}, fetchQuote)
if pw.MemoHas(ctx, store, key) { /* … */ }
pw.MemoSet(ctx, store, key, quote)
pw.MemoInvalidate(ctx, store, key)
pw.MemoInvalidateScope(store, subject)
pw.MemoInvalidateTag(store, "user:u1")
```

```go
// 現在 — 1 行目が変わらないことこそが要点だった
store, err := pw.MemoStore(r, "rates")

quote, err := store.Get(ctx, QuoteKey{Pair: pair}, fetchQuote)
if store.Has(ctx, key) { /* … */ }
store.Set(ctx, key, quote)
store.Invalidate(ctx, key)
store.InvalidateScope(subject)
store.InvalidateTag("user:u1")
```

`pw.Memo` の一族はもう存在せず、同じ名前は `pwfast` からも消えました。
`InvalidateScope` と `InvalidateTag` は型パラメータを取らないので、実は最初から
メソッドにできました。ストアの読み方が二通りに割れないよう、これらも一緒に移して
あります。生成は関数を追っていたのと同じようにメソッド呼び出しを追うので、キー型は
今も結果の隣の引数から見つかります。

## Firestore のトランザクション

整理以上の価値があったのはこれです。書き込みはすでにトランザクションのメソッドなのに
型付きの読み込みはそうではなかったので、ひとつのトランザクションが隣り合う行で二通りに
書かれていました。

```go
// 以前
tx.Store(user)
user, err := firestorebind.LoadTx[User](ctx, tx, key)
```

```go
// 現在
tx.Store(user)
user, err := tx.Load[User](ctx, key)
```

型付きの読み込みは `Load`・`LoadAll`・`QueryPage` の 3 つで、`QueryKeysPage` と `Count`
も一緒に移りました。変わったのは綴りだけではありません。トランザクション境界が引数を
やめてレシーバになり、その呼び出しが「何の内側にいるのか」を API 自身が語ります。
`LoadTx` とその仲間は deprecated なラッパーとして残っています。

なお、トランザクションの読み込みをコンテキスト経由ではなくトランザクション値経由で
到達させるのは別の判断で、こちらはこの変更を経ても変わっていません。ハンドルを
コンテキストに載せてしまうと、同じ呼び出し箇所が、どのコンテキストが届いたかによって
二つの意味を持ってしまうからです。

## DynamoDB と Firestore のハンドル

Popcorn Web はどちらのストアもラップしていないので、`On` 系はアプリケーションの
作者が文字通り自分で書いていた API です。そしてハンドルは具象型、つまりレシーバになる
のを待っていた型でした。

```go
// 以前
h, err := dynamo.Handle(ctx)
note, err := dynamobind.LoadOn[Note](ctx, h, "note", key)
err = dynamobind.StoreOn(ctx, h, "note", note)
```

```go
// 現在
h, err := dynamo.Handle(ctx)
note, err := h.Load[Note](ctx, "note", key)
err = h.Store(ctx, "note", note)
```

最後の行をもう一度見てください。引数から型が推論できる操作は、型引数そのものが
消えます。保存も、まとめての保存も、削除も、ただの呼び出しです。

`On` 系はそれぞれ、接尾辞を外した同名のメソッドになりました。
[DynamoDB](/ja/guides/storage/dynamodb/) のハンドルには `Load`・`LoadAll`・`Store`・
`StoreAll`・`StoreReturning`・`Remove`・`RemoveReturning`・`Update`・`QueryPage`・`Query`・
`ScanPage`・`Scan`、[Firestore](/ja/guides/storage/firestore/) のハンドルには `Load`・
`LoadAll`・`Store`・`StoreAll`・`Insert`・`InsertAll`・`Update`・`Remove`・`RemoveAll`・
`QueryPage`・`Query` と、キーを取らないエントリ群です。`On` 付きの関数は deprecated と
して残っています。隣に並ぶコンテキスト解決版（`dynamobind.Load[Note](ctx, …)`）は
まったく変わっていません。設計上レシーバを持たない側だからです。生成は 3 通りの綴りを
すべて読みます。

## 隔離されたテスト設定

```go
// 以前
testutil.Update[pw.MiddlewareConfig](config, func(middleware *pw.MiddlewareConfig) {
	middleware.CSRF.Enabled = false
})
app := testutil.Get[AppConfig](config)
testutil.Set(config, app)
```

```go
// 現在
config.Update(func(middleware *pw.MiddlewareConfig) {
	middleware.CSRF.Enabled = false
})
app := config.Get[AppConfig]()
config.Set(app)
```

3 つのうち 2 つは引数から型を推論し、`Get` だけが今も型を書きます。
[`testutil`](/ja/productivity/testing/) の設定関数は 3 つとも消え、メソッドに置き換わり
ました。

## セッションレジストリ

`session.Register` がレジストリを第 1 引数に取っていたのは、型付きのコーデックを運ぶ
ためだけでした。今はメソッドで、関数は消えています。`pw.RegisterStore` で状態を宣言して
いるプロジェクトはこれを呼んでいないので、変えるものはありません。

```go
// 以前
err := session.Register[Cart](registry, "cart", session.Private, nil)
```

```go
// 現在
err := registry.Register[Cart]("cart", session.Private, nil)
```

## 動かなかったもの

コンテキスト解決版のアクセサは、この先もずっと関数のままです。設計上レシーバを持た
ない側、つまりハンドルではなく `context.Context` から値を読む側だからです。
コンストラクタも同じ理由で関数のままです。

言語以上の理由で塞がっているものが 1 つあります。`sqlbind.ScanRows` が取る行カーソルは
インターフェイスで、自分が定義していない型にメソッドを生やせるパッケージはありません。
Go がどう変わっても関数のままです。これは後から誰かが調べ直すより、書いておく価値が
あります。

生成されるレイヤーのエントリも上流で移りました。HTML ビルダーのループ・await・live・
プロバイダの各エントリ、JSON パーサの `ParseSlice` と `ParseMap`、そして
`sqlbind.AppendValues` で、いずれも関数は deprecated なラッパーとして残っています。
生成物は今も関数形式を綴るので、アプリケーションが持つものはこれらを理由に再生成
されていません。
