---
title: Server actions
description: Name a Go function from a template instead of writing its URL, and let one build serve a browser with the runtime and one without.
sidebar:
  order: 8
---

A form that mutates needs an address, and an address written by hand is a string
no compiler checks against the function it targets. Rename the handler and
nothing fails until somebody clicks.

`server-action` names the function instead:

```html
package users

export component Page(user: User): html {
  <form server-action="Retire">
    <label>Reason <input type="text" name="reason" /></label>
    <button type="submit">retire</button>
  </form>
}
```

```go
package users

// Retire is an exported handler in the route package, beside the template that
// names it. Nothing is generated around it: registration is all there is.
func Retire(w http.ResponseWriter, r *http.Request) {
	request, err := pw.Parse[retireRequest](r)
	if err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
	if err := retire(r.Context(), pw.PathValue(r, "id"), request.Reason); err != nil {
		pw.WriteProblem(w, r, err)
		return
	}
}

type retireRequest struct {
	Reason string `json:"reason" check:"required"`
}
```

Rename `Retire` in Go and generation fails at the attribute that referenced it.
That is the whole reason to name a function rather than a path.

:::note[Before you start]
This is a [page tree](/guides/cross-layer/discovered-routing/) feature: the
handler lives in the route package the template belongs to. It also needs
`security.csrf` configured, which `pw init` writes for you — see
[When the check is off](#when-the-check-is-off) for what happens otherwise.
:::

## Put it on a form

A `<form>` carrying `server-action` works whether or not the browser runs the
framework runtime, and that is the reason to prefer it.

Generation writes `method="post"`, a hidden field naming the handler, and the
CSRF token. It deliberately writes **no** `action`, because a form without one
posts to the document's own URL — which already has this page's path parameters
filled in. So the same markup does two things:

- With no JavaScript, the browser posts natively and the handler answers.
- With the runtime loaded, the submit is intercepted, posted with `fetch`, and
  the response applied as [regions](/guides/cross-layer/partial-updates/) rather
  than a whole page.

Nothing configures that. The runtime's presence is what picks the path, the same
way a link works because it is a link and the runtime only makes it faster.

## A bare button costs the no-script path

`server-action` is accepted on any element, and on anything that is not a form
it lowers to one attribute the runtime reads:

```html
<button server-action="Rename">rename</button>
```

With scripting off, that button does nothing. No lowering can change it —
nothing in HTML invokes a button outside a form.

It also posts to a different address, and that address carries less. A form goes
to the page URL, so `pw.PathValue(r, "id")` reads the id. A bare button goes to
`/_action/<hash>/Rename`, which is a compile-time constant with no path
parameters in it at all. A handler reached that way cannot tell which user it is
about.

So: **put a server action on a form** unless the interaction genuinely has no
fields and no instance, and you have accepted that scripting-off does nothing.

## What the handler owes

An ordinary `http.HandlerFunc` that owns its whole response. It can be called
directly from a test with `httptest` and no registration.

Writing nothing is meaningful: the form entry point answers `303` back to the
page, so a reload does not resubmit and the address bar keeps showing the page.
Write a status, a header, or a body and that response stands instead — which is
how a handler redirects elsewhere, renders the page inline with validation
errors, or streams.

The address grants nothing. Both entry points are publicly reachable, so the
handler authenticates and authorizes its own caller exactly as any route does.
Every exported handler-shaped function in a route package gets an endpoint
whether or not a template mentions it; lower-case the ones that should not.

### Answering by caller

Owning the whole response means the handler can answer each caller with what
that caller can use:

```go
func Rename(w http.ResponseWriter, r *http.Request) {
	// …mutate…
	switch {
	case pw.WantsValue(r):
		// A script called this by name and is holding the answer.
		pw.WriteAPI(w, r, renamed{Name: request.Name})
	case pw.WantsUpdate(r):
		// The runtime intercepted a gesture, so answer with the regions that
		// changed and it applies them in place.
		pw.WriteUpdate(w, r, http.StatusOK, pw.Replace("name", Name(NameParams{Value: request.Name})))
	default:
		// A native submit, with a document waiting for a page.
		pw.RedirectSeeOther(w, r, "/users/"+id)
	}
}
```

Ask neither question and one response goes to everybody, which is right for a
handler with nothing to return. Ask the first and a form submit will not be shown
a JSON document.

### Why the signature is not typed

Owning the response is exactly what a typed signature cannot express: redirects,
conditional statuses, downloads and streaming are all legitimate answers, and no
fixed return covers them.

The input is still typed — `pw.Parse` recovers the struct, and generation checks
form field names against its fields. What stays untyped is the signature and the
return, and that is the price of answering a form.

A function that answers only a script pays nothing for it. That is the shape
below.

## A function a script calls

A form has to be answered with a response. A script asking a question does not —
it wants a value back. Declare an ordinary Go function and it becomes one:

```go
package users

// The declaration is what publishes it. Put it above the function, so a reader
// meeting the function learns it is reachable.
var _ = pw.ServerAction(profile)

func profile(ctx context.Context, id string) (Profile, error) {
	return load(ctx, id)
}
```

```js
const p = await actions.profile({ id: "42" });   // a Profile, decoded
```

Both ends are typed. Arguments arrive from the call's payload by name, the
result comes back encoded, and an error becomes a
[problem response](/guides/frontend/responses/#errors) with the status the
framework already maps.

Four things follow from the declaration being what admits it:

- **The function can be unexported.** Nothing is published by merely existing
  here, which is the opposite of the handler shape above.
- **Any signature works.** A leading `context.Context` is optional and receives
  the request's, so the database handle and the signed-in reader are both in
  reach.
- **It is called by its published name.** `GetUser` is `actions.getUser`; pass a
  string to `pw.ServerAction` to publish a different one, which is what a rename
  must not move.
- **A template cannot name it.** `server-action="profile"` fails generation,
  because a form reaching it would be shown a value it cannot render.

Reach for this when a script wants an answer, and for the handler shape when a
form wants a response. A page may have both.

## Errors that come back

A rejected submission returns `4xx` and the regions it carries are the
validation errors. The runtime applies them whatever the status says, because
that is the point of returning them — see
[Forms](/guides/interactivity/forms/) for what the re-rendered form shows.

A handler that fails outright is a different answer. A `500`, or a `403` from
the CSRF check, comes back as a problem document rather than as regions, so
there is nothing to apply, and nothing can safely redo a mutation. The runtime
leaves the page as it is and reports it: an error in the console naming the
action and its status, and a `failed` event to whatever listens through
`window.popcornweb.subscribe`.

```js
window.popcornweb.subscribe((kind, detail) => {
  if (kind === "failed" && detail.action) showToast("That did not go through.");
});
```

That listener is where an application says so in its own words. Without one the
control simply leaves its busy state, which reads to a user as a button that did
nothing.

## When the check is off

A generated form carries a CSRF token, which comes from `security.csrf`.

With the check on, the token is issued to every visitor, signed in or not: the
secret rides a sealed cookie while the session is anonymous, so a crawler that
merely loads the page writes no server record. What it does need is
`session.enabled`, since the secret is a session slot.

With `security.csrf.enabled = false`, the form still renders — with an empty
token field, and nothing verifying the submission. That is what turning the
check off means, and it is worth knowing that adopting a server action is the
moment a project acquires an unsafe form it did not have before.

## One page, one POST

A template declaring a form action makes generation register `POST` on the
page's own path, beside its `GET`.

If your application already hand-registers a `POST` at that same path, startup
panics on the duplicate — Go's router names both registration sites, one of
which will be the generated registry. Remove yours, or move the mutation into
the action.

## When not to use it

If the interaction changes nothing on the server, it is not an action. A
disclosure widget is `<details>`, a dialog is `<dialog>`, and a search form that
refines the page it is on is an ordinary `GET` form the runtime already
intercepts.

If it runs in the browser, it is a client handler rather than a server one. It
is named the same way and resolved the same way, and comes from the component's
own script block instead — see
[Component scripts](/guides/interactivity/component-scripts/#handlers-the-markup-can-name).

And if the mutation has to be gated — a confirmation, a check only the browser
can make — put the client handler on the element and leave `server-action` off
it, then call the action from JavaScript. A template carrying both runs the
handler and issues the action regardless. See
[Calling a server action](/guides/interactivity/component-scripts/#calling-a-server-action).

If it changes something and you are not in a page tree, write an ordinary route.
Server actions are a page's implementation detail: they appear in no OpenAPI
document and nothing versions them, so a caller outside the page has no contract
to hold on to.

## Reference

[`server-action` in a page tree](/reference/template-syntax/#server-action-in-a-page-tree)
has the attribute's exact lowering, and
[Discovered routing](/guides/cross-layer/discovered-routing/) has the route
package model the handler lives in.
