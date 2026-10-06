// Popcorn Web boundary runtime.

const modeHeader = "Pw-Response-Mode";
const liveMode = "live";

// The validator of the content each boundary is showing travels back to the
// server on this header, and the server leaves alone every region whose
// validator still matches what it just rendered.
//
// It is what makes a reconnect cost what changed rather than the whole page. A
// live connection re-executes the page and re-renders every boundary on it,
// including the ones that settled once and never change, so without this a
// dropped connection or a lifetime rollover re-transfers the screen to say
// nothing happened.
//
// The values are opaque and are never compared here. This side stores what
// arrived and hands it back; only the server can say whether it still holds.
const liveManifestHeader = "Pw-Live-Manifest";

// The CSRF token travels in a cookie rather than in the page, and it is read
// here at the moment a request is issued rather than once at load. That is the
// whole difference: a value written into the document is fixed at render, so a
// session rotation — a login, a privilege change — would never reach a screen
// that is already open, and its next request would be refused with nothing on
// screen explaining why.
//
// The names are constants because this script cannot read them off its own tag:
// a module script has no document.currentScript. They are pw.CSRFCookieName and
// the header the middleware reads, and the Go side names the same two.
const csrfCookieName = "pw_csrf";
const csrfHeaderName = "X-CSRF-Token";

function csrfToken() {
	// A cookie value cannot contain a semicolon or a space unquoted, and this
	// one is base64url, so splitting on "; " is exact rather than a heuristic.
	const prefix = csrfCookieName + "=";
	for (const entry of document.cookie.split("; ")) {
		if (entry.startsWith(prefix)) return entry.slice(prefix.length);
	}
	return "";
}

// withCSRF adds the token to a header set. Every request this runtime issues
// goes through it, so a request that should carry one cannot be written without
// it by forgetting a call site.
function withCSRF(headers) {
	const token = csrfToken();
	if (token) headers[csrfHeaderName] = token;
	return headers;
}

// Every piece of module state is declared here, above the custom element
// definitions, and that placement is load-bearing. Defining an element upgrades
// the ones the parser already inserted, synchronously, inside the define call —
// so a callback that reads a binding declared further down the module reads it
// before its initializer has run, and throws. The document marker is always
// already in the DOM by then, which makes this the ordinary case rather than a
// race.

// replaced records that the document has been given up on. A replacement is
// terminal: the page it patched no longer exists, so a completion arriving
// after it belongs to a document that is gone.
let replaced = false;

// documentComplete records that the terminal marker arrived. Nothing about the
// transport can say this: a chunked document cut off mid-stream is end of file
// to the parser, which fires DOMContentLoaded and load exactly as it does for a
// complete one.
let documentComplete = false;
let documentVersion = "";

// The live connection: one request to this page's own URL, carrying the mode
// header, re-issued whenever it ends for a reason that is not terminal. The
// server holds no subscription state, so a reconnect is the same request as the
// first connection.
let connection = null;
let running = false;
// liveConnections counts the responses this screen has opened, so the first one
// is distinguishable from a reconnect. Declared here with the rest of the
// module-level state, above the first customElements.define, because defining an
// element upgrades the ones the parser already inserted synchronously inside the
// define call and a callback reading a binding declared further down throws.
let liveConnections = 0;

// applied remembers where each boundary's content sits, as the pair of comment
// nodes that bracket it, plus the HTML currently between them.
//
// A settled boundary could simply replace its placeholder, because it settled
// once. A live boundary is re-rendered for as long as its subscription lives,
// so its content needs an address that survives the first delivery. Comments
// are that address: they are inert, invisible to CSS and to layout, and they
// bracket a range rather than wrap it, so a delivery of several top-level nodes
// needs no container element the author never wrote.
//
// Every boundary is bracketed, not only the live ones, because nothing on the
// wire says which is which: htmlbind allocates the same placeholder markup and
// yields the same id-and-HTML pair for both.
const applied = new Map();

function pruneApplied() {
	for (const [id, range] of applied) {
		if (!range.end.isConnected) applied.delete(id);
	}
}

// The comment pair bracketing one boundary's content. Since system:tinybind
// v0.4.8 a progressive render writes the same pair around an await boundary's
// fallback, so the spelling is a contract with the server rather than a private
// choice, and a range this client creates and one the document arrived with are
// the same thing.
function openMarker(id) {
	return "tb:" + id;
}

function closeMarker(id) {
	return "/tb:" + id;
}

// findFence returns the pair the document was written with, for a boundary this
// client has not settled yet.
//
// A comment carries no id and no selector reaches it, so this walks. The walk is
// over comment nodes only, and it runs once per boundary — the range is kept in
// applied afterwards, and every later delivery to the same boundary refills it
// without searching again.
//
// The open marker is remembered rather than matched pairwise, because boundaries
// nest: an outer fence opens, an inner one opens and closes inside it, and the
// outer one closes last. Taking the nearest open before the matching close is
// what pairs them correctly, and the ids differ, so only markers naming this
// boundary are ever considered.
function findFence(id) {
	const open = openMarker(id);
	const close = closeMarker(id);
	const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_COMMENT);
	let start = null;
	while (walker.nextNode()) {
		const comment = walker.currentNode;
		if (comment.data === open) start = comment;
		else if (comment.data === close && start) return { start: start, end: comment };
	}
	return null;
}

// liveManifest is what this screen claims to be showing, as the pairs the next
// connection carries.
//
// A range whose end marker has left the document is skipped: an enclosing
// boundary re-rendered and took it along, so the content it names is not on
// screen and claiming it would leave the region empty until something else
// changed. A range with no validator is skipped for the same reason in reverse
// — nothing here can compute one, so an unclaimed boundary is simply delivered.
function liveManifest() {
	const pairs = [];
	for (const [id, range] of applied) {
		if (range.digest && range.end.isConnected) pairs.push(id + ":" + range.digest);
	}
	return pairs.join(",");
}

// seedManifest takes the validators of what the document itself committed, off
// the terminal marker.
//
// Without it the first connection of every page view re-transfers the whole
// screen, because the document delivered its boundaries through the parser and
// the connection that follows has no idea which bytes are already there.
function seedManifest(value) {
	if (!value) return;
	for (const entry of value.split(",")) {
		const cut = entry.indexOf(":");
		if (cut <= 0) continue;
		const range = applied.get(entry.slice(0, cut));
		if (range) range.digest = entry.slice(cut + 1);
	}
}

function refill(range, fragment, html) {
	releaseScopesInRange(range);
	let node = range.start.nextSibling;
	const outgoing = [];
	while (node && node !== range.end) {
		const next = node.nextSibling;
		outgoing.push(node);
		node = next;
	}
	// A live boundary replaces content a user may have been typing into, so the
	// same carrying-across an update does applies here.
	const settle = carryClientState(outgoing, fragment);
	for (const gone of outgoing) gone.remove();
	range.end.parentNode.insertBefore(fragment, range.end);
	settle();
	range.html = html;
	pruneApplied();
	// The replacement is between the fences now, so anything scoped inside it is
	// startable.
	mountScopesIn(range.end.parentNode || document.body);
}

// The client state a server render cannot know about, carried from the outgoing
// nodes into the replacement before it lands.
//
// It lives here rather than beside the update runtime because both halves of
// this asset replace content: a delta swaps a region and a live delivery refills
// one, and a user who lost their typing would not care which did it. One
// implementation is also the only way the two cannot drift.

// composing is true while an input method has an unconfirmed composition open.
// Replacing the element under it would commit or discard whatever the user was
// midway through spelling.
let composing = false;
document.addEventListener("compositionstart", () => { composing = true; });
document.addEventListener("compositionend", () => { composing = false; });

export function compositionActive() {
	return composing;
}

// resolveNavigable returns the absolute form of a navigation target the browser
// can follow without running script, or null for one it cannot.
//
// location.assign executes a javascript: URL rather than navigating to it, so
// every sink that reaches it needs this. The server refuses such a target too;
// this is the half that holds when a record arrives from somewhere the server
// did not write it, and it costs one comparison.
//
// Resolving first is what keeps the rule short. A relative target resolves
// against the page and therefore inherits the page's own scheme, so it needs no
// case of its own — after resolution there is only ever an absolute URL to
// check, and the allowlist mirrors internal/safeurl on the Go side.
export function resolveNavigable(value, base) {
	if (typeof value !== "string" || value === "") return null;
	let resolved;
	try {
		resolved = new URL(value, base || document.baseURI);
	} catch {
		return null;
	}
	switch (resolved.protocol) {
		case "http:":
		case "https:":
		case "mailto:":
		case "tel:":
			return resolved.href;
		default:
			return null;
	}
}

// A marked region is moved rather than re-rendered: a third-party widget, a
// canvas, a media element mid-playback. The server does not own what is inside
// it and cannot reproduce it.
export function preserveAttributeName(attr) {
	return "data-" + attr + "-preserve";
}

let preserveAttr = preserveAttributeName("tb");

export function setPreserveAttribute(name) {
	preserveAttr = name;
}

// carryClientState moves preserved islands into the replacement and restores
// the form state the render did not mean to change.
//
// It returns what has to happen after the replacement is in the document.
// Focus is the reason: focusing a node that is still inside a fragment does
// nothing at all, silently, so the one thing a user notices most is the one
// thing that cannot be finished here.
function carryClientState(outgoing, fragment) {
	const live = new Map();
	const values = new Map();
	for (const node of outgoing) {
		if (!node.querySelectorAll) continue;
		collectPreserved(node, live);
		collectFormState(node, values);
	}
	if (live.size) restorePreserved(fragment, live);
	if (!values.size) return settleNothing;
	const focused = restoreFormState(fragment, values);
	return focused ? () => placeFocus(focused) : settleNothing;
}

function settleNothing() {}

// Only a text-like control has a selection. Reading one from a control that
// does not throws rather than answering, so the probe is guarded rather than
// the type being enumerated: which input types carry a selection has changed
// before and is not worth a list here.
function selectionOf(control) {
	try {
		if (control.selectionStart === null || control.selectionStart === undefined) return null;
		return [control.selectionStart, control.selectionEnd, control.selectionDirection || "none"];
	} catch {
		return null;
	}
}

function placeFocus(target) {
	const control = target.control;
	if (!control.isConnected || !control.focus) return;
	// preventScroll because where the viewport belongs is a decision the update
	// runtime has already made, and refocusing must not overrule it.
	control.focus({ preventScroll: true });
	if (!target.selection || !control.setSelectionRange) return;
	try {
		control.setSelectionRange(target.selection[0], target.selection[1], target.selection[2]);
	} catch {
		// A control whose type changed under the swap has no selection to set.
	}
}

function collectPreserved(root, into) {
	if (root.getAttribute && root.hasAttribute(preserveAttr)) {
		into.set(root.getAttribute(preserveAttr), root);
	}
	for (const marked of root.querySelectorAll("[" + preserveAttr + "]")) {
		into.set(marked.getAttribute(preserveAttr), marked);
	}
}

function restorePreserved(fragment, live) {
	for (const hole of fragment.querySelectorAll("[" + preserveAttr + "]")) {
		const kept = live.get(hole.getAttribute(preserveAttr));
		if (kept) hole.replaceWith(kept);
	}
}

// A control's value is compared against its own default rather than against the
// replacement's. That comparison is the whole rule: a value equal to its default
// is one the user never touched, so the new default wins; a value that differs
// is the user's typing, and an update that did not assert a new default must not
// discard it.
function collectFormState(root, into) {
	const controls = root.querySelectorAll ? root.querySelectorAll("input, textarea, select") : [];
	for (const control of controls) {
		const key = control.name || control.id;
		if (!key) continue;
		const held = {};
		// Focus and the caret are carried whether or not the value moved. A user
		// whose cursor is still in the search box has changed nothing about it,
		// and that is exactly the case a region swap loses: a page that updates
		// as it is typed puts the caret at the end of nowhere on every keystroke.
		if (control === document.activeElement) {
			held.focused = true;
			held.selection = selectionOf(control);
		}
		if (control.type === "checkbox" || control.type === "radio") {
			if (control.checked !== control.defaultChecked) held.checked = control.checked;
		} else if (control.type === "file") {
			// A file input's value cannot be set from script at all, so it is not
			// restorable by value; it belongs in a preserved island or outside the
			// region, which requirement:unified-update-runtime records as a gap.
		} else if (control.value !== control.defaultValue) {
			held.value = control.value;
		}
		if (held.focused || "checked" in held || "value" in held) into.set(key, held);
	}
}

// Returns the control focus belongs on once the replacement has landed, or null.
// There is at most one: a document has one active element.
function restoreFormState(fragment, values) {
	let focused = null;
	const controls = fragment.querySelectorAll ? fragment.querySelectorAll("input, textarea, select") : [];
	for (const control of controls) {
		const held = values.get(control.name || control.id);
		if (!held) continue;
		if ("checked" in held) {
			// A changed default is the server asserting a new value, and it wins.
			if (control.checked === control.defaultChecked) control.checked = held.checked;
		} else if ("value" in held && control.value === control.defaultValue) {
			control.value = held.value;
		}
		if (held.focused && !focused) focused = { control: control, selection: held.selection };
	}
	return focused;
}

// parseFragment turns rendered markup into nodes. It is separate from the swap
// below because a caller filling the holes of a decomposed fragment has to reach
// inside it before it lands: a node moved into a hole after insertion is a node
// moved twice, and a reparented iframe reloads on every move.
export function parseFragment(html) {
	const holder = document.createElement("template");
	holder.innerHTML = html;
	return holder.content;
}

// swapNode replaces one addressed element with prepared nodes, carrying the
// client state across. It is what a delta operation, a redraw, and an action
// response all land through, so a region arrives the same way whichever asked.
//
// The carry is in two halves: what has to move before the nodes land — a
// preserved island, a control's value — and what can only be restored once they
// are in the document, which is focus and the caret.
export function swapNode(target, fragment) {
	if (!target || !target.parentNode) return false;
	// Before the replacement lands, because the subtree about to go is still the
	// one every scoped setup inside it ran against. Afterwards the teardown would
	// be working on nodes that are already detached.
	//
	// It lives in the shared swap rather than at each call site for the reason
	// the client-state core does: a delta, a redraw, an action response, and a
	// live refill all destroy a region, and a release missing from one of them is
	// a leak nobody would find.
	releaseScopesIn(target);
	const settle = carryClientState([target], fragment);
	target.replaceWith(fragment);
	settle();
	pruneApplied();
	// And after, so what arrived is started. The fragment is in the document by
	// now, which is what a setup reading the DOM needs.
	mountScopesIn(target.parentNode || document.body);
	return true;
}

// swapElement is swapNode for a caller holding markup rather than nodes.
export function swapElement(target, html) {
	return swapNode(target, parseFragment(html));
}

export function applyBoundary(id, fragment, html, digest) {
	if (replaced) return false;
	const range = applied.get(id);
	if (range && !range.end.isConnected) {
		// An enclosing live boundary re-rendered and took this range with it.
		// The replacement subtree carries this boundary's placeholder again,
		// under the same id, so the lookup below finds it.
		applied.delete(id);
	} else if (range) {
		// The server suppresses an unchanged delivery, so this is the case it
		// cannot: a reconnect to a process whose validator key differs, which
		// renders the same bytes and cannot tell they are already here.
		// Re-inserting identical nodes would restart animations, drop focus and
		// selection inside the region, and make a screen reader announce a log
		// nobody added to, so an identical delivery is left alone — and the new
		// validator is taken, or this screen would keep claiming one the server
		// no longer recognizes and keep being sent the same bytes.
		if (html !== undefined && html === range.html) {
			range.digest = digest;
			return "unchanged";
		}
		refill(range, fragment, html);
		range.digest = digest;
		return "changed";
	}
	// Not settled yet, so the range is the one the document arrived with: the
	// fence around this boundary's fallback.
	//
	// The markers stay. A settled boundary would not need them again, but a live
	// one is re-rendered for as long as its subscription lives, and nothing on
	// the wire says which this is — the same fence and the same id-and-HTML pair
	// serve both. Keeping them costs two comment nodes and is what lets the
	// second delivery find the region the first one wrote.
	const fence = findFence(id);
	if (!fence) return false;
	const opened = { start: fence.start, end: fence.end, html: html, digest: digest };
	applied.set(id, opened);
	refill(opened, fragment, html);
	// A settled boundary arriving is what a handler waiting on this region has
	// been waiting for, and the runtime is the party that observes it. It fires
	// here rather than at the call site so the parser path and the record path
	// report it once between them.
	dispatchSignal(signalBoundarySettled, { id: id });
	return "changed";
}

export function applyHTML(id, html, digest) {
	const holder = document.createElement("template");
	holder.innerHTML = html;
	return applyBoundary(id, holder.content, html, digest);
}

export function replaceDocument(fragment) {
	replaced = true;
	stopLive();
	document.body.replaceChildren(fragment);
	applied.clear();
}

// The signal registry: one table of named callbacks the page registered, which
// both the server and this runtime dispatch into.
//
// A name is a lookup key and never code. Nothing here resolves a name against
// anything but this table — no eval, no dynamic import, no property lookup on a
// global — because the flexibility is meant to come from the payload varying
// rather than from the instruction varying. What the client can be told to do is
// fixed at build time and is exactly what this map holds, which is what lets a
// page keep script-src as a fixed allowlist.
//
// One table for both producers on purpose. A handler cares what happened, not
// which side noticed, and two registries would make every handler pick a side
// and would put the reserved names somewhere an author could shadow them.
const signalHandlers = new Map();

// activeScopes is which page scopes are on screen right now.
//
// A registration is owned by the scope that was current when it ran, and the
// scope is a hash a <pw-page> element carries. The element's own connect and
// disconnect reactions are the whole lifecycle: entering a page activates its
// hash, leaving deactivates it, and the platform decides when rather than any
// route matching here.
//
// This is what makes a return visit work. A page's module is evaluated once per
// URL and never again — an ES module has no second evaluation, and the head
// installer deliberately does not re-insert a script it already loaded — so
// re-registering on arrival is not something the platform offers. Nothing has to
// re-run: the registration never went away, and its scope becomes reachable
// again when the element reconnects.
const activeScopes = new Set();

// pageDefinitions holds the lifecycle a page declared for itself, by hash, and
// pageState what is currently open for one: how many elements carry the hash,
// and what its enter handler registered.
//
// Counting rather than a boolean because a delta may insert the replacement
// before removing the outgoing element, so a page can briefly have two of them
// on screen. Entering twice would run setup twice for a page the user never
// left.
const pageDefinitions = new Map();
const pageState = new Map();

// definePage declares what happens when a page is entered and left.
//
// It is the shape that answers the problem a page's script cannot solve on its
// own: an ES module is evaluated once per URL, so a page's setup cannot re-run
// on a return visit. Declaring it here separates the evaluation, which happens
// once, from the activation, which happens every time — and the element carrying
// the hash is what says when.
//
// Registrations made through the handle are released on leave. An author may
// also release one early with the function it returns, and may register outside
// the handle for something that should outlive the page. What is deliberately
// not left to the author is the ordinary case: a forgotten cleanup would
// re-register on every revisit and the symptom would be a handler firing twice,
// which is the kind of wrong that still looks like it works.
export function definePage(hash, lifecycle) {
	if (typeof hash !== "string" || !hash || !lifecycle) return () => {};
	pageDefinitions.set(hash, lifecycle);
	// The element is upgraded while the document parses and a page's own module
	// is deferred, so by the time this runs the page it describes is usually
	// already on screen — with its scope open and its enter never run, because
	// there was no definition to run. Catching up here is what stops that
	// ordinary ordering from meaning enter never fires at all.
	if (!activeScopes.has(hash)) return releaseDefinition(hash, lifecycle);
	const open = pageState.get(hash);
	if (!open) {
		enterPage(hash, 0);
	} else if (!open.entered) {
		open.entered = true;
		runEnter(lifecycle, open.handle, hash);
	}
	return releaseDefinition(hash, lifecycle);
}

function releaseDefinition(hash, lifecycle) {
	return () => {
		if (pageDefinitions.get(hash) === lifecycle) pageDefinitions.delete(hash);
	};
}

// The scope catalog: declaration identity to module URL, from the wire.
//
// A catalog and never a mount list. What mounts is decided by the DOM, because
// the render writes the declaring component onto every rendered instance and an
// asset set reports what a composition *could* need — including a component
// below a slot that never rendered.
const scopeCatalog = new Map();

// mounted maps a live element to what its setup returned, so a teardown is found
// from the node about to be destroyed. A WeakMap because the key is the only
// thing keeping the entry reachable: a node dropped without passing through the
// apply loop takes its entry with it rather than leaking one per render.
const mounted = new WeakMap();

// scopeMarkerAttribute is the attribute the render writes on a scoped
// component's root element. The update half sets it from the configured prefix;
// the default is the module's own, which is what this framework uses today.
let scopeMarkerAttribute = "data-tb-component";

export function setScopeMarkerAttribute(name) {
	if (typeof name === "string" && name) scopeMarkerAttribute = name;
}

// applyScopeCatalog records what the wire said and starts whatever is on screen.
export function applyScopeCatalog(entries) {
	for (const entry of Array.isArray(entries) ? entries : []) {
		scopeCatalog.set(entry.owner, entry.url);
	}
	mountScopesIn(document.body);
}

function scopeMarkers(root) {
	if (!root) return [];
	const found = [];
	// The root itself counts: a delta's replacement is the component's own
	// element, so the marker is on the node handed in rather than under it.
	if (root.getAttribute && root.getAttribute(scopeMarkerAttribute)) found.push(root);
	if (root.querySelectorAll) {
		for (const element of root.querySelectorAll("[" + scopeMarkerAttribute + "]")) {
			found.push(element);
		}
	}
	return found;
}

// mountScopesIn starts every scoped script inside root that is not started yet.
//
// Document order, so an ancestor's setup runs before a descendant's — the same
// outermost-first rule a composition chain would have given, falling out of the
// tree walk instead of out of an ordering on the wire.
export function mountScopesIn(root) {
	for (const element of scopeMarkers(root)) {
		if (mounted.has(element)) continue;
		const url = scopeCatalog.get(element.getAttribute(scopeMarkerAttribute));
		if (!url) continue;
		// Claimed before the import resolves, so a second scan during the await
		// does not start the same element twice.
		const record = { release: null, handlers: null, settled: null };
		mounted.set(element, record);
		// A descendant waits for its ancestors and not for its siblings. What the
		// guide promises is that an ancestor's setup runs first, which stays true
		// of a promise chain and would not of a free-for-all; what it does not
		// promise is any order between two components in unrelated parts of a
		// page, and one queue for the whole document would let an await that
		// never settles stall every remaining component on it.
		const ancestor = enclosingScope(element);
		const waitFor = ancestor && ancestor.settled ? ancestor.settled : Promise.resolve();
		record.settled = waitFor.then(
			() => loadScopeModule(url, element, record),
			() => loadScopeModule(url, element, record),
		);
	}
}

// enclosingScope is the record of the nearest marked ancestor, or nothing when
// this element is the outermost one.
function enclosingScope(element) {
	const parent = element.parentElement;
	if (!parent || !parent.closest) return null;
	const ancestor = parent.closest("[" + scopeMarkerAttribute + "]");
	return ancestor ? mounted.get(ancestor) || null : null;
}

// releaseScopesIn tears down every scoped script inside root.
//
// It runs before the incoming markup lands, which is why it takes a region
// rather than a list: the subtree about to be replaced is still the one every
// setup inside it ran against, and afterwards a teardown would be working on
// nodes that are already detached.
export function releaseScopesIn(root) {
	for (const element of scopeMarkers(root)) {
		if (!mounted.has(element)) continue;
		const record = mounted.get(element);
		mounted.delete(element);
		if (!record || typeof record.release !== "function") continue;
		try {
			record.release();
		} catch (error) {
			// A failing teardown must not stop the swap it is making room for.
			console.error("Popcorn Web: scope teardown failed", error);
		}
	}
}

// releaseScopesInRange is releaseScopesIn for a live boundary's
// comment-bracketed region, which has no single element to hand in.
export function releaseScopesInRange(range) {
	if (!range || !range.start) return;
	let node = range.start.nextSibling;
	while (node && node !== range.end) {
		releaseScopesIn(node);
		node = node.nextSibling;
	}
}

// elementBag is the one argument a scoped setup receives, destructured.
//
// A bag rather than positional arguments because an author then names only what
// they use, and because a capability added later is one more key rather than a
// third argument nobody passed. Everything taken from it is released with the
// instance, so the ordinary case needs no cleanup at all.
//
// teardown is handed in rather than returned, which is what frees the return
// value to mean one thing: the handlers this component publishes. A registration
// is also the better shape on its own — it can be called more than once, and
// from inside a helper, where a returned value has to be threaded back out.
function elementBag(element) {
	const releases = [];
	const teardowns = [];
	return {
		bag: {
			el: element,
			// onSignal rather than on: this framework's own attribute vocabulary
			// spells a DOM event on-click, and a bare on() beside it would read as
			// binding one when it registers a server-authored signal instead.
			onSignal(name, handler) {
				const release = registerEvent(name, handler);
				releases.push(release);
				return release;
			},
			teardown(fn) {
				if (typeof fn === "function") teardowns.push(fn);
			},
			props: readScopeProps(element),
			actions: pageActions(),
		},
		release() {
			// Signal registrations first and teardowns after, in reverse of
			// registration, which is the order the guide already documents for
			// the scope running before the returned teardown.
			for (const release of releases) release();
			releases.length = 0;
			for (let i = teardowns.length - 1; i >= 0; i--) {
				try {
					teardowns[i]();
				} catch (error) {
					// One failing teardown must not keep the others from running.
					console.error("Popcorn Web: scope teardown failed", error);
				}
			}
			teardowns.length = 0;
		},
	};
}

// scopePropsAttribute is where a component's emitted parameters ride. It is set
// from the configured prefix by the update half, like every other attribute.
let scopePropsAttribute = "data-tb-props";

export function setScopePropsAttribute(name) {
	if (typeof name === "string" && name) scopePropsAttribute = name;
}

// readScopeProps parses what generation emitted for this instance.
//
// Absent, empty, or unreadable all yield an empty object rather than nothing, so
// a setup destructuring a parameter reads undefined instead of throwing on the
// destructuring itself. An absent optional omits its key, which is the rule the
// attribute context already applies and why "in" is the test for one.
function readScopeProps(element) {
	const raw = element.getAttribute ? element.getAttribute(scopePropsAttribute) : null;
	if (!raw) return {};
	try {
		const parsed = JSON.parse(raw);
		return parsed && typeof parsed === "object" ? parsed : {};
	} catch (error) {
		console.error("Popcorn Web: component parameters are unreadable", error);
		return {};
	}
}

// The server functions this page publishes, as functions a script can call.
//
// A component script mutating without a gesture has no element to read an
// address off, and cannot compute one either: the address holds a digest of the
// declaring directory. So the document carries the set the route matched, and
// this turns it into a namespace.
//
// Built once per document and shared by every instance, because it is a
// property of the route rather than of an instance. Frozen so a handler cannot
// add a name to it and have a later reader believe the page published one.
let pageActionsCache = null;

export function resetPageActions() {
	pageActionsCache = null;
}

function pageActions() {
	if (pageActionsCache) return pageActionsCache;
	const meta = document.querySelector('meta[name="pw-actions"]');
	let addresses = {};
	if (meta) {
		try {
			const parsed = JSON.parse(meta.getAttribute("content") || "");
			if (parsed && typeof parsed === "object") addresses = parsed;
		} catch (error) {
			console.error("Popcorn Web: the page action set is unreadable", error);
		}
	}
	const namespace = {};
	for (const name of Object.keys(addresses)) {
		namespace[name] = (input) => callAction(addresses[name], input);
	}
	pageActionsCache = Object.freeze(namespace);
	return pageActionsCache;
}

// actionCaller is how a call reaches the network and the apply path.
//
// It is installed by the update half rather than imported, because issuing a
// request and applying what came back are that half's, and this half is the one
// that loads first.
let actionCaller = null;

export function setActionCaller(caller) {
	if (typeof caller === "function") actionCaller = caller;
}

function callAction(url, input) {
	if (!actionCaller) {
		return Promise.reject(new Error("Popcorn Web: updates are disabled, so an action cannot be called"));
	}
	return actionCaller(url, input);
}

// clientHandlerAttribute lists the events an element binds and the handler each
// names, as one comma-separated list of colon-separated pairs.
let clientHandlerAttribute = "data-tb-on";

export function setClientHandlerAttribute(name) {
	if (typeof name === "string" && name) clientHandlerAttribute = name;
}

// bindHandlers attaches every handler declared under one mounted component.
//
// It runs after setup returned, because the namespace is what setup produced.
// Nothing is recorded for release: a listener added directly to a node dies with
// the node, and the nodes these sit on are exactly the ones a swap destroys.
function bindHandlers(root, handlers) {
	if (!handlers || typeof handlers !== "object") return;
	for (const element of handlerElements(root)) {
		// The nearest marked ancestor owns this element. A descendant component
		// with its own block binds its own, and a nested one's handlers are not
		// this namespace's to answer for.
		if (owningScope(element, root) !== root) continue;
		for (const pair of (element.getAttribute(clientHandlerAttribute) || "").split(",")) {
			const separator = pair.indexOf(":");
			if (separator <= 0) continue;
			const event = pair.slice(0, separator);
			const handler = handlers[pair.slice(separator + 1)];
			if (typeof handler !== "function") {
				console.error("Popcorn Web: no handler named", pair.slice(separator + 1));
				continue;
			}
			element.addEventListener(event, handler);
		}
	}
}

// handlerElements is every element under root declaring a handler, root itself
// included, which is the same shape scopeMarkers takes for the same reason.
function handlerElements(root) {
	const found = [];
	if (!root) return found;
	if (root.getAttribute && root.getAttribute(clientHandlerAttribute)) found.push(root);
	if (root.querySelectorAll) {
		for (const element of root.querySelectorAll("[" + clientHandlerAttribute + "]")) {
			found.push(element);
		}
	}
	return found;
}

// owningScope is the marked component an element's handlers resolve against.
function owningScope(element, fallback) {
	if (element.getAttribute && element.getAttribute(scopeMarkerAttribute)) return element;
	if (!element.closest) return fallback;
	return element.closest("[" + scopeMarkerAttribute + "]") || fallback;
}

const scopeModules = new Map();

// loadScopeModule imports a declaration's module once and runs its setup for one
// element.
//
// The module is evaluated once per URL, as an ES module always is; what runs per
// instance is the function it exported. That distinction is the feature.
function loadScopeModule(source, element, record) {
	let url;
	try {
		url = new URL(source, document.baseURI);
	} catch (error) {
		console.error("Popcorn Web: scope module URL is unusable", source);
		mounted.delete(element);
		return;
	}
	// Same-origin only. The URL is the server's own, so this is not a guard
	// against the application — it is what keeps a templating mistake or an
	// injected value from becoming a script host of somebody else's choosing.
	if (url.origin !== location.origin) {
		console.error("Popcorn Web: refusing a cross-origin scope module", url.href);
		mounted.delete(element);
		return;
	}
	let pending = scopeModules.get(url.href);
	if (!pending) {
		pending = import(url.href);
		scopeModules.set(url.href, pending);
	}
	return pending.then(async (module) => {
		const setup = module && module.setup;
		if (typeof setup !== "function") {
			console.error("Popcorn Web: scope module exports no setup function", url.href);
			mounted.delete(element);
			return;
		}
		// The element may have been released while the import was in flight, in
		// which case it is no longer claimed and must not be started.
		if (mounted.get(element) !== record) return;
		const scope = elementBag(element);
		record.release = scope.release;
		try {
			// Awaited, because refusing a promise is the harder position to hold
			// now. What it costs is a wider window in which the element is inert,
			// which is the same window the dynamic import already opened.
			const handlers = await setup(scope.bag);
			// Released while its setup was running: nothing is bound, and the
			// release already ran against whatever the setup registered.
			if (mounted.get(element) !== record) return;
			record.handlers = handlers;
			bindHandlers(element, handlers);
		} catch (error) {
			scope.release();
			console.error("Popcorn Web: scope setup failed", url.href, error);
		}
	}, (error) => {
		console.error("Popcorn Web: scope module failed to load", url.href, error);
		mounted.delete(element);
	});
}

// parseScopeCatalog reads the owner and URL pairs off the wire.
//
// The grammar is the manifest's: comma between entries, colon within one. A
// malformed entry is skipped rather than failing the parse — a dropped entry
// costs one declaration its lifecycle, where a refused catalog costs every one.
export function parseScopeCatalog(value) {
	const entries = [];
	if (!value) return entries;
	for (const entry of value.split(",")) {
		const separator = entry.indexOf(":");
		if (separator <= 0) continue;
		const owner = entry.slice(0, separator);
		const url = entry.slice(separator + 1);
		if (!owner || !url) continue;
		entries.push({ owner: owner, url: url });
	}
	return entries;
}

function pageHandle(hash) {
	const releases = [];
	return {
		hash: hash,
		// on is registerEvent bound to this page, and what it returns is released
		// on leave whether or not the caller keeps it.
		on(name, handler) {
			const release = registerEvent(hash, name, handler);
			releases.push(release);
			return release;
		},
		releaseAll() {
			for (const release of releases) release();
			releases.length = 0;
		},
	};
}

function enterPage(hash, count) {
	const definition = pageDefinitions.get(hash);
	const handle = pageHandle(hash);
	// entered records whether a definition's enter has run for this opening. A
	// page whose element connected before its module was evaluated opens with
	// none, and definePage is what catches it up.
	pageState.set(hash, { handle: handle, count: count, entered: !!definition });
	if (definition) runEnter(definition, handle, hash);
}

function runEnter(definition, handle, hash) {
	if (typeof definition.enter !== "function") return;
	try {
		definition.enter(handle);
	} catch (error) {
		// A page whose setup threw is still on screen, and the rest of the
		// runtime has to keep working over it.
		console.error("Popcorn Web: page enter failed", hash, error);
	}
}

function leavePage(hash) {
	const state = pageState.get(hash);
	if (!state) return;
	pageState.delete(hash);
	const definition = pageDefinitions.get(hash);
	if (definition && typeof definition.leave === "function") {
		try {
			definition.leave(state.handle);
		} catch (error) {
			console.error("Popcorn Web: page leave failed", hash, error);
		}
	}
	// After the author's own cleanup, so a leave handler can still reach what its
	// enter registered.
	state.handle.releaseAll();
}

// The framework's own signal namespace. A lifecycle name arrives under it, and a
// handler trusts one because application data has no route into it: both live
// loops refuse a source that emits one. The Go side names the same prefix.
const reservedSignalPrefix = "pw.";

// The marker attribute and the delta field carrying the scope chain. The Go side
// names the same one.
const scopeChainAttribute = "scopes";

// The lifecycle suffixes, taken verbatim from the wire contract so one moment
// reads the same across both catalogs rather than being renamed here.
const signalDocumentCommitted = reservedSignalPrefix + "document_committed";
const signalDocumentTruncated = reservedSignalPrefix + "document_truncated";
const signalBoundarySettled = reservedSignalPrefix + "boundary_settled";
const signalLiveOpened = reservedSignalPrefix + "live_opened";
const signalLiveClosed = reservedSignalPrefix + "live_closed";
const signalDeliveryApplied = reservedSignalPrefix + "delivery_applied";
// The last two of the specified set. They fire on the update half's paths rather
// than this one's, and are declared here with the rest so one file holds the
// whole vocabulary.
const signalNavigationApplied = reservedSignalPrefix + "navigation_applied";
const signalDirectiveReceived = reservedSignalPrefix + "directive_received";

// registerEvent publishes one entry of the table.
//
// The scope is a page hash, or omitted for a handler that belongs to no one page
// — a shell script's, which stays reachable everywhere. Several handlers may
// share a name, because two independent widgets can care about one thing;
// registration order is not a contract.
export function registerEvent(scope, name, handler) {
	if (typeof scope === "function" || typeof name === "function") {
		// registerEvent(name, handler), the unscoped form.
		handler = name;
		name = scope;
		scope = null;
	}
	if (typeof name !== "string" || typeof handler !== "function") return () => {};
	const entry = { scope: scope || null, handler: handler };
	let entries = signalHandlers.get(name);
	if (!entries) {
		entries = new Set();
		signalHandlers.set(name, entries);
	}
	entries.add(entry);
	return () => {
		entries.delete(entry);
		if (!entries.size) signalHandlers.delete(name);
	};
}

// unregisterEvent removes one handler by name, for an author holding the
// function rather than the release the registration returned.
//
// It exists because the pair reads as a pair: an author who called
// registerEvent in a page's enter expects to be able to undo it by name in the
// same page's leave. Registrations made through the page handle are released
// anyway, so this is the explicit form rather than the necessary one.
export function unregisterEvent(scope, name, handler) {
	if (typeof scope === "function" || typeof name === "function") {
		handler = name;
		name = scope;
		scope = null;
	}
	const entries = signalHandlers.get(name);
	if (!entries) return false;
	let removed = false;
	for (const entry of Array.from(entries)) {
		if (handler && entry.handler !== handler) continue;
		if (scope && entry.scope !== scope) continue;
		entries.delete(entry);
		removed = true;
	}
	if (!entries.size) signalHandlers.delete(name);
	return removed;
}

// activeScope reports whether a page hash is on screen, which is what tells an
// author whether a handler they registered is live right now.
export function activeScope(hash) {
	return typeof hash === "string" && activeScopes.has(hash);
}

// dispatchSignal calls every handler registered for name whose scope is on
// screen. An unregistered name does nothing: an unpublished name is a capability
// the page did not grant, and this is the default-deny of that model rather than
// leniency — it also happens to make a deploy that adds a name ahead of the
// client an ordinary event rather than a broken screen.
export function dispatchSignal(name, payload) {
	const entries = signalHandlers.get(name);
	if (!entries) return false;
	let dispatched = false;
	// Copied before iterating: a handler that registers or unregisters inside its
	// own callback is doing something legitimate, and must not mutate the set
	// being walked.
	for (const entry of Array.from(entries)) {
		if (entry.scope !== null && !activeScopes.has(entry.scope)) continue;
		dispatched = true;
		try {
			entry.handler(payload);
		} catch (error) {
			// A notification must not stop what it was told about. A bug in a
			// toast handler cannot be allowed to stop deliveries from landing.
			console.error("Popcorn Web: signal handler failed", name, error);
		}
	}
	return dispatched;
}

// pw-page carries the hash of the template that rendered it, and its own connect
// and disconnect reactions are the page scope's whole lifecycle.
//
// It is inert: no script, no styling, no layout. A delta that swaps the region
// holding it disconnects the outgoing one and connects the incoming one, so the
// reset happens without anything here matching a URL against a route.
//
// Nesting is meaningful rather than a conflict. A layout's element and its
// page's element are both connected, so a layout-scoped handler stays reachable
// across the pages that share it while a page-scoped one does not.
customElements.define("pw-page", class extends HTMLElement {
	connectedCallback() {
		const hash = this.getAttribute("hash");
		if (!hash) return;
		activeScopes.add(hash);
		const state = pageState.get(hash);
		if (state) {
			// Already open. A delta can insert the replacement before removing the
			// outgoing element, so a page can briefly carry two, and running enter
			// again would be setting up a page the user never left.
			state.count++;
			return;
		}
		enterPage(hash, 1);
	}

	disconnectedCallback() {
		const hash = this.getAttribute("hash");
		if (!hash) return;
		const state = pageState.get(hash);
		if (state && --state.count > 0) return;
		// The count is authoritative, but a page entered by definePage before any
		// element connected starts at zero, so the document is what settles it.
		if (document.querySelector('pw-page[hash="' + hash.replace(/["\\]/g, "\\$&") + '"]')) return;
		// The page is left before its scope closes, not after. A leave handler
		// that dispatches or unregisters is working on the page it is leaving, and
		// closing the scope first would hand it a table its own handlers had
		// already dropped out of.
		leavePage(hash);
		activeScopes.delete(hash);
	}
});

customElements.define("tb-apply", class extends HTMLElement {
	connectedCallback() {
		const id = this.getAttribute("for");
		this.remove();
		if (!id) return;
		const quoted = id.replace(/["\\]/g, "\\$&");
		const template = document.querySelector('template[data-tb-boundary="' + quoted + '"]');
		if (!template) return;
		applyBoundary(id, template.content, template.innerHTML);
		template.remove();
	}
});

customElements.define("tb-apply-document", class extends HTMLElement {
	connectedCallback() {
		this.remove();
		const template = document.querySelector("template[data-tb-document]");
		if (!template) return;
		// replaceChildren drops the template along with the page it replaces,
		// and every pending placeholder with it.
		replaceDocument(template.content);
	}
});

customElements.define("tb-stream-end", class extends HTMLElement {
	connectedCallback() {
		documentComplete = true;
		documentVersion = this.getAttribute("version") || "";
		const state = this.getAttribute("state");
		// The marker is the last markup of the document, so every tb-apply has
		// already run and every range this names is in place.
		seedManifest(this.getAttribute("manifest"));
		// The composition's scoped scripts, outermost first. Applied before the
		// marker is removed and before any live connection opens, so a setup
		// that registers a signal handler is in the table by the time the first
		// record can arrive.
		applyScopeCatalog(parseScopeCatalog(this.getAttribute(scopeChainAttribute)));
		this.remove();
		// This document arrived whole, so a reload attempted for a truncated
		// one is over and the next truncation is allowed to reload again.
		clearReloadGuard();
		// final means nothing more is coming and live means the screen keeps
		// changing. Asking either way would cost a whole page execution on the
		// server for a page that has nothing to deliver.
		// Fired after the marker has been consumed and the manifest seeded, so a
		// handler that needed the whole page sees the state the document ended in
		// rather than one mid-assembly. The three marker states arrive under one
		// name, so a handler tells a finished static page from one about to go
		// live and from one that ended on an unrecovered failure without a name
		// per outcome.
		dispatchSignal(signalDocumentCommitted, { reason: state || "" });
		if (state === "live") startLive();
	}
});

// tb-scopes carries the scope catalog of a document that arrived whole.
//
// A streamed document names its scoped scripts on tb-stream-end and a
// navigation names them in a header. A buffered document has neither, and
// without this it mounted nothing: the components were on screen, their modules
// were loaded, and no setup ever ran until a navigation came back to the page.
//
// The marker is the last markup of the document, so everything it could mount
// is in place by the time it connects.
customElements.define("tb-scopes", class extends HTMLElement {
	connectedCallback() {
		const value = this.getAttribute("value");
		this.remove();
		applyScopeCatalog(parseScopeCatalog(value));
	}
});

// checkDocumentEnd decides whether this document ended or was cut off.
//
// readyState says when the question can be answered and the marker says what
// the answer is: while it is loading, bytes are still arriving and the marker
// may be among them; once it is complete, no more bytes are coming and the
// marker either arrived or never will.
//
// A document that streamed nothing carries no marker either, so it is excluded
// rather than reloaded. Its boundaries — the placeholders still on screen, or
// the ranges already applied — are what say this response was one that streams.
function documentStreamed() {
	return applied.size > 0 || anyFence();
}

// anyFence reports whether this document was written with boundary markers at
// all, settled or not.
//
// The markers outlive settling now — a live boundary needs its range for every
// delivery after the first — so their presence says the response was one that
// streams rather than that something is still pending. That is the question
// being asked: a document that streamed nothing carries no terminal marker
// either, and reloading it would be reloading a page that arrived whole.
function anyFence() {
	const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_COMMENT);
	while (walker.nextNode()) {
		if (walker.currentNode.data.startsWith("tb:")) return true;
	}
	return false;
}

function checkDocumentEnd() {
	if (document.readyState !== "complete") return;
	if (documentComplete || !documentStreamed()) return;
	// Announced before the reload rather than after: this is the one lifecycle
	// name that describes an absence, so there is nothing to fire after, and the
	// reload is about to take the page away.
	dispatchSignal(signalDocumentTruncated, {});
	reloadOnce("the document was truncated");
}

document.addEventListener("readystatechange", checkDocumentEnd);
// The module may have been imported after the document settled, in which case
// no further readystatechange is coming.
checkDocumentEnd();

// reloadOnce recovers a screen that cannot be repaired in place. The guard is
// what keeps a server that truncates every response from turning that into a
// reload loop the user cannot escape.
function reloadGuardKey() {
	return "pw:reloaded:" + location.pathname + location.search;
}

function reloadOnce(reason) {
	try {
		if (sessionStorage.getItem(reloadGuardKey())) {
			console.error("Popcorn Web: " + reason + "; reload already attempted");
			return;
		}
		sessionStorage.setItem(reloadGuardKey(), "1");
	} catch (error) {
		// Storage can be unavailable in a private or partitioned context. A
		// missing guard is worth less than a stuck screen, so recover anyway.
	}
	console.warn("Popcorn Web: " + reason + "; reloading");
	stopLive();
	location.reload();
}

function clearReloadGuard() {
	try {
		sessionStorage.removeItem(reloadGuardKey());
	} catch (error) {
		// Nothing to clear if storage is unavailable.
	}
}

// readRecords yields the records of a newline-delimited stream.
//
// One reader, because both this framework's stream shapes are the same framing
// and reading them twice is how the two come apart. A line that will not parse
// ends the sequence: a stream whose framing is broken has no next record worth
// guessing at, and a caller distinguishes that from a clean end by whether it
// saw a terminator.
export async function* readRecords(body) {
	const reader = body.getReader();
	const decoder = new TextDecoder();
	let buffer = "";
	for (;;) {
		const chunk = await reader.read();
		if (chunk.done) return;
		buffer += decoder.decode(chunk.value, { stream: true });
		let newline = buffer.indexOf("\n");
		while (newline >= 0) {
			const line = buffer.slice(0, newline);
			buffer = buffer.slice(newline + 1);
			newline = buffer.indexOf("\n");
			if (!line.trim()) continue;
			try {
				yield JSON.parse(line);
			} catch (error) {
				return;
			}
		}
	}
}

export function stopLive() {
	running = false;
	if (connection) {
		connection.abort();
		connection = null;
	}
}

export function startLive() {
	if (running) return;
	running = true;
	runLive();
}

async function runLive() {
	let attempts = 0;
	while (running) {
		const outcome = await connectOnce();
		if (!running || outcome.stop) return;
		// A response the server closed at its lifetime bound is healthy, so
		// backing off exponentially would stall a working screen. Only a failure
		// or a truncated stream escalates.
		if (outcome.retry) {
			attempts = 0;
			await sleep(jitter(outcome.retryAfter || 500));
			continue;
		}
		attempts += 1;
		await sleep(jitter(Math.min(30000, 500 * Math.pow(2, attempts))));
	}
}

async function connectOnce() {
	const controller = new AbortController();
	connection = controller;
	let opened = false;
	try {
		const headers = withCSRF({ [modeHeader]: liveMode });
		// Built at connect time rather than kept as a running value, because
		// what is on screen changes between connections: a navigation delta
		// rewrote a region, an enclosing boundary took a nested one with it.
		const manifest = liveManifest();
		if (manifest) headers[liveManifestHeader] = manifest;
		const response = await fetch(location.href, {
			headers: headers,
			credentials: "same-origin",
			cache: "no-store",
			redirect: "error",
			signal: controller.signal,
		});
		if (!response.ok || !response.body) return { stop: false, retry: false };
		let closed = null;
		for await (const record of readRecords(response.body)) {
			const outcome = handleRecord(record);
			if (outcome === "stop") {
				controller.abort();
				return { stop: true, retry: false };
			}
			if (record.r === "end") closed = record;
		}
		// Every ending reports, including the one with no record at all: a handler
		// telling a user the screen is stale needs done and truncation apart, and
		// only this side can tell them apart without reimplementing the backoff.
		if (closed && closed.reason === "done") {
			dispatchSignal(signalLiveClosed, { reason: "done" });
			return { stop: true, retry: false };
		}
		if (closed) {
			dispatchSignal(signalLiveClosed, { reason: "retry", retryMs: closed.retryMs });
			return { stop: false, retry: true, retryAfter: closed.retryMs };
		}
		// The stream ended with no terminal record, so it was cut off rather
		// than finished. Reconnecting is safe: the page executes again and
		// delivers the current state of every live region.
		dispatchSignal(signalLiveClosed, { reason: "truncated" });
		return { stop: false, retry: false };
	} catch (error) {
		if (controller.signal.aborted) return { stop: true, retry: false };
		return { stop: false, retry: false };
	} finally {
		if (connection === controller) connection = null;
	}
}

function handleRecord(record) {
	if (record.r === "head") {
		// Boundary ids name positions in generated code. A deployment that
		// changed that code addresses a document this screen is not showing, so
		// applying anything from it would put content in the wrong place. An
		// unstamped build sends nothing here, which disables the check rather
		// than reloading every screen whenever the server restarts.
		if (documentVersion && record.build && record.build !== documentVersion) {
			reloadOnce("the server was deployed while this page was open");
			return "stop";
		}
		// A delivery whose content reaches a component the document never
		// carried needs its tags before its markup, which is the same ordering
		// the navigation delta makes normative.
		installLiveHead(record.head);
		// The response began yielding. Whether this was a first subscribe or a
		// reconnect is what a handler showing a staleness indicator needs, and it
		// is knowable here and nowhere else on the client.
		dispatchSignal(signalLiveOpened, { reconnect: liveConnections > 0 });
		liveConnections++;
		return "opened";
	}
	if (record.r === "await") {
		if (typeof record.id !== "string" || typeof record.html !== "string") return "ignored";
		const changed = applyHTML(record.id, record.html, typeof record.v === "string" ? record.v : undefined);
		if (changed) {
			// After the nodes are in the document, never before: the whole use is
			// a handler reading or decorating what just arrived, and firing first
			// would hand it the previous DOM.
			//
			// The changed flag is this framework's own beside the specified
			// fields, and it is legal because the client is what observed it. The
			// server suppresses a delivery whose validator matches and this side
			// leaves an identical one alone, so an arrival is not a change, and a
			// handler that flashes a region wants to know which this was.
			dispatchSignal(signalDeliveryApplied, {
				id: record.id,
				v: typeof record.v === "string" ? record.v : undefined,
				changed: changed === "changed",
			});
		}
		if (!changed) {
			// The same page executed again produces the same ids, so an id this
			// screen does not hold means the page's structure changed — a panel
			// added to a dashboard somebody has been watching. Placing it
			// correctly means rendering a document this client did not render,
			// so the honest move is to start over rather than guess.
			reloadOnce("the page structure changed");
			return "stop";
		}
		return "applied";
	}
	if (record.r === "signal") {
		// Dispatched rather than applied: a signal addresses no region, carries no
		// validator and advances nothing, so a name this page never registered is
		// skipped and the stream carries on. A malformed one desynchronizes
		// nothing, which is why it is dropped rather than treated as a fault.
		if (typeof record.name === "string") dispatchSignal(record.name, record.data);
		return "signalled";
	}
	if (record.r === "end") return "closed";
	if (record.r === "reload") {
		dispatchSignal(signalDirectiveReceived, { directive: "reload" });
		reloadOnce("the server asked for a reload");
		return "stop";
	}
	if (record.r === "navigate") {
		const target = resolveNavigable(record.url);
		if (target) {
			dispatchSignal(signalDirectiveReceived, { directive: "navigate", url: target });
			stopLive();
			location.assign(target);
		}
		return "stop";
	}
	// An unknown record comes from a newer server. Ignoring it keeps an older
	// client working instead of tearing down a connection it could still use.
	return "ignored";
}

// installLiveHead adds the tags a delivery needs and nothing else.
//
// It is this half's own rather than the update runtime's, because the update
// runtime is built from a configuration this half never sees. The rule it holds
// is the one that matters: a tag already in the head is left alone, so a
// reconnect that repeats the whole head installs nothing.
function installLiveHead(tags) {
	if (!tags || !tags.length) return;
	for (const tag of tags) {
		const holder = document.createElement("template");
		holder.innerHTML = tag;
		for (const node of Array.from(holder.content.children)) {
			if (node.tagName === "TITLE") continue;
			let present = false;
			for (const existing of document.head.children) {
				if (existing.outerHTML === node.outerHTML) present = true;
			}
			if (!present) document.head.appendChild(node);
		}
	}
}

function jitter(delay) {
	return Math.round(delay * (0.75 + Math.random() * 0.5));
}

function sleep(delay) {
	return new Promise((resolve) => setTimeout(resolve, delay));
}

// A tab going away should release the response rather than leave the server
// rendering for a screen nobody is looking at.
window.addEventListener("pagehide", stopLive);
