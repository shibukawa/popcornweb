# Interactivity: server actions, component scripts, signals

The client-facing half of a page tree: how markup reaches Go without naming a
URL, how a component owns JavaScript that starts and stops with its own
instances, and how a live source pushes a named instruction the page acts on.
All three are compiled by `pw generate` into `_pw_gen.go` build outputs and
extracted asset files — never edit either. Rendering mechanics (async, partial
updates, live boundaries) are in [rendering.md](rendering.md); template syntax
is in [templates.md](templates.md).

The rule underneath all of it: a page renders and a link navigates with no
JavaScript, and every feature here is an enhancement over markup the server
already sent. Nothing may be correct only because `setup` ran.

## Server actions

A page is a `GET`; a website is not. Inside a page tree a template names an
exported Go handler and generation supplies the address, so renaming the Go
function fails the build at the attribute that referenced it.

```html
package users

export component Page(id: string): html {
  <form server-action="Retire">
    <label>Reason <input type="text" name="reason" /></label>
    <button type="submit">retire</button>
  </form>
}
```

```go
package users

type retireRequest struct {
	Reason string `payload:"reason" check:"required"`
}

func Retire(w http.ResponseWriter, r *http.Request) {
	request, err := pw.Parse[retireRequest](r)
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	if err := retire(r.Context(), pw.PathValue(r, "id"), request.Reason); err != nil {
		pw.WriteProblem(w, r, err)
	}
}
```

Requirements: a page tree (`generate.pages`), the handler in the route package
beside the template, and `security.csrf` configured — `pw init` writes it.

### Put it on a form

A `<form server-action>` works with and without the browser runtime, and that is
the reason to prefer it. Generation writes `method="post"`, a hidden field
naming the handler, and the CSRF token, and deliberately **no `action`** — so a
native submit posts to the document's own URL, which already carries this page's
path parameters:

```html
<form data-tb-action="/_action/d71506d06c1e/Retire" method="post">
  <input type="hidden" name="_action" value="d71506d06c1e/Retire" />
  <input type="hidden" name="_csrf" value="…" />
```

With no JavaScript the browser posts natively and the handler answers. With the
runtime loaded the submit is intercepted, posted with `fetch`, and the response
applied as update regions. Nothing configures the choice; the runtime's presence
picks it. A form action also makes generation register `POST` on the page's own
pattern beside its `GET` — if the application already hand-registers a `POST`
there, startup panics on the duplicate.

### A bare element costs the no-script path

`server-action` is accepted on any element, and on anything that is not a form
it lowers to one attribute the runtime reads:

```html
<button server-action="Rename" data-target="#name">rename</button>
<!-- lowers to -->
<button data-tb-action="/_action/00369cf962b6/Rename" data-target="#name">rename</button>
```

With scripting off that button does nothing — nothing in HTML invokes a button
outside a form. It also posts to `/_action/<hash>/<Name>`, a compile-time
constant carrying **no path parameters**, so a handler reached that way cannot
tell which record it is about. Put a server action on a form unless the
interaction genuinely has no fields and no instance.

Every other attribute on the element survives unread: `server-action` resolves a
name to an address and models no client protocol.

### What the handler owes

An ordinary `http.HandlerFunc` owning its whole response, callable from a test
with `httptest` and no registration. Writing nothing is meaningful: the form
entry point answers `303` back to the page, so a reload resubmits nothing. Write
a status, a header, or a body and that response stands instead.

Answer each caller with what that caller can use:

```go
func Rename(w http.ResponseWriter, r *http.Request) {
	// …mutate…
	switch {
	case pw.WantsValue(r):   // a script called this by name and holds the answer
		pw.WriteAPI(w, r, renamed{Name: name})
	case pw.WantsUpdate(r):  // the runtime intercepted a gesture
		pw.WriteUpdate(w, r, http.StatusOK, pw.Replace("name", Name(NameParams{Value: name})))
	default:                 // a native submit, with a document waiting for a page
		pw.RedirectSeeOther(w, r, "/users/"+id)
	}
}
```

Ask neither question and one response goes to everybody, which is right for a
handler with nothing to return. A rejected submission returns `4xx` and the
regions it carries are the validation errors; the runtime applies them whatever
the status says. A handler that fails outright (`500`, a CSRF `403`) answers with a problem document rather than regions: nothing is applied and nothing is retried, and the runtime reports it — `console.error`, plus a `failed` event (`detail.action`, `detail.status`) to `window.popcornweb.subscribe((kind, detail) => …)` listeners, which is where an app shows its own error message.

### What is reachable, and what it grants

Every exported handler-shaped function in a route package gets an endpoint,
whether or not a template mentions it. Lowercase the ones that should not have
one — generated code in another package cannot reach an unexported symbol.
`Load` is excluded; it is the page's own entry point.

The address is `/_action/<hash>/<HandlerName>`, where the hash is the leading 12
hex digits of a digest over the **declaring directory** and the handler name.
There is no build salt, so an unchanged project reproduces the same address
across deploys. An address hides structure and grants nothing: both entry points
are publicly reachable, so each handler authenticates and authorizes its own
caller. The generated `Actions` table lists every endpoint.

### A typed function a script calls

A form has to be answered with a response; a script asking a question wants a
value. Declare an ordinary Go function and it becomes one:

```go
package users

// The declaration is what publishes it. Put it above the function.
var _ = pw.ServerAction(profile)

func profile(ctx context.Context, id string) (Profile, error) {
	return load(ctx, id)
}
```

```js
const p = await actions.profile({ id: "42" });   // a Profile, decoded
```

Because the declaration admits it, not the signature:

- **it may be unexported** — nothing is published by merely existing;
- **any signature works**, and a leading `context.Context` receives the
  request's, so the database handle and the signed-in reader are in reach;
- **it is called by its published name** — `GetUser` is `actions.getUser`; pass
  a string to `pw.ServerAction(fn, "name")` to publish a different one, which is
  what a rename must not move;
- **a template cannot name it.** `server-action="profile"` is a generation error
  naming what a script would call instead, because a form has nowhere to put a
  returned value.

Results are one value and an error, or an error alone. An error becomes a
problem response with the status the framework already maps. `pw.ServerAction`
is a package-level call, so it is written as an assignment to the blank
identifier or from `init`.

Reach for this when a script wants an answer, and for the handler shape when a
form wants a response. A page may have both.

**When not to use a server action:** an interaction that changes nothing on the
server is not one — a disclosure widget is `<details>`, a dialog is `<dialog>`,
a search form refining its own page is an ordinary `GET` form the runtime
already intercepts. Outside a page tree, write an ordinary route: actions are a
page's implementation detail, appear in no OpenAPI document, and nothing
versions them.

## Component scripts

A `<script component>` block gives one component its own module. `setup` runs
**per rendered instance**; what it registers is released when that instance goes.

```html
package shop

export component Countdown(deadline: string): html {
<script component>
  export function setup({ el, teardown }) {
    const label = el.querySelector("[data-remaining]");
    const timer = setInterval(() => {
      label.textContent = remaining(el.dataset.deadline);
    }, 1000);
    teardown(() => clearInterval(timer));
  }

  function remaining(iso) {
    const seconds = Math.max(0, (Date.parse(iso) - Date.now()) / 1000);
    return Math.floor(seconds) + "s";
  }
</script>
  <p class="countdown" data-deadline={deadline}>
    <span data-remaining>—</span>
  </p>
}
```

The block goes at the top of the declaration, beside the `head` block, before
the markup. Content is read verbatim — a brace is JavaScript, not an
interpolation. The bare `component` attribute is required; without it the
element is an ordinary `<script>` and keeps its current meaning.

Three rules generation enforces:

- **One block per component.** Two would need an order nothing declares.
- **One root element.** The marker naming the declaration is written onto it.
- **A module, and imports are absolute.** The block is extracted to a
  content-hashed file under the public tree, so a relative specifier would
  resolve against a directory the file no longer sits in. Import by URL; that is
  also how two components share code.

### The module runs once; `setup` runs per instance

The extracted file is an ES module: its top level runs once per URL for the life
of the document. Module scope is therefore wrong for anything belonging to one
instance or one visit.

```js
let count = 0;                       // shared by every instance, forever
export function setup({ el }) {
	let ownCount = 0;                  // this instance's
}
```

Everything `setup` needs arrives in one object; destructure what you use:

```js
export function setup({ el, teardown, onSignal, props, actions }) { }
```

| Key | What it is |
| --- | --- |
| `el` | this instance's root element |
| `teardown(fn)` | register a release; called any number of times, run in reverse |
| `onSignal(name, fn)` | register a signal handler scoped to this instance |
| `props` | the component parameters this block destructured (see below) |
| `actions` | one function per server action of the page's own route package |

### Release happens before the replacement lands

When a partial update or a live delivery replaces a region, the runtime tears
down every component script inside it, **then** applies the new markup, then
starts what arrived. So a teardown can still reach its nodes:

```js
export function setup({ el, teardown }) {
	const observer = new ResizeObserver(() => reflow(el));
	observer.observe(el);
	teardown(() => observer.disconnect());   // el is still in the document here
}
```

An operation that moves a region without replacing it — reordering a list —
releases nothing; the instances travel with their nodes.

### Handlers the markup can name

What `setup` returns is the set of handlers this component publishes:

```html
export component Counter(label: string): html {
<script component>
  export function setup({ el }) {
    let count = 0;
    const output = el.querySelector("output");
    return {
      increment() { output.textContent = ++count; },
    };
  }
</script>
  <div>
    <output>0</output>
    <button on-click="increment">{label}</button>
  </div>
}
```

Generation resolves each name against what the block returns and lowers every
pair on one element into `data-tb-on="click:increment,blur:settle"`. A name the
block does not return is a generation error at the attribute.

Rules on `on-<event>`:

- reserved **only inside a component declaring a script block**; anywhere else
  an `on-`prefixed attribute is emitted unread;
- a second hyphen is not matched, so `on-my-event` stays an ordinary attribute;
- the value is a name, not a call — `on-click="increment()"` is an error;
- two of the same event on one element is an error.

What varies per element is read from the DOM at event time, not at mount:

```html
<button on-click="remove" data-id={row.id}>delete</button>
```

```js
remove(event) { const id = event.currentTarget.dataset.id; }
```

`onclick` is untouched and still means inline JavaScript — which still does not
run under the default `script-src 'self'`, and that is the other reason handlers
arrive this way.

### Calling a server action from a script

```js
export function setup({ el, actions }) {
	return {
		async remove(event) {
			if (!confirm("Delete this?")) return;
			await actions.delete({ id: event.currentTarget.dataset.id });
		},
	};
}
```

`actions` holds the route package's exported handlers and everything declared
with `pw.ServerAction`, named in lowerCamelCase. Nothing names a URL — the
address holds a digest a script could not compute. The argument is sent as JSON,
which `pw.Parse` reads into the same input struct a form posts, so one handler
serves both; calling with no argument sends no body. Update regions in the
response are applied exactly as a gesture's are, and anything else is returned
to the caller. There is no in-flight marking, because no element was activated.

This is also the shape for a **gated** mutation. A template carrying both a
client handler and `server-action` runs the handler and issues the action
regardless — there is no cancellation channel. Put the handler on the element,
leave `server-action` off it, and call the action from JavaScript.

### Parameters the block asks for

```js
export function setup({ props: { deadline } }) { }
```

Destructuring a parameter from `props` makes generation emit it onto the root
element as JSON (`data-tb-props`). Only the names you destructure cross, which
makes the destructuring a declaration of what this component publishes to the
browser — read `{price}` there as putting the price in the DOM where anyone can
edit it. Treat what comes back as untrusted. An absent optional omits its key
rather than arriving as `null`, so `"deadline" in props` is the test. Values keep
their JSON types, and they are the values at mount, not a live binding.

**Cost:** one extracted file per component that declares a block, referenced
once from the merged head and cached like any static asset; per instance, one
attribute and one `setup` call.

**When not to write one:** if the browser already does it, do not — `<details>`,
`<dialog>`, `popover` all work with no script and keep working when yours
throws. If the region's *content* is what changes, that is a partial update or a
live boundary, not a script that rewrites what the server just sent.

## Signals

A live source can say *this region now shows X*. A signal is for the other kind
of message: a **name** and a JSON payload, sent from a source, dispatched to a
callback the page registered under that name. A delivery is a snapshot, so
missing one costs nothing; an instruction ("the export you started is ready") is
true once and nothing later makes it redundant.

```go
func WatchJob(ctx context.Context, id string) iter.Seq2[Job, error] {
	return func(yield func(Job, error) bool) {
		for job := range jobs.Watch(ctx, id) {
			if !yield(job, nil) {
				return
			}
			if job.Done {
				yield(Job{}, pw.NewSignal("app.finished", finished{URL: job.ResultURL}))
				return
			}
		}
	}
}
```

```js
export function setup({ onSignal }) {
	onSignal("app.finished", (event) => window.popcornweb.navigate(event.url));
}
```

A signal is yielded where an error would be and is classified before anything
treats it as one, so it **renders nothing** (no `recover` subtree, no revision),
**ends nothing** (the subscription lives and the next delivery arrives), and is
**never coalesced**. Values and signals come out of one sequence, so a signal
yielded between two deliveries arrives between them.

**Naming.** At most 64 bytes, starting with a letter, then letters, digits, dot,
underscore, or hyphen, compared byte for byte. `tb.` and `pw.` are reserved and
refused at emit — a handler trusts a `pw.` name precisely because application
code has no route into that namespace. Use a dotted prefix of your own.

**Payload.** A value with a generated encoder, encoded once at construction, and
nothing inspects it. Two consequences: whatever you put in one is public (no
projection happens, unlike a `recover` clause's four safe fields), and a shared
source sends to every subscriber, so anything reader-specific comes from a
per-subscription source.

**Delivery is best effort.** A signal produced while no connection is open is
not held, a reconnect replays nothing, and the server never learns whether the
browser dispatched one. The test is whether a reader who reloads still finds
out: the page must say "finished" in its own render, and the signal saves them
the reload. The budget is `html.live_max_signal_bytes` (256 KiB per response);
reaching it closes the response for retry rather than dropping records.

### Lifecycle names

The runtime dispatches into the same table under the `pw.` prefix:

| Name | Fires when | Carries |
| --- | --- | --- |
| `pw.document_committed` | the streamed document ended | `final`, `live_pending`, or `failed` |
| `pw.document_truncated` | parsing finished with no end marker | nothing |
| `pw.boundary_settled` | an await boundary's content is in the DOM | the boundary id |
| `pw.live_opened` | the live response began yielding | whether it was a reconnect |
| `pw.live_closed` | the connection ended | the reason and any retry hint |
| `pw.delivery_applied` | a live delivery landed | the boundary id, and whether the DOM changed |
| `pw.navigation_applied` | a navigation delta was applied | the URL now displayed |
| `pw.directive_received` | a navigate or reload directive arrived | which, and the target |

Each fires **after** the thing it describes is in the DOM, except
`pw.document_truncated`, which describes an absence. `pw.delivery_applied`
carrying `changed` matters: an identical delivery is skipped or left alone, so an
arrival is not a change.

### What a handler may be

A name resolves against the page's table and nothing else — never `eval`, never
a global lookup, never an attribute the payload names. That is what lets the
feature exist under `script-src 'self'` with no nonce. The risk is at
registration, not dispatch:

```js
onSignal("app.finished", () => window.popcornweb.navigate("/exports/latest"));
onSignal("app.finished", (event) => window.popcornweb.navigate(event.url));
```

The first lets the server say *when*; the second lets it say *where*, which is
an open redirect the moment that URL comes from a row somebody else can write.
Prefer closing over the answer.

**In a project that also builds for fasthttp**, call `pwruntime.NewSignal` and
`pwruntime.NamedSignal` rather than the `pw` re-exports: a source names no
transport, so it lives in a file no build tag excludes, and `pw` is not in the
fasthttp build. See [deployment.md](deployment.md).

**When not to use a signal:** not for state (anything a region displays is a
delivery), not for anything the client can already tell (`pw.delivery_applied`
exists), and not as a general remote-call channel — the set of things a page can
be told to do is fixed at build time and is exactly what its table holds.

## Common mistakes

- Putting `server-action` on a button when the interaction has fields or an
  instance — the direct endpoint carries no path parameters and scripting-off
  does nothing.
- Naming a `pw.ServerAction` function from `server-action` in markup, or
  expecting a raw handler to return a value to a script.
- Registering a DOM listener in module scope instead of `setup`, or in `setup`
  without a `teardown` — a replaced region leaves the callback behind.
- Writing `on-click="increment()"`, or naming a handler the block does not
  return.
- A relative `import` inside a `<script component>` block.
- Two root elements in a component that declares a script block (a `//` comment
  beside the markup counts as content and breaks the single-root rule).
- Putting display state in a signal payload, or reader-specific data in a signal
  from a shared source.
- Reaching for `pw.NewSignal` in a project that also builds for fasthttp.
