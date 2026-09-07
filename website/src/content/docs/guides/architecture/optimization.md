---
title: Incremental HTML updates
description: Three ways to deliver HTML progressively, after navigation, or when server state changes—and exactly how much survives with JavaScript turned off.
sidebar:
  order: 7
---

[Performance](/guides/architecture/performance/) is about the work a request
costs the server. This page is about the two costs the server never sees: the
bytes that go on the wire, and the time a reader spends looking at nothing.

They are separate problems. A page can be cheap to render and still take two
seconds to become useful, because one query on it is slow. A page can render in
microseconds and still re-send a navigation bar the browser has had open for
twenty minutes. Popcorn Web has one mechanism for each, plus a third for the
case where the server is the one that learns something new.

## Better than static HTML

A static file is the floor everyone measures a web framework against. Nothing to
compute, cache it forever, serve it from an edge. It is hard to argue with.

It is also, on the second page view, strictly worse than what this framework
sends.

<figure>
<svg viewBox="0 0 700 205" role="img" aria-label="Two bars comparing what the second page view transfers. The static site transfers a full document, of which only the last sixth is the part that changed; the rest is layout, navigation and footer the browser already holds. Popcorn Web transfers only the part that changed.">
  <g fill="currentColor" font-family="inherit">
    <text x="0" y="34" font-size="12" opacity="0.75">A static site</text>
    <text x="0" y="50" font-size="11" opacity="0.5">second page view</text>
    <text x="0" y="124" font-size="12" opacity="0.75">Popcorn Web</text>
    <text x="0" y="140" font-size="11" opacity="0.5">second page view</text>
  </g>
  <g fill="currentColor">
    <rect x="175" y="24" width="420" height="26" rx="3" opacity="0.16"/>
    <rect x="595" y="24" width="80" height="26" rx="3"/>
    <rect x="175" y="114" width="80" height="26" rx="3"/>
  </g>
  <g fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">
    <text x="385" y="70" text-anchor="middle">layout, navigation, footer — already open in the browser</text>
    <text x="635" y="16" text-anchor="middle">changed</text>
    <text x="215" y="106" text-anchor="middle">changed</text>
  </g>
  <line x1="595" y1="20" x2="595" y2="54" stroke="currentColor" stroke-width="1" stroke-dasharray="3 3" opacity="0.45"/>
  <line x1="255" y1="110" x2="255" y2="144" stroke="currentColor" stroke-width="1" stroke-dasharray="3 3" opacity="0.45"/>
  <text x="175" y="180" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">The static server cannot send less. It has no idea what this particular browser is already holding.</text>
  <text x="175" y="196" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">A page that knows sends the difference, and still serves a plain document to anything that asks.</text>
</svg>
</figure>

That is the whole argument, and it is worth being precise about why it holds. A
static server answers every request identically because it has to: two browsers
asking for `/orders?page=2` get the same bytes, and one of them may have arrived
from `/orders?page=1` while the other opened a bookmark. Sending the difference
would require knowing which. Popcorn Web knows, because the browser tells it in
the request, and the answer is the changed region rather than the document.

The catch a claim like this usually hides is that you have paid for it somewhere
else — a client-side router, a hydration pass, a build step, a page that shows
nothing until a bundle loads. There is none of that here. What reaches the
browser is server-rendered HTML from first byte to last, and the one script
involved moves finished markup into place. Turn it off and you are back to the
static-file behavior you started with, which is covered in full
[below](#what-survives-with-javascript-off).

## Three mechanisms, three questions

The three differ in *when the server knows something the browser does not*, and
that is the only question you need to pick between them.

<figure>
<svg viewBox="0 0 700 240" role="img" aria-label="Three example timelines on a shared axis from zero to four seconds. Async rendering makes one request and receives the shell at 0.1 seconds, then two regions at 0.9 and 1.5 seconds. Partial updates make requests at zero and 2.2 seconds, receiving a full page at 0.5 seconds and a small delta at 2.5 seconds. Live rendering opens one connection and receives updates at 1, 2.4, and 3.6 seconds.">
  <g fill="currentColor" font-family="inherit">
    <text x="0" y="36" font-size="12" opacity="0.8">Async rendering</text>
    <text x="0" y="52" font-size="11" opacity="0.5">one response</text>
    <text x="0" y="96" font-size="12" opacity="0.8">Partial updates</text>
    <text x="0" y="112" font-size="11" opacity="0.5">two requests</text>
    <text x="0" y="156" font-size="12" opacity="0.8">Live rendering</text>
    <text x="0" y="172" font-size="11" opacity="0.5">one open connection</text>
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
    <text x="163" y="20">shell</text>
    <text x="262" y="20">region</text>
    <text x="338" y="20">region</text>
    <text x="228" y="80">full page</text>
    <text x="463" y="80">delta</text>
    <text x="275" y="140">update</text>
    <text x="450" y="140">update</text>
    <text x="600" y="140">update</text>
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
    <text x="275" y="216">1s</text>
    <text x="400" y="216">2s</text>
    <text x="525" y="216">3s</text>
    <text x="650" y="216">4s</text>
  </g>
  <text x="650" y="234" fill="currentColor" font-family="inherit" font-size="10" text-anchor="end" opacity="0.5">illustrative time →</text>
</svg>
</figure>

The times are illustrative, but the events are the distinction. A dashed tick
is the browser asking. Async rendering asks once and receives one response in
parts. Partial updates ask again and receive a much smaller second answer. Live
rendering asks once, keeps the pale connection open, and receives a mark only
when the server has something new.

The rule for choosing between the second and the third is about the URL, not
about the mechanism. **State that can live in the URL belongs there** — a sort
order, a page number, a filter — because that makes the page shareable and
back-navigable, and partial updates handle it with no code. Reach for live
rendering when there is no request to attach the new information to, because
what changed happened on the server while nobody was typing.

## Async rendering — making the first view early

A page is usually as slow as its slowest query. The handler waits for
everything, the template renders once, and the reader gets a blank tab until the
last dependency answers.

An `{await}` block breaks that coupling. The section renders a fallback
immediately and the response commits with it; when the value settles, the
finished markup is written into the same response and takes the fallback's
place.

<figure>
<svg viewBox="0 0 700 265" role="img" aria-label="The contents of one streamed HTTP response, in order: the status line, head and shell; two placeholder divs holding fallbacks; then two template elements carrying the settled markup, arriving at 0.9 and 1.5 seconds; then an end marker.">
  <rect x="20" y="14" width="480" height="200" rx="8" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.45"/>
  <text x="34" y="34" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">one HTTP response</text>
  <g fill="currentColor" font-family="inherit" font-size="12">
    <rect x="38" y="44" width="444" height="26" rx="3" opacity="0.16"/>
    <text x="50" y="62" opacity="0.85">200 · &lt;head&gt; · the shell that does not wait</text>
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
    <text x="20" y="238">The two dependencies run concurrently, so the response ends at 1.5 s rather than 2.4 s.</text>
    <text x="20" y="254">But it became readable at 20 ms.</text>
  </g>
</svg>
</figure>

The important property is not the total. It is that the status code, the head,
and every value that was ready leave the server before the slow work finishes.
Nothing about the handler changes to get this: whether a response streams is a
property of the templates it composed.

[Async Rendering](/guides/cross-layer/async-rendering/) covers `pw.Go`, the
timeouts, and what a failed boundary does to a response that has already
committed.

## Live rendering — when nobody asked

Async rendering delivers a slow section once. A chat log, a metrics panel and a
notification feed want the opposite: the server learns something, and a region
of a page somebody is already looking at should say so.

The alternative most applications reach for is polling, and its cost is not the
requests — it is that almost all of them find nothing.

<figure>
<svg viewBox="0 0 700 195" role="img" aria-label="Polling issues eight requests to discover three updates; five of the responses report nothing new. Live rendering holds one connection open and is written to exactly three times.">
  <g fill="currentColor" font-family="inherit">
    <text x="0" y="34" font-size="12" opacity="0.75">Polling</text>
    <text x="0" y="114" font-size="12" opacity="0.75">Live rendering</text>
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
    <text x="190" y="66">eight requests, five of which found nothing</text>
    <text x="190" y="82">the update still waited for the next tick</text>
    <text x="190" y="146">one connection, written the moment there is something</text>
    <text x="190" y="162">nothing travels in between</text>
  </g>
</svg>
</figure>

A live source is declared `external live` and bound in the same `{await}` clause
an async value is, so a source that changes from `async` to `live` changes no
template that calls it. The handler changes nothing at all, and the browser
holds one connection back to the page's own URL for as long as the screen is
open.

[Live Rendering](/guides/cross-layer/live-rendering/) covers the source
signature, reconnection, and what happens when a subscription outlives its data.

## Partial updates — every view after the first

Every layout and page of a rendered chain is already an update boundary, with an
identity and a digest of what it rendered written onto it by generation. A
request arriving with the old digests can be answered with the difference.

An ordinary component is deliberately *not* a boundary, and that decision is
what keeps the mechanism cheap.

<figure>
<svg viewBox="0 0 700 250" role="img" aria-label="A nested diagram of one page. The document shell is not a boundary. Inside it the layout is a boundary, containing a navigation component that is not one, and the search page, which is a boundary and is the only part that changed. Inside the page, a five-hundred-row result list is an ordinary component and is not addressed separately.">
  <rect x="20" y="20" width="660" height="180" rx="8" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="5 4" opacity="0.4"/>
  <text x="36" y="40" fill="currentColor" font-family="inherit" font-size="11" opacity="0.55">the document shell — never a boundary, because a delta reuses the one on screen</text>
  <rect x="40" y="52" width="620" height="132" rx="8" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.6"/>
  <text x="56" y="72" fill="currentColor" font-family="inherit" font-size="12" opacity="0.75">layout.pw.html — a boundary</text>
  <rect x="60" y="86" width="160" height="80" rx="6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="5 4" opacity="0.35"/>
  <text x="74" y="110" fill="currentColor" font-family="inherit" font-size="11" opacity="0.5">site navigation</text>
  <text x="74" y="128" fill="currentColor" font-family="inherit" font-size="11" opacity="0.5">an ordinary</text>
  <text x="74" y="144" fill="currentColor" font-family="inherit" font-size="11" opacity="0.5">component</text>
  <rect x="240" y="86" width="400" height="80" rx="6" fill="currentColor" fill-opacity="0.1" stroke="currentColor" stroke-width="2"/>
  <text x="256" y="110" fill="currentColor" font-family="inherit" font-size="12" opacity="0.9">search.pw.html — a boundary, and the only one that changed</text>
  <rect x="256" y="122" width="368" height="32" rx="4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="5 4" opacity="0.35"/>
  <text x="270" y="143" fill="currentColor" font-family="inherit" font-size="11" opacity="0.5">a 500-row result list — one component, not 500 addresses</text>
  <text x="20" y="226" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">Only the outermost changed boundary travels. Everything above it matched the digests the request carried, and everything inside it came along.</text>
  <text x="20" y="242" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">Making every component a boundary would put five hundred entries in every request, which is why an ordinary component is not one.</text>
</svg>
</figure>

The runtime intercepts a same-origin link or a GET form, re-requests the page's
own URL, and applies what comes back — so a search form refines the page it is on
with no application JavaScript at all. Nothing is written on either side; this is
what `enabled = true` buys on its own.

[Partial Updates](/guides/cross-layer/partial-updates/) covers the two other
paths — redrawing one component, and answering a mutation with the regions it
changed — along with the validator key that enabling this requires.

## What survives with JavaScript off

Here the three genuinely differ, and rounding that off to "it degrades
gracefully" would cost somebody an afternoon.

<figure>
<svg viewBox="0 0 700 220" role="img" aria-label="What a browser with scripting disabled receives from each mechanism. Async rendering asks through a noscript redirect and then serves the settled document, at the cost of one extra round trip. Live rendering serves a snapshot but stops updating. Partial updates lose nothing at all.">
  <g fill="currentColor" font-family="inherit" font-size="12" opacity="0.75">
    <text x="0" y="42">Async rendering</text>
    <text x="0" y="106">Live rendering</text>
    <text x="0" y="170">Partial updates</text>
  </g>
  <g stroke="currentColor" stroke-width="1.5" opacity="0.45" fill="none">
    <path d="M140 37 L188 37"/><path d="M188 37 l-8 -4 l0 8 z" fill="currentColor" stroke="none"/>
    <path d="M140 101 L188 101"/><path d="M188 101 l-8 -4 l0 8 z" fill="currentColor" stroke="none"/>
    <path d="M140 165 L188 165"/><path d="M188 165 l-8 -4 l0 8 z" fill="currentColor" stroke="none"/>
  </g>
  <rect x="200" y="18" width="480" height="40" rx="6" fill="currentColor" fill-opacity="0.1" stroke="currentColor" stroke-width="2"/>
  <text x="216" y="35" fill="currentColor" font-family="inherit" font-size="11" opacity="0.9">the settled document, at the same path</text>
  <text x="216" y="51" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">one extra round trip on the first page, and the progressive delivery</text>
  <rect x="200" y="82" width="480" height="40" rx="6" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.5"/>
  <text x="216" y="99" fill="currentColor" font-family="inherit" font-size="11" opacity="0.7">a real snapshot, and then it stops</text>
  <text x="216" y="115" fill="currentColor" font-family="inherit" font-size="11" opacity="0.55">no non-script way for a server to push exists, so the updating is what is lost</text>
  <rect x="200" y="146" width="480" height="40" rx="6" fill="currentColor" fill-opacity="0.1" stroke="currentColor" stroke-width="2"/>
  <text x="216" y="163" fill="currentColor" font-family="inherit" font-size="11" opacity="0.9">nothing at all is lost</text>
  <text x="216" y="179" fill="currentColor" font-family="inherit" font-size="11" opacity="0.6">links navigate, GET forms submit, back works — this is the path that was always there</text>
</svg>
</figure>

Partial updates are the outlier, and the reason is structural rather than
careful. There is no fallback implementation to maintain, because the runtime
never replaced anything: a link is an `<a href>` and a filter is a
`<form method="get">`, and the runtime is an optimization of what that markup
already does by itself. A request that carries no update header is answered with
the ordinary document, so a crawler, `curl`, and a browser with scripting
disabled are all unaffected. That is also the standard the intercepting half is
held to — every gesture it takes over has to reach the destination the browser
would have reached on its own.

Async rendering has to ask, because a browser with scripting off is not a
crawler and sends nothing that says so. `<noscript>` is the one HTML feature
that fires precisely when scripting is off, so the framework contributes a block
to the head of a streamed page that redirects to *that same page* under a marker
parameter — and the marked request renders buffered. The reader lands on the
page they asked for, complete, at the same path. What they give up is the
progressive delivery and one round trip on the first page of a visit; a cookie
remembers the answer for the rest of it, and a scripted browser never sees any
of it. Turn it off with `scriptless_detection = false`, or take the older
site-wide answer with `streaming = false`, which gives up the early paint for
everybody.

Live rendering is the one that cannot be rescued, and it should not pretend
otherwise: delivering without being asked is the feature. It is not nothing,
though. The buffered branch renders a live boundary from its first delivered
value, so a scriptless reader gets a real snapshot rather than a fallback that
never resolves.

## When not to reach for any of this

A page that reloads in a hundred milliseconds needs none of it. Each mechanism
adds something real — a browser runtime on every document, a secret to deploy, a
rule that every re-render must be free of side effects, a held-open connection
per open screen — and a reload nobody notices is not a cost worth paying to
remove.

Reach for async rendering when one section is measurably slower than the rest of
the page, not when the page as a whole is slow; if everything is slow, the fix is
in the queries. Reach for partial updates when a screen is refreshed often enough
that the reload is the thing people complain about. Reach for live rendering when
the alternative is polling that mostly finds nothing.

And when the application wants to own the swapping itself, none of these is the
right shape — [Fragments and islands](/guides/interactivity/fragments/) renders
one template with no negotiation and no ordering guarantee, and hands it to
whatever library you chose.
