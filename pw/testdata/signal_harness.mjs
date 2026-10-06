// Harness driving the signal registry of the boundary runtime under node.
//
// It loads boundary.js for real rather than stubbing it, because what is being
// checked is that module's own bookkeeping: which names resolve, which scopes
// are on screen, and what a custom element's connect and disconnect reactions do
// to both. The update harness beside this one stubs the boundary half for the
// opposite reason — there the subject is the update runtime.
//
// Only what the module touches at load is stubbed, and the stubs are inert. No
// DOM is parsed: the element reactions are invoked directly, which is what a
// browser would do and what nothing here can simulate honestly.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

let failures = 0;
function check(condition, message) {
	if (!condition) {
		failures += 1;
		console.error("FAIL: " + message);
	}
}

// --- the stubbed browser ----------------------------------------------------

const definitions = new Map();
// The connected <pw-page> elements, which is what the disconnect reaction
// queries to decide whether a hash is still on screen.
let pages = [];
let fenceComments = [];

function quoteAttribute(value) {
	return value.replace(/["\\]/g, "\\$&");
}

globalThis.HTMLElement = class {};
globalThis.customElements = {
	define(name, constructor) {
		definitions.set(name, constructor);
	},
};
globalThis.NodeFilter = { SHOW_COMMENT: 128 };
globalThis.document = {
	baseURI: "file://" + path.join(here, "..") + "/",
	cookie: "",
	readyState: "complete",
	head: { children: [] },
	body: {},
	addEventListener() {},
	createElement() {
		return { innerHTML: "", content: { children: [] }, setAttribute() {}, style: {} };
	},
	// The comment fence a live boundary's content sits between. Empty unless a
	// case installs one, because most of this file never reaches the walk.
	createTreeWalker() {
		let index = -1;
		return {
			get currentNode() {
				return fenceComments[index];
			},
			nextNode() {
				index++;
				return index < fenceComments.length ? fenceComments[index] : null;
			},
		};
	},
	querySelector(selector) {
		// The one selector the module builds: pw-page by its hash attribute.
		const match = /^pw-page\[hash="(.*)"\]$/.exec(selector);
		if (!match) return null;
		const wanted = match[1];
		for (const page of pages) {
			if (quoteAttribute(page.hash) === wanted) return page;
		}
		return null;
	},
};
globalThis.window = { addEventListener() {} };
const baseHref = "file://" + path.join(here, "..") + "/";
globalThis.location = { href: baseHref, origin: "null", reload() {} };
globalThis.sessionStorage = { getItem: () => null, setItem() {}, removeItem() {} };

const runtime = await import(
	"file://" + path.join(here, "..", "..", "pwbrowser", "boundary.js")
);

// --- a stand-in for one <pw-page> element -----------------------------------

const PageElement = definitions.get("pw-page");
check(PageElement !== undefined, "the runtime defines pw-page");

function page(hash, moduleSource) {
	const element = new PageElement();
	element.hash = hash;
	element.getAttribute = (name) => {
		if (name === "hash") return hash;
		if (name === "module") return moduleSource || null;
		return null;
	};
	return {
		hash: hash,
		element: element,
		connect() {
			pages.push(this);
			element.connectedCallback();
		},
		disconnect() {
			pages = pages.filter((entry) => entry !== this);
			element.disconnectedCallback();
		},
	};
}

// --- a name resolves only against the table ---------------------------------

{
	const seen = [];
	const off = runtime.registerEvent("app.toast", (payload) => seen.push(payload));
	check(runtime.dispatchSignal("app.toast", { text: "hi" }), "a registered name dispatches");
	check(seen.length === 1 && seen[0].text === "hi", "the handler receives the payload");

	check(!runtime.dispatchSignal("app.nothing", {}), "an unregistered name dispatches nothing");

	off();
	check(!runtime.dispatchSignal("app.toast", {}), "an unregistered handler stops receiving");
}

// --- several handlers may share one name ------------------------------------

{
	let first = 0;
	let second = 0;
	const offA = runtime.registerEvent("app.shared", () => first++);
	const offB = runtime.registerEvent("app.shared", () => second++);
	runtime.dispatchSignal("app.shared", {});
	check(first === 1 && second === 1, "both handlers of one name are called");
	offA();
	offB();
}

// --- a scope is reachable only while its page is on screen ------------------

{
	const room = page("roomhash");
	const calls = [];
	const off = runtime.registerEvent("roomhash", "app.finish", () => calls.push("fired"));

	runtime.dispatchSignal("app.finish", {});
	check(calls.length === 0, "a handler whose page is not on screen is not called");
	check(!runtime.activeScope("roomhash"), "an unconnected hash is not active");

	room.connect();
	check(runtime.activeScope("roomhash"), "connecting a pw-page activates its hash");
	runtime.dispatchSignal("app.finish", {});
	check(calls.length === 1, "a handler whose page is on screen is called");

	room.disconnect();
	check(!runtime.activeScope("roomhash"), "disconnecting deactivates the hash");
	runtime.dispatchSignal("app.finish", {});
	check(calls.length === 1, "leaving the page stops the handler being reached");

	// The registration never went away, so returning makes it reachable again
	// with nothing re-executed. This is the whole reason the scope is a set
	// rather than the registry being cleared.
	room.connect();
	runtime.dispatchSignal("app.finish", {});
	check(calls.length === 2, "returning to the page reaches the same handler again");
	room.disconnect();
	off();
}

// --- two instances of one page overlap during a swap ------------------------

{
	const outgoing = page("swaphash");
	const incoming = page("swaphash");
	outgoing.connect();
	incoming.connect();
	// A delta can insert the replacement before removing the outgoing one, so
	// deactivating on the strength of one element alone would close a scope that
	// is still on screen.
	outgoing.disconnect();
	check(runtime.activeScope("swaphash"), "a hash stays active while another element still carries it");
	incoming.disconnect();
	check(!runtime.activeScope("swaphash"), "the last element leaving deactivates the hash");
}

// --- an unscoped handler belongs to no page ---------------------------------

{
	let calls = 0;
	const off = runtime.registerEvent("app.global", () => calls++);
	runtime.dispatchSignal("app.global", {});
	check(calls === 1, "an unscoped handler is reachable with no page on screen");
	off();
}

// --- a throwing handler cannot stop the next one ----------------------------

{
	let reached = 0;
	const previousError = console.error;
	console.error = () => {};
	const offA = runtime.registerEvent("app.throws", () => {
		throw new Error("handler bug");
	});
	const offB = runtime.registerEvent("app.throws", () => reached++);
	runtime.dispatchSignal("app.throws", {});
	console.error = previousError;
	check(reached === 1, "a throwing handler does not stop the next one");
	offA();
	offB();
}

// --- a handler registering during its own dispatch is not walked into -------

{
	let inner = 0;
	let off2 = () => {};
	const off1 = runtime.registerEvent("app.reentrant", () => {
		off2 = runtime.registerEvent("app.reentrant", () => inner++);
	});
	runtime.dispatchSignal("app.reentrant", {});
	check(inner === 0, "a handler registered during dispatch is not called by that dispatch");
	off1();
	off2();
}

// --- a page declares its own enter and leave --------------------------------

{
	const events = [];
	const undefine = runtime.definePage("lifehash", {
		enter(page) {
			events.push("enter");
			page.on("app.tick", () => events.push("tick"));
		},
		leave() {
			events.push("leave");
		},
	});

	const element = page("lifehash");
	element.connect();
	check(events.join(",") === "enter", "entering the page runs its enter handler");

	runtime.dispatchSignal("app.tick", {});
	check(events.join(",") === "enter,tick", "a handler registered in enter receives while on screen");

	element.disconnect();
	check(events.join(",") === "enter,tick,leave", "leaving the page runs its leave handler");

	// The whole point of the handle: a forgotten cleanup would otherwise leave
	// this reachable and re-register on the next visit.
	runtime.dispatchSignal("app.tick", {});
	check(events.join(",") === "enter,tick,leave", "a handle registration is released on leave");

	// Re-entering runs setup again with nothing re-evaluated, which is what an
	// ES module cannot do for itself.
	element.connect();
	runtime.dispatchSignal("app.tick", {});
	check(
		events.join(",") === "enter,tick,leave,enter,tick",
		"returning re-runs enter and the handler works again",
	);
	element.disconnect();
	undefine();
}

// --- revisiting does not accumulate handlers --------------------------------

{
	let calls = 0;
	const undefine = runtime.definePage("repeathash", {
		enter(page) {
			page.on("app.repeat", () => calls++);
		},
	});
	const element = page("repeathash");
	for (let visit = 0; visit < 3; visit++) {
		element.connect();
		element.disconnect();
	}
	element.connect();
	runtime.dispatchSignal("app.repeat", {});
	check(calls === 1, "four visits leave exactly one handler, not four");
	element.disconnect();
	undefine();
}

// --- a definition arriving after the element is already on screen -----------

{
	// The element upgrades while the document parses and a page's module is
	// deferred, so this is the ordinary order rather than an edge case. Without
	// the catch-up, enter would never run at all.
	const element = page("latehash");
	element.connect();

	let entered = 0;
	const undefine = runtime.definePage("latehash", {
		enter() {
			entered++;
		},
	});
	check(entered === 1, "a definition registered after its element connected still enters");

	element.disconnect();
	undefine();
}

// --- two elements of one page during a swap -------------------------------

{
	const events = [];
	const undefine = runtime.definePage("swaplife", {
		enter() {
			events.push("enter");
		},
		leave() {
			events.push("leave");
		},
	});
	const outgoing = page("swaplife");
	const incoming = page("swaplife");
	outgoing.connect();
	incoming.connect();
	check(events.join(",") === "enter", "a second element of one page does not enter again");
	outgoing.disconnect();
	check(events.join(",") === "enter", "the page is not left while an element still carries it");
	incoming.disconnect();
	check(events.join(",") === "enter,leave", "the last element leaving leaves the page");
	undefine();
}

// --- a leave handler can still reach what enter registered ------------------

{
	let reachable = false;
	const undefine = runtime.definePage("teardownhash", {
		enter(page) {
			page.on("app.teardown", () => {});
		},
		leave() {
			// Release happens after this runs, so a leave handler that dispatches
			// or unregisters is not working against an already-empty table.
			reachable = runtime.dispatchSignal("app.teardown", {});
		},
	});
	const element = page("teardownhash");
	element.connect();
	element.disconnect();
	check(reachable, "leave runs before the handle is released");
	undefine();
}

// --- a throwing enter does not take the runtime down ------------------------

{
	const previousError = console.error;
	console.error = () => {};
	const undefine = runtime.definePage("throwhash", {
		enter() {
			throw new Error("setup bug");
		},
	});
	const element = page("throwhash");
	element.connect();
	console.error = previousError;
	check(runtime.activeScope("throwhash"), "a page whose enter threw is still on screen");
	element.disconnect();
	undefine();
}

// --- unregisterEvent removes by name and handler ----------------------------

{
	let calls = 0;
	const handler = () => calls++;
	runtime.registerEvent("app.explicit", handler);
	check(runtime.unregisterEvent("app.explicit", handler), "unregisterEvent reports what it removed");
	runtime.dispatchSignal("app.explicit", {});
	check(calls === 0, "an explicitly unregistered handler stops receiving");
}

// --- what setup is handed, and what it hands back ---------------------------
//
// The bag and the returned namespace are this framework's own conventions: the
// module reports the block and takes an answer back, and what a setup looks like
// is entirely ours. Nothing in Go can assert any of it.

{
	// An element carrying a handler declaration and one carrying parameters,
	// both read off the same node the marker sits on.
	function component(owner, attributes, children) {
		const listeners = [];
		const node = {
			children: children || [],
			listeners: listeners,
			getAttribute: (name) => {
				if (name === "data-tb-component") return owner;
				return name in attributes ? attributes[name] : null;
			},
			hasAttribute: (name) => name === "data-tb-component" || name in attributes,
			querySelectorAll: (selector) =>
				selector === "[data-tb-on]" ? node.children.filter((c) => c.getAttribute("data-tb-on")) : node.children,
			addEventListener: (event, handler) => listeners.push({ event: event, handler: handler }),
			closest: () => node,
		};
		return node;
	}

	runtime.applyScopeCatalog(runtime.parseScopeCatalog(
		"Bagged:./testdata/pagemodule_fixture.mjs"));
	globalThis.__pageModuleEnters = 0;
	globalThis.__pageModuleLeaves = 0;
	globalThis.__pageModuleProps = null;
	globalThis.__pageModuleHandled = 0;

	const instance = component("Bagged", {
		"data-tb-on": "click:announce",
		"data-tb-props": '{"label":"hi","count":3}',
	});
	runtime.mountScopesIn(instance);
	await new Promise((resolve) => setTimeout(resolve, 60));

	check(globalThis.__pageModuleEnters === 1, "the bagged setup ran");
	// Parameters keep their JSON types, which is the thing reading an attribute
	// back as text cannot do.
	check(globalThis.__pageModuleProps && globalThis.__pageModuleProps.count === 3,
		"a component parameter arrived as a number rather than as text");

	// The returned namespace is bound to the element that named it.
	check(instance.listeners.length === 1 && instance.listeners[0].event === "click",
		"the declared handler was bound for its event");
	instance.listeners[0].handler({});
	check(globalThis.__pageModuleHandled === 1, "and calling it reaches what setup returned");

	// A teardown registered rather than returned still runs, which is the whole
	// point of freeing the return value for the handlers above.
	runtime.releaseScopesIn(instance);
	check(globalThis.__pageModuleLeaves === 1, "a registered teardown ran on release");
}

{
	// Unreadable parameters yield an empty object rather than throwing, so a
	// setup destructuring one reads undefined instead of failing to start at all.
	runtime.applyScopeCatalog(runtime.parseScopeCatalog(
		"Broken:./testdata/pagemodule_fixture.mjs"));
	globalThis.__pageModuleEnters = 0;
	globalThis.__pageModuleProps = null;
	const node = {
		children: [],
		getAttribute: (name) => {
			if (name === "data-tb-component") return "Broken";
			return name === "data-tb-props" ? "{not json" : null;
		},
		hasAttribute: () => false,
		querySelectorAll: () => [],
		addEventListener: () => {},
		closest: () => node,
	};
	runtime.mountScopesIn(node);
	await new Promise((resolve) => setTimeout(resolve, 60));
	check(globalThis.__pageModuleEnters === 1, "an unreadable parameter object did not stop the setup");
	check(globalThis.__pageModuleProps && typeof globalThis.__pageModuleProps === "object",
		"and it read as an empty object rather than as nothing");
	runtime.releaseScopesIn(node);
}

// --- scoped scripts start per element, not per declaration -----------------

{
	function marked(owner, children) {
		const node = {
			children: children || [],
			getAttribute: (name) => (name === "data-tb-component" ? owner : null),
			hasAttribute: () => false,
			querySelectorAll: () => node.children,
		};
		return node;
	}

	runtime.applyScopeCatalog(runtime.parseScopeCatalog(
		"Counter:./testdata/pagemodule_fixture.mjs"));
	globalThis.__pageModuleEnters = 0;
	globalThis.__pageModuleLeaves = 0;
	globalThis.__pageModuleCalls = 0;

	// Two instances of one declaration. This is the case the identity round trip
	// existed for: neither carries an instance id, and both must run.
	const first = marked("Counter");
	const second = marked("Counter");
	const region = marked("Wrapper", [first, second]);
	runtime.mountScopesIn(region);
	await new Promise((resolve) => setTimeout(resolve, 60));
	check(globalThis.__pageModuleEnters === 2, "every instance of one declaration is started");

	// A second scan starts nothing again, which is what makes it safe to run
	// after every operation.
	runtime.mountScopesIn(region);
	await new Promise((resolve) => setTimeout(resolve, 30));
	check(globalThis.__pageModuleEnters === 2, "an already-started element is not started twice");

	// A handler registered through the scope reaches, and stops reaching once the
	// instance is released — without the author's teardown doing anything.
	runtime.dispatchSignal("app.frommodule", {});
	check(globalThis.__pageModuleCalls === 2, "each instance's own handler receives");

	runtime.releaseScopesIn(region);
	check(globalThis.__pageModuleLeaves === 2, "releasing a region tears down every instance in it");
	runtime.dispatchSignal("app.frommodule", {});
	check(globalThis.__pageModuleCalls === 2, "a scope registration is released with its instance");

	// And a released element can be started again, since a replacement carries
	// new ones with the same markers.
	runtime.mountScopesIn(region);
	await new Promise((resolve) => setTimeout(resolve, 30));
	check(globalThis.__pageModuleEnters === 4, "a released element can be started again");
	runtime.releaseScopesIn(region);
}

// --- a marker with no catalog entry starts nothing --------------------------

{
	const unknown = {
		getAttribute: (name) => (name === "data-tb-component" ? "pages.nothing.Nope" : null),
		hasAttribute: () => false,
		querySelectorAll: () => [],
	};
	// An asset set is a catalogue, so a marker for a declaration whose script this
	// response did not carry is ordinary and must not throw.
	runtime.mountScopesIn(unknown);
	runtime.releaseScopesIn(unknown);
	check(true, "a marker with no catalog entry is ignored");
}

// --- a live refill releases what it is replacing ---------------------------

// The other apply path. A delivery refills a comment-bracketed region rather
// than replacing an element, so it reaches the release by its own route and a
// wiring present in one and missing from the other is invisible to the swap
// test above.
{
	runtime.applyScopeCatalog([{ owner: "Refilled", url: "./testdata/pagemodule_fixture.mjs" }]);
	globalThis.__pageModuleEnters = 0;
	globalThis.__pageModuleLeaves = 0;

	const inside = {
		getAttribute: (name) => (name === "data-tb-component" ? "Refilled" : null),
		hasAttribute: () => false,
		querySelectorAll: () => [],
		remove() {},
	};
	const container = { insertBefore() {} };
	const open = { data: "tb:tb-9", isConnected: true };
	const close = { data: "/tb:tb-9", isConnected: true, parentNode: container };
	open.nextSibling = inside;
	inside.nextSibling = close;
	fenceComments = [open, close];

	// The first apply finds the fence and establishes the range.
	check(runtime.applyHTML("tb-9", "<p>one</p>") === "changed", "the first delivery applied");
	runtime.mountScopesIn(inside);
	await new Promise((resolve) => setTimeout(resolve, 60));
	check(globalThis.__pageModuleEnters === 1, "the instance inside the region started");

	// The second is a refill of the range the first opened, which is where the
	// release has to happen.
	// The replacement carries the marker again, as a re-rendered region does, so
	// the scan after the refill has something to start.
	const replacement = {
		getAttribute: (name) => (name === "data-tb-component" ? "Refilled" : null),
		hasAttribute: () => false,
		querySelectorAll: () => [],
		remove() {},
	};
	container.querySelectorAll = () => [replacement];
	container.getAttribute = () => null;
	runtime.applyHTML("tb-9", "<p>two</p>");
	check(globalThis.__pageModuleLeaves === 1, "a live refill released what it replaced");
	await new Promise((resolve) => setTimeout(resolve, 40));
	check(globalThis.__pageModuleEnters === 2, "and started what arrived in its place");
	fenceComments = [];
}

// --- a module without setup is not mistaken for a lifecycle ----------------

{
	const previousError = console.error;
	const reported = [];
	console.error = (...args) => reported.push(args.join(" "));
	runtime.applyScopeCatalog([
		{ owner: "NoSetup", url: "./testdata/pagemodule_nosetup_fixture.mjs" },
	]);
	const element = {
		getAttribute: (name) => (name === "data-tb-component" ? "NoSetup" : null),
		querySelectorAll: () => [],
	};
	runtime.mountScopesIn(element);
	await new Promise((resolve) => setTimeout(resolve, 40));
	console.error = previousError;
	check(
		reported.some((line) => line.includes("no setup function")),
		"a module default-exporting something of its own is not treated as a lifecycle",
	);
}

// --- a cross-origin module is refused ---------------------------------------

{
	const previousError = console.error;
	const reported = [];
	console.error = (...args) => reported.push(args.join(" "));
	runtime.applyScopeCatalog([{ owner: "Evil", url: "https://elsewhere.test/pwn.js" }]);
	const element = {
		getAttribute: (name) => (name === "data-tb-component" ? "Evil" : null),
		querySelectorAll: () => [],
	};
	runtime.mountScopesIn(element);
	await new Promise((resolve) => setTimeout(resolve, 20));
	console.error = previousError;
	check(
		reported.some((line) => line.includes("cross-origin")),
		"a cross-origin scope module is refused rather than imported",
	);
	// And it is unclaimed, so a later catalog naming a good URL can still start it.
	runtime.releaseScopesIn(element);
	check(true, "a refused element is left releasable");
}

// --- release and mount are wired into the apply loop ------------------------

// The primitives above are exercised directly, which says nothing about whether
// the apply loop calls them. Upstream asked specifically to hear if the
// release-before-swap recipe did not survive contact with it, so these go
// through swapNode and refill rather than through the scan.

function domNode(owner, children) {
	const node = {
		children: children || [],
		parentNode: null,
		getAttribute: (name) => (name === "data-tb-component" ? owner || null : null),
		querySelectorAll: (selector) =>
			selector.indexOf("data-tb-component") >= 0 ? node.children.filter((c) => c.getAttribute("data-tb-component")) : [],
		hasAttribute: () => false,
		replaceWith(replacement) {
			node.replaced = replacement;
		},
		remove() {},
	};
	for (const child of node.children) child.parentNode = node;
	return node;
}

{
	runtime.applyScopeCatalog([
		{ owner: "Wired", url: "./testdata/pagemodule_fixture.mjs" },
	]);
	globalThis.__pageModuleEnters = 0;
	globalThis.__pageModuleLeaves = 0;

	const outgoing = domNode("Wired");
	const parent = domNode(null, [outgoing]);
	outgoing.parentNode = parent;

	runtime.mountScopesIn(outgoing);
	await new Promise((resolve) => setTimeout(resolve, 60));
	check(globalThis.__pageModuleEnters === 1, "the element under test started");

	// The replacement carries the same marker, as a re-rendered component does.
	const incoming = domNode("Wired");
	parent.children = [incoming];
	incoming.parentNode = parent;
	runtime.swapNode(outgoing, incoming);
	check(globalThis.__pageModuleLeaves === 1, "swapNode released the outgoing instance");

	await new Promise((resolve) => setTimeout(resolve, 40));
	check(globalThis.__pageModuleEnters === 2, "swapNode started the incoming instance");
}

// A live delivery refills a comment-bracketed region rather than replacing an
// element, so it reaches the release by its own path.
{
	runtime.applyScopeCatalog([
		{ owner: "Refilled", url: "./testdata/pagemodule_fixture.mjs" },
	]);
	globalThis.__pageModuleEnters = 0;
	globalThis.__pageModuleLeaves = 0;

	const inside = domNode("Refilled");
	let chain = [];
	const end = { parentNode: null, nextSibling: null };
	const start = { nextSibling: inside };
	inside.nextSibling = end;
	const container = {
		insertBefore() {},
		querySelectorAll: () => [],
		getAttribute: () => null,
	};
	end.parentNode = container;

	runtime.mountScopesIn(inside);
	await new Promise((resolve) => setTimeout(resolve, 60));
	check(globalThis.__pageModuleEnters === 1, "the region's instance started");

	runtime.applyBoundary;
	// refill is internal; the exported path that reaches it is applyHTML into an
	// already-applied range, which is what a second live delivery is.
	runtime.releaseScopesInRange({ start: start, end: end });
	check(globalThis.__pageModuleLeaves === 1, "a range release tears down what is inside it");
}

// A document that arrived whole names its scoped scripts on a marker of its
// own. Without one nothing told the client which module belongs to which
// owner, so a page opened directly mounted nothing.
{
	const ScopesElement = definitions.get("tb-scopes");
	check(ScopesElement !== undefined, "the runtime defines tb-scopes");
	const marker = new ScopesElement();
	let removed = false;
	marker.getAttribute = (name) => (name === "value" ? "Buffered:./testdata/pagemodule_fixture.mjs" : null);
	marker.remove = () => {
		removed = true;
	};
	marker.connectedCallback();
	check(removed, "the marker removes itself once read");

	globalThis.__pageModuleEnters = 0;
	const instance = {
		children: [],
		getAttribute: (name) => (name === "data-tb-component" ? "Buffered" : null),
		hasAttribute: (name) => name === "data-tb-component",
		querySelectorAll: () => [],
		addEventListener() {},
		closest: () => instance,
	};
	runtime.mountScopesIn(instance);
	await new Promise((resolve) => setTimeout(resolve, 60));
	check(globalThis.__pageModuleEnters === 1, "a component named by the marker has its setup run");
	runtime.releaseScopesIn(instance);
}

if (failures > 0) {
	console.error(failures + " check(s) failed");
	process.exit(1);
}
console.log("all checks passed");
