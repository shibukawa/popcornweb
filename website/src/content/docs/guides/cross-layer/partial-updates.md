---
title: Partial Updates
description: Let links, forms, and JavaScript requests refresh only the server-rendered regions whose markup changed.
sidebar:
  order: 4
---

A search page already contains its header, navigation, filter form, and footer.
Change `?sort=price`, however, and an ordinary navigation downloads and parses
all of them again. Only the results needed to move.

Partial updates keep the ordinary page and change the response. A browser asking
for a document still receives the whole document. A page that already holds the
shell sends a manifest of its rendered regions, and the server answers with only
the regions whose markup differs. The URL, handler, and templates stay the same.

That makes the first experiment unusually small. Enable the feature, then click
a same-origin link or submit a GET form:

```toml
# config.dev.toml
[html]
[html.update]
enabled = true
validator_key = "${HTML_UPDATE_VALIDATOR_KEY}"
```

```html
<nav>
  <a href="/orders?sort=newest">Newest</a>
  <a href="/orders?sort=price">Lowest price</a>
</nav>

<form method="get" action="/orders">
  <input name="q" type="search" value={query}>
  <button type="submit">Search</button>
</form>
```

No click handler is needed. The link remains a link, the form remains a form,
and both still work when JavaScript is unavailable. With the runtime present,
the browser keeps the current document and applies the changed regions.

## Four ways to start an update

The visible result may be the same, but the initiator determines which API owns
the request.

| Initiator | How it starts | Typical use | Enhancement |
| --- | --- | --- | --- |
| `<a href>` | same-origin navigation | another page, sort link, pagination | automatic |
| `<form method="get">` | query-string navigation | search and filtering | automatic |
| `<form method="post">` | mutation | create, rename, validation | a component script submits with update headers and applies the response |
| JavaScript | `update`, `navigate`, `redraw`, or `fetch` | local controls and custom interaction | explicit |

Links and GET forms are intercepted because the browser's fallback has the same
meaning as the enhanced request. POST is different. The runtime does not
silently take ownership of every unsafe form; the application opts in where it
can also decide loading, error, and CSRF behavior.

This component script enhances a POST form while preserving its ordinary
submission when updates are disabled:

```html
export component RenameForm(orderID: string): html {
<script component>
  export function setup({ el: form, teardown }) {
    if (!window.popcornweb) return;

    async function submit(event) {
      event.preventDefault();
      const response = await fetch(form.action, {
        method: "POST",
        headers: window.popcornweb.updateHeaders(),
        credentials: "same-origin",
        body: new FormData(form),
      });
      await window.popcornweb.apply(response);
    }

    form.addEventListener("submit", submit);
    teardown(() => form.removeEventListener("submit", submit));
  }
</script>
<form method="post" action="/orders/rename">
  <input type="hidden" name="order_id" value={orderID}>
  <input name="name" required>
  <button type="submit">Rename</button>
</form>
}
```

`updateHeaders()` marks the request as an action update and includes the current
CSRF header when configured. `apply()` accepts the regions returned by the
handler, including validation regions carried by a 4xx response. The listener is
released through `teardown` rather than by returning a cleanup function, because
what a `setup` returns is the set of handlers the component publishes — see
[Component scripts](/guides/interactivity/component-scripts/#release-happens-before-the-replacement-lands).
That is the whole reason to write this in a component script: replacing the form
does not leave a duplicate submit listener behind.

JavaScript can also initiate each path directly:

```js
if (window.popcornweb) {
  await window.popcornweb.update({ sort: "newest" });
  await window.popcornweb.navigate("/orders/17");
  await window.popcornweb.redraw("card-17", { orderID: 17 });
}
```

`update()` replaces the whole query string, just as a GET form does. Read and
pass back any parameters that should survive.

An array value becomes one pair per element — `{ tag: ["boots", "hats"] }`
writes `?tag=boots&tag=hats` — which is the repeated key a checkbox group
submits and the only array spelling the server reads. An empty array writes
nothing, the same query a form with no box checked sends. Joining the elements
into one comma-separated value would produce a query no form could have written,
and a comma inside an element could never be told from the join.

## One route, two kinds of response

The optimization does not introduce a second page implementation. It adds a
second representation of the same render.

![Two flows through one handler. In blue, the browser requests the page, the handler renders the whole chain, and the browser paints a new page. In green, the runtime adds the render and manifest headers, the same handler compares each region with the digest already held by the browser, returns replacement instructions, and the runtime applies them to the live DOM.](../../../../assets/diagrams/partial-update-sequence.svg)

Without update headers, the response is a document. The browser parses it and
replaces the page. Crawlers, `curl`, disabled JavaScript, and a proxy that strips
the enhancement headers all take this path.

With update headers, the request carries the current region manifest. The server
renders the same route, compares each boundary digest, and returns replacement
instructions plus new markup only where the digest changed. The runtime applies
those instructions to the DOM already on screen.

The distinction explains both the speed and the resilience. Unchanged markup
does not cross the network or get reparsed, while the complete-document path
remains the fallback rather than becoming a separate implementation to maintain.

## How much smaller is the response?

The useful estimate is based on changed markup, not the number of components.
Let `D` be the uncompressed document body, `R` the markup of changed regions,
and `O` the small update envelope and manifest. A reload transfers `D`; a
partial response transfers roughly `R + O`.

The following numbers are illustrative, not a benchmark. Assume a rendered
document of 50 KiB: 34 KiB of shell and navigation, 14 KiB of results, and a
2 KiB order summary. HTTP headers and compression are excluded.

| What changed | Full document | Approximate partial body | Markup avoided |
| --- | ---: | ---: | ---: |
| results after sorting | 50 KiB | 14 KiB + `O` | about 72% |
| one order summary | 50 KiB | 2 KiB + `O` | about 96% |
| results and summary | 50 KiB | 16 KiB + `O` | about 68% |
| no boundary markup | 50 KiB | `O` only | nearly 100% |

Compression changes the bytes on the wire, and small responses make `O` more
visible. The direction remains useful: a smaller boundary saves more transfer
and DOM work when it changes independently.

There is an important limit. Navigation still renders the page chain so the
server can compare digests; a smaller response does not automatically mean
less server CPU. A targeted redraw can skip unrelated data loading, which is
where the server-side saving appears.

## Boundaries are the unit of comparison

A boundary answers one question: if this rendered unit changed, what is the
smallest region the server can replace safely?

Layouts and pages in a `.pw.html` render chain are boundaries automatically.
Generation writes an identity on each boundary root and computes a digest for
its rendered markup. An ordinary nested component is deliberately not a
boundary. If every row in a 500-row table were one, every navigation request
would carry 500 manifest entries before anything changed.

Start with the automatic page and layout boundaries. Add a finer boundary only
when all three statements are true:

1. The region changes independently often enough to matter.
2. It has one stable root that can be replaced without taking neighboring UI.
3. Its inputs can be reconstructed safely for a direct redraw.

The third condition is stricter than it first appears. URL state such as a sort,
filter, or page number should stay in the URL and use navigation. Browser-local
state that should not be shared may justify a reloadable component.

```html
package templates

@reloadable
export component OrderCard(id: string, orderID: int): html {
<article class="card">
  <h3>Order {orderID}</h3>
</article>
}
```

A reloadable component must be exported, have exactly one root, and declare the
`id` its caller writes. Every other argument must have a deterministic query
representation. Records, slices, and `html` are rejected at generation because
a redraw request could not reconstruct them from a URL.

This produces a practical hierarchy:

- The **layout boundary** protects the document shell shared by several pages.
- The **page boundary** replaces route-specific content.
- A **reloadable component boundary** is reserved for an independently redrawn
  region with URL-serializable inputs.
- Ordinary components remain implementation detail inside their nearest
  boundary.

Smaller is not always better. Boundary count enlarges the manifest, and a
boundary that renders a clock or random value never matches. Put continuously
changing data in [Live Rendering](/guides/cross-layer/live-rendering/) or let the
browser format it.

## Navigation, redraw, and action

The three response paths differ by where the changed input lives.

### Navigation: the request owns the state

A same-origin link or GET form changes a route or query. The runtime requests
that URL, the handler renders normally, and the server returns changed
boundaries. This is the default path and needs no application JavaScript.

State that can live in the URL belongs here. Search terms, sorting, filters, and
pagination then remain shareable, bookmarkable, and compatible with back and
forward navigation.

### Redraw: the browser owns the state

`redraw(id, parameters)` asks one reloadable component to render again without
executing the whole page. A classic handler can answer redraws before unrelated
queries run:

```go
func Orders(w http.ResponseWriter, r *http.Request) {
    if !pw.Authenticated(r) {
        pw.WriteProblem(w, r, pw.Unauthorized())
        return
    }
    if pw.Redraw(w, r, templates.OrdersPage) {
        return
    }

    orders, err := loadOrders(r.Context())
    if err != nil {
        pw.WriteProblem(w, r, err)
        return
    }
    pw.WriteHTMLPage(w, r, nil,
        templates.OrdersPage(templates.OrdersPageParams{Orders: orders}))
}
```

The page is named, not called. Place `pw.Redraw` below authorization and above
expensive page data loading. That preserves the page's security boundary while
skipping work the component does not need.

Redraw arguments come from the requester. A component loading `orderID=17` must
check that the current subject may read order 17, exactly as a handler would.
Naming the page limits which component types the URL serves; it does not
authorize their arguments.

### Action: the handler owns a mutation

An action mutates once and then chooses the response representation. Keep the
mutation above the branch so enhanced and ordinary clients cannot drift:

```go
func Rename(w http.ResponseWriter, r *http.Request) {
    order, err := renameOrder(r)
    if err != nil {
        pw.WriteProblem(w, r, err)
        return
    }

    if !pw.WantsUpdate(r) {
        http.Redirect(w, r, "/orders", http.StatusSeeOther)
        return
    }
    pw.WriteUpdate(w, r, http.StatusOK,
        pw.Replace("order-summary",
            templates.Summary(templates.SummaryParams{Order: order})))
}
```

The ordinary POST takes post-redirect-get. The enhanced POST carries update
headers and receives the changed regions in the same round trip. A 4xx action
response may still carry validation regions; `apply()` applies them because
showing the rejection is the intended result. If the mutation changes the
user's destination, use `pw.WriteUpdateNavigate(w, r, "/orders/17")`.

Rendering may be superseded and discarded after the server produced it.
Therefore navigation and redraw rendering must be free of side effects.
Mutations belong in action handlers.

## What remains in place

Keeping the document means keeping state around the changed boundary: scroll
position, open controls, selection, and text the user has not submitted. Within
a replaced region, matching controls preserve their current value; a focused
control also preserves focus and caret position.

Navigation and `update()` intentionally differ. A link or GET form moves to a
new history entry, scrolls to the top or named fragment, and restores focus when
the old target disappears. `update()` changes the current route's arguments and
keeps the viewport where it is. Back and forward restore saved scroll positions
after the new regions land.

An update waits for IME composition to finish rather than replacing the control
under an unconfirmed Japanese or Chinese conversion. If a newer response
overtakes it during that wait, the older response is discarded.

Some DOM is not owned by the server at all. Preserve a map, canvas, or playing
video across a replacement with a stable key:

```html
<div data-tb-preserve="chart"><canvas></canvas></div>
```

While navigation or redraw is pending, the document root carries
`data-tb-updating`:

```css
[data-tb-updating] .results { opacity: 0.6; }
```

## When another method fits better

Partial updates are strongest when server-rendered pages remain the source of
truth and request-driven changes repeatedly replace a minority of the document.
The comparison changes when either half of that statement stops being true.

| Situation | Better fit | Why |
| --- | --- | --- |
| the page reloads quickly and rarely | ordinary navigation | no runtime, manifest, or validator secret |
| the application chooses one fragment endpoint and target | [fragments](/guides/interactivity/fragments/) or [htmx](/guides/interactivity/htmx/) | explicit swap ownership without page-boundary negotiation |
| the server learns new data without a request | [Live Rendering](/guides/cross-layer/live-rendering/) | the server can deliver repeatedly over an open connection |
| state is entirely local to one widget | a [component script](/guides/interactivity/component-scripts/) or React island | no server round trip is needed |
| most of the screen is long-lived client state | a client-rendered architecture | repeated server reconciliation is no longer the simpler owner |
| a POST must always use ordinary browser semantics | post-redirect-get | refresh, history, and failure behavior stay native |

The runtime also declines modified clicks, `target`, `download`, cross-origin
URLs, fragment-only links, and non-GET forms. Add `data-tb-ignore` to an element
or ancestor when an otherwise eligible link or GET form should remain native.

## Before enabling it broadly

- Deploy a strong `html.update.validator_key`. Startup refuses an enabled
  configuration without one; rotating it merely causes comparisons to miss and
  the next response to fall back to a complete document.
- Keep renders side-effect free because superseded responses are discarded.
- Authorize every redraw argument as untrusted request input.
- Avoid volatile markup inside broad boundaries.
- Test the same link and form with JavaScript disabled. The destination and
  mutation must remain correct before enhancement.
- Measure both transfer and server work. Navigation saves response and DOM work;
  an early redraw can also skip server queries.

For a first trial, choose a result page with a GET filter, enable updates, and
watch the response while switching sort order. The address bar and back button
still behave normally. The network response now contains the results boundary,
not another copy of the page that was already on screen.

Every configuration key and default is listed in
[Application Configuration Keys](/reference/configuration/).
