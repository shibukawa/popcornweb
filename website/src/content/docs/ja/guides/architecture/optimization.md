---
title: 段階的な HTML 更新
description: HTML を段階的に届ける、画面遷移後に更新する、サーバ状態の変化に合わせて更新する3つの方法と、JavaScript を切ったときにどこまで残るのか。
sidebar:
  order: 7
---

[パフォーマンス](/ja/guides/architecture/performance/)では、リクエストを処理するときの
サーバー負荷を説明しました。ここで注目するのは、サーバー側の計測だけでは見えない
2つのコストです。通信量と、利用者が空の画面を見ながら待つ時間です。

この2つは別の問題です。描画が安くても、上に載っている1つのクエリが遅ければページが
使えるようになるまで2秒かかります。マイクロ秒で描画できても、20分前から開いている
ナビゲーションバーを毎回送り直していれば、それは無駄なままです。Popcorn Web には
それぞれに対応する仕組みがあり、さらに「新しいことを知るのがサーバの側」という3つ目
の場合のための仕組みがあります。

## 静的 HTML より速い

静的ファイルは計算処理がなく、長期間キャッシュでき、エッジからも配信できます。
Web フレームワークの速度を比べるうえで、基準にしやすい方式です。

それでも**2回目のページ表示では**、このフレームワークが送るものより厳密に劣ります。

<figure>
<svg viewBox="0 0 700 205" role="img" aria-label="2回目のページ表示で転送されるものを比べた2本のバー。静的サイトはドキュメント全体を送り、そのうち変わったのは末尾の6分の1だけで、残りはブラウザがすでに持っているレイアウト・ナビゲーション・フッタ。Popcorn Web は変わった部分だけを送る。">
  <g fill="currentColor" font-family="inherit">
    <text x="0" y="34" font-size="12" opacity="0.75">静的サイト</text>
    <text x="0" y="50" font-size="11" opacity="0.5">2回目の表示</text>
    <text x="0" y="124" font-size="12" opacity="0.75">Popcorn Web</text>
    <text x="0" y="140" font-size="11" opacity="0.5">2回目の表示</text>
  </g>
  <g fill="currentColor">
    <rect x="175" y="24" width="420" height="26" rx="3" opacity="0.16"/>
    <rect x="595" y="24" width="80" height="26" rx="3"/>
    <rect x="175" y="114" width="80" height="26" rx="3"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">
    <text x="385" y="70" text-anchor="middle">レイアウト・ナビゲーション・フッタ — ブラウザはもう開いている</text>
    <text x="635" y="16" text-anchor="middle">変わった分</text>
    <text x="215" y="106" text-anchor="middle">変わった分</text>
  </g>
  <line x1="595" y1="20" x2="595" y2="54" stroke="currentColor" stroke-width="1" stroke-dasharray="3 3" opacity="0.45"/>
  <line x1="255" y1="110" x2="255" y2="144" stroke="currentColor" stroke-width="1" stroke-dasharray="3 3" opacity="0.45"/>
  <text x="175" y="180" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">静的サーバはこれ以上減らせない。目の前のブラウザが何を持っているかを知る手段が無いからだ。</text>
  <text x="175" y="196" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">知っているページは差分で答えられる — そして求められればただのドキュメントを返す。</text>
</svg>
</figure>

理屈はこれだけですが、なぜ成り立つのかは正確に押さえておく価値があります。静的
サーバがどのリクエストにも同じバイト列を返すのは、そうするしかないからです。
`/orders?page=2` を要求する2つのブラウザのうち、一方は `/orders?page=1` から来た
かもしれず、他方はブックマークを開いただけかもしれません。差分を送るにはどちらか
を知る必要があります。Popcorn Web は知っています。ブラウザがリクエストで告げる
からで、だから答えはドキュメントではなく変わった領域になります。

この種の高速化では、別の場所にコストが移っていることも少なくありません。
クライアントサイドルータ、ハイドレーション、ビルド工程、バンドルが届くまで何も
出ないページ。ここにはそのどれもありません。ブラウザに届くのは最初のバイトから
最後までサーバ描画された HTML で、関わるスクリプトは完成したマークアップを所定の
位置へ移すもの1つだけです。それを切れば、出発点だった静的ファイルの挙動に戻ります。
詳しくは[後述](#javascript-を切ると何が残るか)します。

## 3つの仕組みと、3つの問い

3つが分かれるのは「**サーバがブラウザの知らないことを知るのはいつか**」だけです。
選ぶのに必要な問いもこれ1つです。

<figure>
<svg viewBox="0 0 700 240" role="img" aria-label="0秒から4秒までの共通軸に載せた3つの例示タイムライン。非同期レンダリングは1回リクエストし、0.1秒でシェル、0.9秒と1.5秒で領域を受け取る。部分更新は0秒と2.2秒にリクエストし、0.5秒でページ全体、2.5秒で小さな差分を受け取る。ライブレンダリングは接続を1本開き、1秒、2.4秒、3.6秒に更新を受け取る。">
  <g fill="currentColor" font-family="inherit">
    <text x="0" y="36" font-size="12" opacity="0.8">非同期レンダリング</text>
    <text x="0" y="52" font-size="11" opacity="0.5">1レスポンス</text>
    <text x="0" y="96" font-size="12" opacity="0.8">部分更新</text>
    <text x="0" y="112" font-size="11" opacity="0.5">2リクエスト</text>
    <text x="0" y="156" font-size="12" opacity="0.8">ライブレンダリング</text>
    <text x="0" y="172" font-size="11" opacity="0.5">開いた接続1本</text>
  </g>
  <g stroke="currentColor" stroke-width="1.5" opacity="0.2">
    <line x1="150" y1="40" x2="650" y2="40"/>
    <line x1="150" y1="100" x2="650" y2="100"/>
    <line x1="150" y1="160" x2="650" y2="160"/>
  </g>
  <g stroke="currentColor" stroke-width="1" stroke-dasharray="3 3" opacity="0.55">
    <line x1="150" y1="22" x2="150" y2="52"/>
    <line x1="150" y1="82" x2="150" y2="112"/>
    <line x1="425" y1="82" x2="425" y2="112"/>
    <line x1="150" y1="142" x2="150" y2="172"/>
  </g>
  <g fill="currentColor">
    <rect x="158" y="33" width="10" height="14" rx="2"/>
    <rect x="257" y="33" width="10" height="14" rx="2"/>
    <rect x="333" y="33" width="10" height="14" rx="2"/>
    <rect x="207" y="93" width="42" height="14" rx="2"/>
    <rect x="457" y="93" width="12" height="14" rx="2"/>
    <rect x="150" y="153" width="450" height="14" rx="3" opacity="0.14"/>
    <rect x="270" y="153" width="10" height="14" rx="2"/>
    <rect x="445" y="153" width="10" height="14" rx="2"/>
    <rect x="595" y="153" width="10" height="14" rx="2"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="10" opacity="0.65" text-anchor="middle">
    <text x="163" y="20">シェル</text>
    <text x="262" y="20">領域</text>
    <text x="338" y="20">領域</text>
    <text x="228" y="80">全ページ</text>
    <text x="463" y="80">差分</text>
    <text x="275" y="140">更新</text>
    <text x="450" y="140">更新</text>
    <text x="600" y="140">更新</text>
  </g>
  <line x1="150" y1="194" x2="650" y2="194" stroke="currentColor" stroke-width="1" opacity="0.4"/>
  <g stroke="currentColor" stroke-width="1" opacity="0.4">
    <line x1="150" y1="194" x2="150" y2="200"/>
    <line x1="275" y1="194" x2="275" y2="200"/>
    <line x1="400" y1="194" x2="400" y2="200"/>
    <line x1="525" y1="194" x2="525" y2="200"/>
    <line x1="650" y1="194" x2="650" y2="200"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="11" opacity="0.65" text-anchor="middle">
    <text x="150" y="216">0</text>
    <text x="275" y="216">1秒</text>
    <text x="400" y="216">2秒</text>
    <text x="525" y="216">3秒</text>
    <text x="650" y="216">4秒</text>
  </g>
  <text x="650" y="234" fill="currentColor" font-family="inherit" font-size="10" text-anchor="end" opacity="0.5">例示時間 →</text>
</svg>
</figure>

時刻は説明用の例ですが、出来事の違いは実際のものです。破線はブラウザが訊いた時点です。
非同期レンダリングは1回訊き、1つのレスポンスを分割して受け取ります。部分更新はもう一度
訊き、2回目にはずっと小さい応答を受け取ります。ライブレンダリングは1回訊き、薄い線で
示した接続を開いたまま、サーバに新しい情報が生まれたときだけ印の位置で受け取ります。

2番目と3番目の選び分けは、仕組みの話ではなく URL の話です。**URL に置ける状態は
URL に置く** —— 並び順、ページ番号、絞り込み。そうすればページは共有でき、戻る
ボタンも機能し、部分更新でも追加コードなしで扱えます。ライブレンダリングを使う
のは、新しい情報を結びつけるリクエストがそもそも無いときです。誰もキーを叩いて
いない間にサーバ側で起きたことだからです。

## 非同期レンダリング —— 最初の表示を早くする

ページはたいてい、いちばん遅いクエリと同じだけ遅くなります。ハンドラが全部を待ち、
テンプレートが1回描画され、最後の依存が答えるまで読み手は白いタブを見ています。

`{await}` ブロックがこの結合を断ちます。その区画はまずフォールバックを描いて
レスポンスを確定させ、値が揃ったところで完成したマークアップが**同じレスポンスの中**
に書き込まれてフォールバックと入れ替わります。

<figure>
<svg viewBox="0 0 700 265" role="img" aria-label="1本のストリーミングレスポンスの中身を順に並べた図。ステータス行とヘッドとシェル、フォールバックを持つ2つのプレースホルダ div、そして0.9秒と1.5秒に届く2つの template 要素。">
  <rect x="20" y="14" width="480" height="200" rx="8" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.45"/>
  <text x="34" y="34" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">1本の HTTP レスポンス</text>
  <g fill="currentColor" font-family="inherit" font-size="12">
    <rect x="38" y="44" width="444" height="26" rx="3" opacity="0.16"/>
    <text x="50" y="62" opacity="0.85">200 · &lt;head&gt; · 待たなくていいシェル</text>
    <rect x="38" y="78" width="444" height="24" rx="3" opacity="0.08"/>
    <text x="50" y="95" opacity="0.6">&lt;div id="orders"&gt; loading… &lt;/div&gt;</text>
    <rect x="38" y="106" width="444" height="24" rx="3" opacity="0.08"/>
    <text x="50" y="123" opacity="0.6">&lt;div id="recs"&gt; loading… &lt;/div&gt;</text>
    <rect x="38" y="140" width="444" height="26" rx="3" opacity="0.16"/>
    <text x="50" y="158" opacity="0.85">&lt;template for="orders"&gt; … &lt;/template&gt;</text>
    <rect x="38" y="174" width="444" height="26" rx="3" opacity="0.16"/>
    <text x="50" y="192" opacity="0.85">&lt;template for="recs"&gt; … &lt;/template&gt;</text>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="11" opacity="0.55">
    <text x="516" y="62">20 ms</text>
    <text x="516" y="158">0.9 s</text>
    <text x="516" y="192">1.5 s</text>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">
    <text x="20" y="238">2つの依存は並行に走るので、レスポンスの終わりは2.4秒ではなく1.5秒。</text>
    <text x="20" y="254">それでも、読めるようになったのは20ミリ秒の時点だ。</text>
  </g>
</svg>
</figure>

大事なのは合計時間ではありません。**ステータスコードとヘッド、そして揃っていた値が、
遅い処理の完了前にサーバを出ていく**ことです。これを得るためにハンドラは何も変わり
ません。レスポンスがストリーミングになるかどうかは、組み合わせたテンプレートの性質
だからです。

`pw.Go`、タイムアウト、そして確定済みのレスポンスの中で境界が失敗したときの扱いは
[非同期レンダリング](/ja/guides/cross-layer/async-rendering/)にあります。

## ライブレンダリング —— 誰も訊いていないとき

非同期レンダリングは遅い区画を1回届けます。チャットログ、メトリクスパネル、通知
フィードが欲しいのは逆です。サーバが何かを知り、いま誰かが見ているページの一部が
それを言うべきだ、という状況です。

多くのアプリケーションでは代わりにポーリングを使います。その問題はリクエスト数
ではありません。**そのほとんどが何も見つけない**ことです。

<figure>
<svg viewBox="0 0 700 195" role="img" aria-label="ポーリングは3件の更新を見つけるために8回リクエストを出し、うち5回は何も無い。ライブレンダリングは1本の接続を開いたまま、ちょうど3回だけ書き込まれる。">
  <g fill="currentColor" font-family="inherit">
    <text x="0" y="34" font-size="12" opacity="0.75">ポーリング</text>
    <text x="0" y="114" font-size="12" opacity="0.75">ライブレンダリング</text>
  </g>
  <g stroke="currentColor" stroke-width="1" opacity="0.45">
    <line x1="190" y1="18" x2="190" y2="48"/>
    <line x1="250" y1="18" x2="250" y2="48"/>
    <line x1="310" y1="18" x2="310" y2="48"/>
    <line x1="370" y1="18" x2="370" y2="48"/>
    <line x1="430" y1="18" x2="430" y2="48"/>
    <line x1="490" y1="18" x2="490" y2="48"/>
    <line x1="550" y1="18" x2="550" y2="48"/>
    <line x1="610" y1="18" x2="610" y2="48"/>
    <line x1="190" y1="98" x2="190" y2="128"/>
  </g>
  <g fill="currentColor">
    <rect x="196" y="24" width="10" height="18" rx="2" opacity="0.16"/>
    <rect x="256" y="24" width="10" height="18" rx="2"/>
    <rect x="316" y="24" width="10" height="18" rx="2" opacity="0.16"/>
    <rect x="376" y="24" width="10" height="18" rx="2" opacity="0.16"/>
    <rect x="436" y="24" width="10" height="18" rx="2"/>
    <rect x="496" y="24" width="10" height="18" rx="2" opacity="0.16"/>
    <rect x="556" y="24" width="10" height="18" rx="2"/>
    <rect x="616" y="24" width="10" height="18" rx="2" opacity="0.16"/>
    <rect x="196" y="104" width="460" height="18" rx="2" opacity="0.16"/>
    <rect x="256" y="104" width="10" height="18" rx="2"/>
    <rect x="436" y="104" width="10" height="18" rx="2"/>
    <rect x="556" y="104" width="10" height="18" rx="2"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">
    <text x="190" y="66">8回のリクエストのうち5回は空振り</text>
    <text x="190" y="82">しかも更新は次の周期まで待たされた</text>
    <text x="190" y="146">1本の接続へ、何かあった瞬間に書き込まれる</text>
    <text x="190" y="162">その間は何も流れない</text>
  </g>
</svg>
</figure>

ライブなソースは `external live` と宣言し、非同期の値と同じ `{await}` 節で束縛
します。だからソースを `async` から `live` に変えても、それを呼ぶテンプレートは
1つも変わりません。ハンドラに至っては何も変わらず、ブラウザは画面が開いている間、
ページ自身の URL に向けた接続を1本持ち続けます。

ソースのシグネチャ、再接続、購読がデータより長生きしたときの扱いは
[ライブレンダリング](/ja/guides/cross-layer/live-rendering/)にあります。

## 部分更新 —— 2回目以降のすべての表示

描画されるチェーンのレイアウトとページは、すでに更新の境界です。識別子と描画結果の
ダイジェストが生成時に書き込まれているので、古いダイジェストを携えて届いたリクエスト
には差分で答えられます。

普通のコンポーネントは意図的に境界**ではありません**。この判断こそが、この仕組みを
安く保っています。

<figure>
<svg viewBox="0 0 700 250" role="img" aria-label="1ページの入れ子構造。ドキュメントシェルは境界ではない。その中のレイアウトは境界で、境界ではないナビゲーションコンポーネントと、唯一変わった境界である検索ページを含む。ページの中の500行の結果一覧は普通のコンポーネントで、個別には指定されない。">
  <rect x="20" y="20" width="660" height="180" rx="8" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="5 4" opacity="0.4"/>
  <text x="36" y="40" fill="currentColor" font-family="inherit" font-size="11" opacity="0.55">ドキュメントシェル — 差分は画面上のものを再利用するので、決して境界にならない</text>
  <rect x="40" y="52" width="620" height="132" rx="8" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.6"/>
  <text x="56" y="72" fill="currentColor" font-family="inherit" font-size="12" opacity="0.75">layout.pw.html — 境界</text>
  <rect x="60" y="86" width="160" height="80" rx="6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="5 4" opacity="0.35"/>
  <text x="74" y="110" fill="currentColor" font-family="inherit" font-size="11" opacity="0.5">サイトナビ</text>
  <text x="74" y="128" fill="currentColor" font-family="inherit" font-size="11" opacity="0.5">普通の</text>
  <text x="74" y="144" fill="currentColor" font-family="inherit" font-size="11" opacity="0.5">コンポーネント</text>
  <rect x="240" y="86" width="400" height="80" rx="6" fill="currentColor" fill-opacity="0.1" stroke="currentColor" stroke-width="2"/>
  <text x="256" y="110" fill="currentColor" font-family="inherit" font-size="12" opacity="0.9">search.pw.html — 境界、そして唯一変わったもの</text>
  <rect x="256" y="122" width="368" height="32" rx="4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="5 4" opacity="0.35"/>
  <text x="270" y="143" fill="currentColor" font-family="inherit" font-size="11" opacity="0.5">500行の結果一覧 — コンポーネント1つであって500個の宛先ではない</text>
  <text x="20" y="226" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">送られるのは変わった境界のうち最も外側だけ。その上はリクエストが運んだダイジェストと一致し、その中身は一緒に付いてくる。</text>
  <text x="20" y="242" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">全コンポーネントを境界にすれば毎回のリクエストに500件が載る。普通のコンポーネントが境界でないのはそのためだ。</text>
</svg>
</figure>

ランタイムは同一オリジンのリンクと GET フォームを横取りしてページ自身の URL を
要求し直し、返ってきたものを当てます。だから検索フォームは、アプリケーション側の
JavaScript を1行も書かずに、いま開いているページを絞り込みます。どちら側にも書く
コードはありません。`enabled = true` だけで手に入るのがこれです。

残り2つの経路 —— コンポーネント1つの再描画と、変更に対してその領域を返す応答 ——
と、有効化に必須の validator key は[部分更新](/ja/guides/cross-layer/partial-updates/)
にあります。

## JavaScript を切ると何が残るか

ここで3つは本当に違います。「うまく劣化します」で丸めると、誰かの半日が飛びます。

<figure>
<svg viewBox="0 0 700 220" role="img" aria-label="スクリプトを無効にしたブラウザが各仕組みから受け取るもの。非同期レンダリングは noscript リダイレクトで訊いたうえで確定済みのドキュメントを返す。ライブレンダリングはスナップショットを返すが更新は止まる。部分更新は何も失わない。">
  <g fill="currentColor" font-family="inherit" font-size="12" opacity="0.75">
    <text x="0" y="42">非同期レンダリング</text>
    <text x="0" y="106">ライブレンダリング</text>
    <text x="0" y="170">部分更新</text>
  </g>
  <g stroke="currentColor" stroke-width="1.5" opacity="0.45" fill="none">
    <path d="M140 37 L188 37"/><path d="M188 37 l-8 -4 l0 8 z" fill="currentColor" stroke="none"/>
    <path d="M140 101 L188 101"/><path d="M188 101 l-8 -4 l0 8 z" fill="currentColor" stroke="none"/>
    <path d="M140 165 L188 165"/><path d="M188 165 l-8 -4 l0 8 z" fill="currentColor" stroke="none"/>
  </g>
  <rect x="200" y="18" width="480" height="40" rx="6" fill="currentColor" fill-opacity="0.1" stroke="currentColor" stroke-width="2"/>
  <text x="216" y="35" fill="currentColor" font-family="inherit" font-size="11" opacity="0.9">確定済みのドキュメント、同じパスで</text>
  <text x="216" y="51" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">1ページ目の1往復と、progressive delivery を手放す</text>
  <rect x="200" y="82" width="480" height="40" rx="6" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.5"/>
  <text x="216" y="99" fill="currentColor" font-family="inherit" font-size="11" opacity="0.7">本物のスナップショット、そこで止まる</text>
  <text x="216" y="115" fill="currentColor" font-family="inherit" font-size="11" opacity="0.55">スクリプト無しでサーバから押し込む手段は無いので、失われるのは更新のほう</text>
  <rect x="200" y="146" width="480" height="40" rx="6" fill="currentColor" fill-opacity="0.1" stroke="currentColor" stroke-width="2"/>
  <text x="216" y="163" fill="currentColor" font-family="inherit" font-size="11" opacity="0.9">失うものは何も無い</text>
  <text x="216" y="179" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">リンクは辿れ、GET フォームは送信でき、戻るも効く — 最初からあった道だ</text>
</svg>
</figure>

部分更新だけが例外で、その理由は注意深さではなく構造です。**維持すべき
フォールバック実装が存在しません**。ランタイムは何も置き換えていないからです。
リンクは `<a href>` であり、絞り込みは `<form method="get">` であって、ランタイムは
そのマークアップがもともと自力でやることの最適化にすぎません。更新ヘッダを持たない
リクエストには普通のドキュメントが返るので、クローラも `curl` もスクリプトを切った
ブラウザも影響を受けません。横取りする側もこの基準で測られます。横取りした操作は
すべて、ブラウザが自力で辿り着いたはずの行き先に辿り着かなければなりません。

非同期レンダリングは**訊く**必要があります。スクリプトを切ったブラウザはクローラでは
なく、そうと分かる情報を何も送ってこないからです。スクリプトが無効なときにだけ動く
HTML 機能は `<noscript>` ひとつなので、ストリーミングするページの head にブロックを
寄与し、**同じページ**をマーカー付きクエリで指してリダイレクトさせます。マーカー付きの
リクエストはバッファ経路で描画されます。読み手は求めたページに、完全な形で、同じ
パスで着きます。

手放すのは progressive delivery と、訪問の1ページ目の1往復だけです。cookie が残りの
ページ分の答えを覚え、スクリプトが動くブラウザはこの一切を見ません。`scriptless_detection = false`
で切れますし、`streaming = false` という以前からのサイト全体の答えもあります（そちらは
全員の早期描画を手放します）。

救えないのはライブレンダリングのほうで、救えるふりをすべきでもありません。訊かれずに
配ることが機能そのものだからです。ただしゼロでもありません。バッファ経路は live 境界を
**最初に配信された値で**描画するので、スクリプトを切った読み手にも、解決しない
フォールバックではなく本物のスナップショットが出ます。

## どの機能も使わないほうがよい場面

100ミリ秒でリロードが終わるページには、どれも要りません。それぞれが本物の代償を
伴います —— ドキュメントごとのブラウザランタイム、配布する秘密、再描画は副作用を
持ってはいけないという規則、開いた画面ごとの接続。誰も気づかないリロードを消すため
に払う額ではありません。

非同期レンダリングが適するのは、**1つの区画が**ページの他の部分より明確に遅いとき
であって、ページ全体が遅いときではありません。全部が遅いなら、直すのはクエリです。
部分更新は、リロードそのものが苦情の対象になるほど頻繁に更新される画面から。ライブ
レンダリングは、代替が「ほとんど空振りするポーリング」になるときです。

そして入れ替えをアプリケーション側で持ちたいなら、どれも正しい形ではありません。
[フラグメントとアイランド](/ja/guides/interactivity/fragments/)がテンプレートを1つ
描き、ネゴシエーションも順序保証も無しに、選んだライブラリへ渡します。
