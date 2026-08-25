// Popcorn Web update runtime.
//
// This is the framework's own implementation of the update wire contract that
// system:tinybind publishes. It replaced the module's bundled client, which used
// to be concatenated above this file: the protocol's names, its endpoints, and
// its server are this framework's, and having the browser half belong to a
// dependency meant a change this framework could make alone — where a redraw is
// addressed, most concretely — needed a coordinated release instead.
//
// It is written against the specification rather than against that client, and
// deliberately shares the apply core in boundary.js rather than carrying a
// second one. Two implementations of "put this markup there" on one page is what
// the merged asset used to be, and nothing made them agree.
//
// Every failure path here performs the ordinary browser navigation. That is the
// invariant the whole design rests on: it is what lets each capability be
// incomplete without ever being incorrect.

export function createUpdateRuntime(config) {
	const renderHeader = config.header + "-Render";
	const buildHeader = config.header + "-Build";
	const manifestHeader = config.header + "-Manifest";
	const kindHeader = config.header + "-Kind";
	const instanceHeader = config.header + "-Instance";
	const liveHeader = config.header + "-Live";
	// The scope chain of the composition a navigation delta arrives at. Built
	// from the configured prefix like every other header here, so a deployment
	// that renamed the prefix and a client that did not is impossible rather
	// than silent.
	const scopeChainHeader = config.header + "-Scopes";
	// Sent by a call made from a script, and by nothing else.
	const callHeader = config.header + "-Call";
	// The marker the render writes on a scoped component's root element. Built
	// from the same configured prefix as every other attribute here, and handed
	// to the boundary half, which owns the scan but is loaded before any
	// configuration exists.
	setScopeMarkerAttribute("data-" + config.attr + "-component");
	// The behaviour attributes come from the same prefix as everything else, so
	// one document holds one spelling and this half is where every name is
	// derived, because the boundary half is loaded before the configuration is.
	setClientHandlerAttribute("data-" + config.attr + "-on");
	setScopePropsAttribute("data-" + config.attr + "-props");
	// Calling a server function is this half's, because issuing the request and
	// applying what came back are. The boundary half holds the namespace because
	// it is what hands a setup its bag, and it loads first.
	setActionCaller(callPageAction);
	// The capability header says this client can walk a sequence tree; the
	// address header asks for one. They are two headers rather than one because
	// a request that walks sequences and a request for a sequence are different
	// requests — the first is a navigation, the second is its own mode.
	const sequenceHeader = config.header + "-Sequences";
	const sequenceAddressHeader = config.header + "-Sequence-Address";

	const idAttr = "data-" + config.attr + "-id";
	const kindAttr = "data-" + config.attr + "-kind";
	const ignoreAttr = "data-" + config.attr + "-ignore";
	// The placeholder a decomposed fragment leaves where a nested boundary sits.
	//
	// It is a template because a template is the one element the HTML parser
	// leaves where it was written. An unknown element inside a table is
	// foster-parented — lifted out of the tbody and inserted before the table —
	// so a list's holes would sit outside the list and the rows filling them
	// would land loose on the page. It renders nothing, which is what a hole must
	// do until it is filled, and it carries attributes, so the id is on it and
	// every lookup here finds it the same way it finds a boundary.
	//
	// A progressive render's await boundary is a comment fence rather than this,
	// which is the one place the two shapes differ: a template's contents do not
	// render, and a fallback that does not render is not a fallback.
	const placeholderElement = "template";
	// The inert element a document carries its own validators in, named from the
	// same prefix as every other element this framework emits.
	const manifestElement = config.attr + "-manifest";
	const busyAttr = "data-" + config.attr + "-updating";
	// Where a lowered server-action wrote its endpoint. It comes from the same
	// prefix as the boundary attributes because it is written by the same
	// generation into the same document, and a second spelling here would be a
	// second constant to keep in step with the emitter.
	const actionAttr = "data-" + config.attr + "-action";
	setPreserveAttribute("data-" + config.attr + "-preserve");

	// The browser's own scroll restoration runs at popstate, against the document
	// still on screen, before the delta that makes the page that tall has arrived.
	// Taking it over is what lets the position be restored after the content is.
	if (typeof history === "object" && "scrollRestoration" in history) {
		history.scrollRestoration = "manual";
	}

	// The validators this client holds, keyed by instance id. They are a hint the
	// server uses to omit unchanged regions, and nothing else: an oversized
	// manifest is dropped rather than rejected, so a delta is never assumed
	// minimal.
	const manifest = new Map();

	// One ticket per target. A superseded response must not overwrite newer
	// state, and aborting it also stops spending bandwidth and parse work on a
	// response that can no longer land.
	const tickets = new Map();

	// abortRedraws cancels every redraw still in flight.
	//
	// It runs before navigation records are applied, because a redraw addresses
	// an element of the page being left. Applied afterwards it would write a
	// component of the old page into the new one, at an id the new page may well
	// also use — and the ticket that would have caught the supersession is keyed
	// per instance, so it never sees a navigation at all.
	function abortRedraws() {
		for (const [target, ticket] of tickets) {
			if (target.startsWith("redraw:")) ticket.controller.abort();
		}
	}

	function claim(target) {
		const previous = tickets.get(target);
		if (previous) previous.controller.abort();
		const ticket = { controller: new AbortController() };
		tickets.set(target, ticket);
		return {
			signal: ticket.controller.signal,
			current: () => tickets.get(target) === ticket,
			release: () => {
				if (tickets.get(target) === ticket) tickets.delete(target);
			},
		};
	}

	const listeners = new Set();
	function emit(kind, detail) {
		for (const listener of listeners) {
			try {
				listener(kind, detail);
			} catch (error) {
				// A subscriber is a progress indicator or an analytics call. It
				// must not be able to break the update it is watching.
				console.error("Popcorn Web: update subscriber failed", error);
			}
		}
	}

	// A request in flight is marked on the document root rather than reported only
	// through the events above, so a progress affordance is CSS an author writes
	// once instead of a subscriber every application writes again. It is additive:
	// nothing depends on the marker ever appearing, which is what keeps a page
	// that never runs this script from needing a rule about it.
	let inFlight = 0;
	function markBusy(busy) {
		const root = document.documentElement;
		if (!root || !root.setAttribute) return;
		inFlight += busy ? 1 : -1;
		if (inFlight === 1 && busy) root.setAttribute(busyAttr, "");
		else if (inFlight <= 0) {
			inFlight = 0;
			root.removeAttribute(busyAttr);
		}
	}

	// A document load announces itself; a delta has to say so. The region is
	// created at construction rather than at the moment there is something to
	// announce, because a live region inserted and filled in the same task is not
	// reliably read at all.
	let announcer = null;
	function installAnnouncer() {
		if (!document.body || !document.createElement) return;
		const region = document.createElement("div");
		if (!region.setAttribute || !region.style) return;
		region.setAttribute("aria-live", "polite");
		region.setAttribute("aria-atomic", "true");
		// Set through the CSSOM rather than as a style attribute, so a policy
		// forbidding inline style still gets a region that is out of the way.
		region.style.position = "absolute";
		region.style.width = "1px";
		region.style.height = "1px";
		region.style.overflow = "hidden";
		region.style.clip = "rect(0 0 0 0)";
		region.style.whiteSpace = "nowrap";
		document.body.appendChild(region);
		announcer = region;
	}

	function announce(text) {
		// A full document replacement takes the body's children with it, and the
		// region is one of them. Re-installing costs a check and is the difference
		// between announcing once and announcing until the first such replacement.
		if (announcer && announcer.isConnected === false) {
			announcer = null;
			installAnnouncer();
		}
		if (announcer && text) announcer.textContent = text;
	}

	// An update landing mid composition replaces the control being composed into,
	// which commits or discards whatever the user was midway through spelling. It
	// is the ordinary case for a Japanese or Chinese search box that updates as it
	// is typed, and it is the reason the boundary half exposes the probe at all.
	//
	// Deferring never resurrects stale markup: every caller re-checks its ticket
	// after waiting, so a response superseded while a composition was open is
	// discarded exactly as one superseded at any other moment is.
	function afterComposition() {
		if (!compositionActive()) return null;
		return new Promise((resolve) => {
			document.addEventListener("compositionend", () => resolve(), { once: true });
		});
	}

	// fall performs the navigation the browser would have performed. Every
	// failure ends here, so a user action is never lost.
	function fall(url, reason) {
		emit("fellBack", { url: url, reason: reason });
		if (url && url !== location.href) location.assign(url);
		else location.reload();
		return { applied: false, fellBack: true, reason: reason };
	}

	// Every request this client issues says it can walk a sequence, because it
	// can. Whether a fragment then travels as an address and values or as markup
	// is the server's choice per fragment: values are smaller on a large region
	// and larger on a small one, so neither answer is wrong and no bookkeeping
	// about which addresses this client holds needs to travel.
	//
	// A sequence request is the exception. Asking for a tree while claiming to
	// walk one says nothing, and the mode already says what it is.
	function headers(mode, extra) {
		const set = { [renderHeader]: mode, [buildHeader]: config.build };
		if (mode !== "sequence") set[sequenceHeader] = "1";
		return Object.assign(set, extra || {});
	}

	// The served mode is checked on every response. A shared cache or a proxy
	// may have answered a delta request with the document body, and applying that
	// as a delta fills the page with markup that means nothing.
	function served(response) {
		const value = response.headers.get(renderHeader) || "";
		return value.split(";")[0].trim();
	}

	// Head is installed before the markup that needs it, which is the ordering
	// the contract makes normative: a delta reuses the live document shell, so a
	// component reachable for the first time has no stylesheet on the page and
	// markup landing first paints unstyled.
	function installHead(tags) {
		if (!tags || !tags.length) return;
		for (const tag of tags) {
			const holder = document.createElement("template");
			holder.innerHTML = tag;
			for (const node of Array.from(holder.content.children)) {
				if (node.tagName === "TITLE") {
					// A title accumulates into nonsense otherwise, and history
					// entries and assistive technology both read the old one.
					document.title = node.textContent || "";
					continue;
				}
				const existing = headCounterpart(node);
				if (!existing) {
					document.head.appendChild(node);
				} else if (node.tagName === "META") {
					// A named meta describes the page rather than loading
					// something, so the arriving one is the current answer and
					// the one on screen is the previous page's. Replacing is the
					// same call the title above makes, for the same reason.
					existing.replaceWith(node);
				}
				// A link or a script naming what the page already loaded is left
				// exactly as it is: re-inserting it would fetch and re-execute.
			}
		}
	}

	// headCounterpart finds the tag already in the head that this one is another
	// spelling of, or null when nothing there names the same thing.
	//
	// Identical markup used to be the test, and it let a tag whose content varies
	// per render accumulate without bound: the runtime's own configuration meta
	// carries a freshly masked CSRF token, so no two renders spelled it the same
	// way and every navigation appended another copy. That meta no longer travels
	// on a delta at all, but the shape of the mistake is not specific to it —
	// a nonce, a timestamp, or a page's own description would each repeat it, and
	// the head is the one part of the page nothing ever removes from.
	//
	// What names a tag is its element and the one attribute that identifies it:
	// a link is its href, a script is its src, a meta is its name. A tag with
	// none of those is compared by markup, which is what an inline style block
	// has to be compared by.
	const headIdentityAttribute = { LINK: "href", SCRIPT: "src", META: "name" };

	function headCounterpart(node) {
		const attribute = headIdentityAttribute[node.tagName];
		const identity = attribute ? node.getAttribute(attribute) : "";
		// Walked rather than selected. An attribute value reaching a selector
		// would have to be escaped as a CSS string, whose rules are not JSON's,
		// and the head is a few dozen elements.
		for (const existing of document.head.children) {
			if (existing.tagName !== node.tagName) continue;
			if (identity) {
				if (existing.getAttribute(attribute) === identity) return existing;
				continue;
			}
			if (existing.outerHTML === node.outerHTML) return existing;
		}
		return null;
	}

	// A boundary is addressed by the framework attribute first and by the
	// author's own id second, because a redraw and an action response rewrite
	// regions the author named.
	function locate(id) {
		const quoted = id.replace(/["\\]/g, "\\$&");
		return document.querySelector("[" + idAttr + '="' + quoted + '"]') || document.getElementById(id);
	}

	// A fragment arrives decomposed: its own markup, with an empty placeholder
	// where each nested boundary sits, and the ids of those holes listed beside
	// it. A hole is filled one of two ways, and nothing in the markup says which.
	//
	// This client decides by what it holds. A hole whose boundary is on screen
	// takes that live node, moved rather than re-rendered, which is what keeps
	// the state inside it — a playing video, a third-party widget, an open
	// details element. A hole whose boundary this client does not hold is left
	// empty for the operation that fills it, which arrives later in the same
	// response because operations are ordered outermost first.
	//
	// Deciding by what we hold rather than by which operations this response
	// carries is what makes the streamed path work at all: when a parent lands,
	// its children's records have not arrived yet, so there is nothing to consult
	// but the DOM. Retaining a node an operation then replaces costs one swap and
	// converges on the same screen.
	function fillHoles(fragment, boundaries) {
		if (!boundaries || !boundaries.length) return;
		for (const id of boundaries) {
			const hole = fragment.querySelector("[" + idAttr + '="' + quote(id) + '"]');
			if (!hole) continue;
			const held = locate(id);
			// A node that is inside the region being replaced is not a retain: it
			// is about to be discarded along with its parent, and moving it would
			// be moving a node into its own replacement.
			if (held && held !== hole) hole.replaceWith(held);
		}
	}

	function quote(id) {
		return id.replace(/["\\]/g, "\\$&");
	}

	// A fragment's static half — its literal text — is identical in every render,
	// so it stops travelling. An operation then carries the address of that half
	// and the values that fill it, and this client rebuilds the markup.
	//
	// The address is a digest of the template's compiled shape, so a sequence is
	// immutable and the response carrying it is public: it is the one thing on
	// this wire that is not per user. That is also why it is fetched rather than
	// pushed — riding inside a private response would forfeit exactly that, and
	// re-send every sequence on every page load.
	const sequences = new Map();
	const sequenceRequests = new Map();

	// sequenceNodes returns the tree an address names, fetching it once. Two
	// operations naming one address share a single request rather than racing.
	function sequenceNodes(address) {
		if (sequences.has(address)) return Promise.resolve(sequences.get(address));
		let pending = sequenceRequests.get(address);
		if (!pending) {
			pending = fetchSequence(address).then((nodes) => {
				sequenceRequests.delete(address);
				// A null is cached too. An address this deployment cannot describe
				// will not start describing itself, and asking again on every
				// operation would turn one miss into one request per record.
				sequences.set(address, nodes);
				return nodes;
			});
			sequenceRequests.set(address, pending);
		}
		return pending;
	}

	async function fetchSequence(address) {
		let response;
		try {
			response = await fetch(location.href, {
				headers: withCSRF(headers("sequence", { [sequenceAddressHeader]: address })),
				credentials: "same-origin",
				redirect: "error",
			});
		} catch (error) {
			return null;
		}
		// 404 means this process has never rendered the plan behind the address,
		// so it cannot describe it. That is not a failure to recover from here: a
		// sequence is an optimization over markup that is always available, and
		// the caller falls back to asking for the markup.
		if (!response.ok || served(response) !== "sequence") return null;
		try {
			const body = await response.json();
			return body && Array.isArray(body.nodes) ? body.nodes : null;
		} catch (error) {
			return null;
		}
	}

	// materialize returns an operation's markup, whichever half it arrived as.
	// Null means this client cannot rebuild it and the caller falls back.
	//
	// The address wins where both are present, and that ordering is load-bearing
	// rather than arbitrary. An operation is supposed to carry one form or the
	// other, but the redraw response encodes its markup field unconditionally, so
	// choosing values leaves an empty string beside them. Reading the markup first
	// takes that empty string and blanks the region: the runtime reports the
	// redraw applied, the row leaves the page, and nothing anywhere says why.
	//
	// Preferring the address is also right on its own terms. An empty string is
	// a legitimate rendering — a region that now shows nothing — so it cannot be
	// told from an absent one, and the address is the unambiguous half.
	async function materialize(operation) {
		if (typeof operation.seq !== "string") {
			return typeof operation.html === "string" ? operation.html : null;
		}
		// Markup beside an address is the recovery when the address cannot be
		// resolved, which costs a swap this client already has the bytes for
		// instead of the whole page.
		const carried = operation.html ? operation.html : null;
		const nodes = await sequenceNodes(operation.seq);
		if (!nodes) {
			// Named separately from a missing target, because the two failures
			// look identical from the outside and have nothing in common: one is
			// a page that moved under this client, the other is an address this
			// deployment cannot describe.
			console.warn("Popcorn Web: no sequence for", operation.seq);
			return carried;
		}
		const html = reassemble(nodes, operation.values || []);
		if (html === null) {
			console.warn("Popcorn Web: values do not fit sequence", operation.seq,
				(operation.values || []).length);
			return carried;
		}
		return html;
	}

	// reassemble walks the tree and consumes the values, which is the whole of
	// what a client does with a sequence.
	//
	// One value per hole, per conditional (which branch ran), per loop (how many
	// times), and per component call (whether it opened a boundary). Consuming
	// the wrong number at any node puts every later value in the wrong place, so
	// a walk that does not end exactly at the end of the values is a mismatch and
	// yields nothing rather than markup that is subtly wrong.
	//
	// Nothing is escaped here. The values were escaped by the render that
	// produced them, which is the whole reason this client needs no escaping
	// rules of its own — and must not apply any.
	function reassemble(nodes, values) {
		const out = [];
		const consumed = walkSequence(nodes, values, 0, out);
		if (consumed !== values.length) return null;
		return out.join("");
	}

	// The node kinds, as the tree encodes them: a static run is a bare string, a
	// hole is the number zero, and everything else is an object naming its kind.
	const seqIf = 2;
	const seqRepeat = 3;
	const seqComponent = 4;

	function walkSequence(nodes, values, index, out) {
		for (const node of nodes) {
			if (typeof node === "string") {
				out.push(node);
				continue;
			}
			if (node === 0) {
				if (index >= values.length) return -1;
				out.push(values[index++]);
				continue;
			}
			if (index >= values.length) return -1;
			const marker = values[index++];
			let taken;
			if (node.k === seqIf) {
				if (marker !== "t" && marker !== "f") return -1;
				taken = marker === "f" ? node.e || [] : node.t;
			} else if (node.k === seqComponent) {
				if (marker !== "b" && marker !== "i") return -1;
				taken = marker === "i" ? node.e || [] : node.t;
			} else if (node.k === seqRepeat) {
				// The count is decimal digits, never a JavaScript number cast: an
				// empty string casts to zero and a trailing character casts to NaN,
				// and both would silently produce the wrong markup.
				if (!/^\d+$/.test(marker)) return -1;
				const count = parseInt(marker, 10);
				for (let repeat = 0; repeat < count; repeat++) {
					index = walkSequence(node.t, values, index, out);
					if (index < 0) return -1;
				}
				continue;
			} else {
				// A node kind from a newer server. There is no safe guess about
				// how many values it takes, so the walk stops.
				return -1;
			}
			index = walkSequence(taken, values, index, out);
			if (index < 0) return -1;
		}
		return index;
	}

	// applyOperation is async because a replacement may arrive as an address and
	// its values rather than as markup, and resolving the address can cost one
	// fetch. Every other kind decides without waiting.
	async function applyOperation(operation) {
		if (operation.kind === "children") return reconcileChildren(operation);
		// An unrecognized kind comes from a newer server. Ignoring it keeps this
		// client working rather than abandoning a stream it could still use.
		if (operation.kind !== "replace") return true;
		const html = await materialize(operation);
		// An operation carrying neither markup nor a resolvable address describes
		// a region this client cannot rebuild, so the caller falls back.
		if (typeof html !== "string") return false;
		const target = locate(operation.id);
		if (!target) return false;
		const fragment = parseFragment(html);
		fillHoles(fragment, operation.boundaries);
		return swapNode(target, fragment);
	}

	// A children operation says a boundary's own markup is unchanged and its
	// nested boundaries are now these, in this order.
	//
	// It exists because appending one row to a list is the ordinary event on a
	// live screen, and saying it by replacing the parent costs the whole list of
	// holes. So the parent is left alone and only the arrangement is stated.
	//
	// An id already on screen keeps its node, moving if the order moved. An id
	// the list drops is removed. An id this client does not hold gets an empty
	// placeholder, which the operation later in this response fills.
	function reconcileChildren(operation) {
		const parent = locate(operation.id);
		if (!parent) return false;
		const wanted = operation.boundaries || [];
		const held = directBoundaries(parent);
		if (!wanted.length) {
			for (const node of held) node.remove();
			return true;
		}
		// The container is where the children already are. Reading it from the
		// DOM rather than assuming the boundary's root is what keeps a list
		// wrapped in a ul working, and a parent holding none is a rearrangement
		// this client cannot place — it falls back rather than guessing.
		const anchor = held.length ? held[0] : null;
		if (!anchor) return false;
		const container = anchor.parentNode;
		if (!container) return false;
		const byID = new Map();
		for (const node of held) byID.set(node.getAttribute(idAttr), node);
		let before = anchor;
		for (const id of wanted) {
			let node = byID.get(id);
			if (node) {
				byID.delete(id);
			} else {
				// The operation that fills this arrives later in the same
				// response. Until then the placeholder holds the position, which
				// is what keeps the order the server stated.
				node = document.createElement(placeholderElement);
				node.setAttribute(idAttr, id);
			}
			// insertBefore moves a node that is already in the document, so a
			// reorder costs one move per element and no re-render.
			container.insertBefore(node, before);
			before = node.nextSibling;
		}
		// Whatever the list did not name is gone from the render.
		for (const node of byID.values()) node.remove();
		return true;
	}

	// directBoundaries returns the update boundaries immediately inside one,
	// skipping the ones nested deeper: a grandchild belongs to its own parent's
	// arrangement rather than to this one.
	function directBoundaries(parent) {
		const found = [];
		for (const node of parent.querySelectorAll("[" + idAttr + "]")) {
			const enclosing = node.parentElement && node.parentElement.closest("[" + idAttr + "]");
			if (enclosing === parent) found.push(node);
		}
		return found;
	}

	// A manifest entry is four fields, not two. The frame validator says whether
	// a boundary's own markup changed; the children validator says whether the
	// arrangement of the boundaries inside it did, which is what lets a list that
	// gained a row be answered by naming the new order rather than by re-sending
	// the list. Holding only the frame makes every parent's arrangement compare
	// unequal, so the server restates one on every navigation.
	function recordValidator(entry) {
		if (entry && typeof entry.id === "string" && typeof entry.frame === "string") {
			manifest.set(entry.id, {
				frame: entry.frame,
				children: typeof entry.children === "string" ? entry.children : "",
				parent: typeof entry.parent === "string" ? entry.parent : "",
			});
		}
	}

	function replaceManifest(entries) {
		manifest.clear();
		for (const entry of entries || []) recordValidator(entry);
	}

	// recordManifest merges entries into what this client holds, for a response
	// that describes part of the page rather than all of it. A redraw is that
	// case: it renders one component and the boundaries under it, and says
	// nothing about the rest of the screen, so clearing would throw away every
	// validator the last navigation established.
	function recordManifest(entries) {
		for (const entry of entries || []) recordValidator(entry);
	}

	// seedManifest reads the validators of the document this runtime loaded into.
	//
	// Without it every page view starts blank, so the first navigation after
	// arriving — the click a reader is most likely to make — carries no hints and
	// is answered with every region of the page. The marker is written by the
	// document that produced these boundaries, which is the one DOM its
	// validators are valid for.
	//
	// It is consumed rather than left: it describes the document as it was
	// delivered, and after the first delta the page is no longer that document.
	function seedManifest() {
		const marker = document.querySelector(manifestElement);
		if (!marker) return;
		const encoded = marker.getAttribute("value") || "";
		marker.remove();
		// The server's encoder read backwards, which is the same parse the
		// header form takes because it is the same encoding.
		for (const entry of encoded.split(",")) {
			const [id, frame, children, parent] = entry.split(":");
			if (!id || !frame) continue;
			manifest.set(id, { frame: frame, children: children || "", parent: parent || "" });
		}
	}

	// The trailing fields are written only when they carry something, which is
	// what keeps a flat page's manifest the size it always was. The order and the
	// omission rule are the server's encoder read backwards; a parent with no
	// children validator still writes an empty field, because the two positions
	// cannot be told apart otherwise.
	function manifestValue() {
		if (!manifest.size) return "";
		const entries = [];
		for (const [id, held] of manifest) {
			let entry = id + ":" + held.frame;
			if (held.children || held.parent) entry += ":" + held.children;
			if (held.parent) entry += ":" + held.parent;
			entries.push(entry);
		}
		return entries.join(",");
	}

	// A navigation, a refinement of the current route, and a back or forward are
	// one request. What differs is only what happens to the history entry and to
	// the viewport afterwards, and naming the three keeps that from being decided
	// by a boolean that means something different at each call site.
	//
	//	push    a link or a GET form: a new entry, and a new page to be at the top of
	//	replace the same page with different arguments: the entry and the viewport stay
	//	pop     back or forward: the browser already moved, so nothing is written
	async function go(url, mode) {
		const target = new URL(url, document.baseURI);
		if (target.origin !== location.origin) return fall(target.href, "cross-origin");
		// Recorded before anything is swapped. After the delta lands the page is a
		// different height and the position the user was at may already have
		// clamped, so reading it afterwards describes somewhere they never were.
		if (mode === "push") rememberScroll();
		const request = claim("navigation");
		markBusy(true);
		try {
			return await navigateClaimed(target, mode, request);
		} finally {
			markBusy(false);
			request.release();
		}
	}

	async function navigateClaimed(target, mode, request) {
		const current = request.current;
		emit("start", { url: target.href });
		const validators = manifestValue();
		let response;
		try {
			response = await fetch(target.href, {
				headers: withCSRF(headers("navigation", validators ? { [manifestHeader]: validators } : null)),
				credentials: "same-origin",
				redirect: "error",
				signal: request.signal,
			});
		} catch (error) {
			if (!current()) return { applied: false, superseded: true };
			return fall(target.href, "network");
		}
		if (!response.ok) return fall(target.href, "status");
		// A build mismatch is answered as a document, so the mode check catches it
		// with no separate branch: the page reloads and arrives holding the new
		// client, which is what makes deploying the two halves independent.
		if (served(response) !== "navigation") return fall(target.href, "not-a-delta");
		if (!current()) return { applied: false, superseded: true };

		// The order below is the contract, and every step of it is about the page
		// being left rather than the one arriving.
		//
		// An open composition goes first, because waiting for it yields: every
		// decision after this point has to be made against the page as it is
		// once the user has finished spelling, not as it was when the response
		// arrived.
		const composition = afterComposition();
		if (composition) {
			await composition;
			if (!current()) return { applied: false, superseded: true };
		}
		// The outgoing page's live connection goes next: it is executing that
		// page on the server and delivering to boundary ids this response is
		// about to reuse, so a delivery landing between here and the last record
		// writes the old page's content into the new page's region.
		//
		// Pending redraws go with it, for the same reason at a smaller scale.
		stopLive();
		abortRedraws();

		const type = response.headers.get("Content-Type") || "";
		const outcome = type.includes("ndjson")
			? await consumeStream(response, current)
			: await consumeBuffered(response, current);
		if (outcome.fellBack) return fall(target.href, outcome.reason);
		if (outcome.superseded) return { applied: false, superseded: true };
		if (outcome.navigate) {
			const destination = resolveNavigable(outcome.navigate);
			// A target that would run script instead of navigating is a failure
			// like any other here, so it takes the ordinary path: reload, and
			// name the reason for whoever is watching the events.
			if (!destination) return fall(location.href, "unsafe-navigate");
			// The weakest of the lifecycle names, and kept for the reason the
			// specification gives: the page is about to go away, so a handler has
			// little time to act, but logging one is cheap and its absence is
			// invisible. Fired before the assignment, because after it there is
			// no page left to fire on.
			dispatchSignal(signalDirectiveReceived, { directive: "navigate", url: destination });
			location.assign(destination);
			return { applied: false, navigated: true };
		}

		// The scope chain of the page just applied. It runs after every operation
		// landed, so a setup reading the DOM sees the page it belongs to rather
		// than the one being replaced, and before the live connection opens, so a
		// setup registering a signal handler is in the table when the first
		// record arrives.
		//
		// A response carrying no chain header means a composition with no scoped
		// script, which releases everything currently mounted rather than leaving
		// the outgoing page's scripts running over the incoming page.
		applyScopeCatalog(parseScopeCatalog(response.headers.get(scopeChainHeader)));
		// History moves only after the response committed, so a delta that failed
		// leaves the address bar describing what is actually on screen.
		commitHistory(target, mode);
		settlePlace(target, mode);
		// And the new page's connection opens last, after every record landed and
		// after the viewport settled. Opening it earlier would have it deliver
		// into regions this response had not written yet, which is the same
		// collision as the outgoing one in the other direction.
		if (outcome.live || response.headers.get(liveHeader) === "1") startLive();
		emit("applied", { url: target.href });
		// The lifecycle name, after the delta is in the DOM and the scopes of the
		// page it belongs to are mounted. An analytics or scroll-restoration
		// handler wants the URL now displayed, which is why that is what it
		// carries rather than the one it left.
		dispatchSignal(signalNavigationApplied, { url: target.href });
		return { applied: true };
	}

	async function consumeBuffered(response, current) {
		let body;
		try {
			body = await response.json();
		} catch (error) {
			return { fellBack: true, reason: "unreadable" };
		}
		if (!current()) return { superseded: true };
		installHead(body.head);
		for (const operation of body.ops || []) {
			if (!(await applyOperation(operation))) return { fellBack: true, reason: "missing-target" };
		}
		replaceManifest(body.manifest);
		return { navigate: body.navigate, live: body.live === true };
	}

	// A streamed delta applies each region as it is written rather than when the
	// response ends, which is what makes a slow region cost only itself.
	async function consumeStream(response, current) {
		if (!response.body || !response.body.getReader) return { fellBack: true, reason: "not-a-stream" };
		// Validators are held aside until the terminator arrives. A truncated
		// stream must not leave this client claiming regions it never received,
		// because the server would then omit them forever.
		const pending = [];
		let ended = null;
		let navigate = null;
		try {
			for await (const record of readRecords(response.body)) {
				if (!current()) return { superseded: true };
				if (record.r === "head") {
					// The opening record repeats the build, so a server redeployed
					// under an open connection is detectable without reading the
					// response headers again.
					if (record.build && record.build !== config.build) {
						return { fellBack: true, reason: "stale-build" };
					}
					installHead(record.head);
					continue;
				}
				if (record.r === "op") {
					// All three fields of a manifest entry travel on every
					// operation record, including an unchanged one, because a
					// client rebuilding its manifest from a stream has no other
					// source for them and the next request is compared against
					// all three: the frame decides whether a region is resent,
					// the children digest whether a list looks reordered, and the
					// parent whether a disappearing boundary can be narrowed to
					// something smaller than replacing the outermost one.
					pending.push({
						id: record.id, frame: record.frame,
						children: record.children, parent: record.parent,
					});
					// A record with no kind restates a validator and nothing else:
					// the region is unchanged, so it is recorded and not applied.
					// Every kind is dispatched, because a kind carrying no markup
					// is not the same statement — a children operation says the
					// arrangement moved while the markup stayed.
					if (!record.kind) continue;
					if (!(await applyOperation(record))) return { fellBack: true, reason: "missing-target" };
					continue;
				}
				if (record.r === "await") {
					// A placeholder id names a hole inside a region already
					// installed, which is a different namespace from an instance
					// id and lands through the boundary half of this asset.
					applyHTML(record.id, record.html);
					continue;
				}
				if (record.r === "signal") {
					// The delta stream and the live stream are one record grammar
					// written by one encoder upstream, so a signal can arrive on
					// either. Dispatched here rather than collected, because the
					// order a source emitted its signals in relative to its
					// deliveries is the only ordering guaranteed at all.
					if (typeof record.name === "string") dispatchSignal(record.name, record.data);
					continue;
				}
				if (record.r === "end") {
					// The terminator is the last record by contract, so nothing
					// after it is read rather than read and ignored.
					ended = record;
					break;
				}
				if (record.r === "navigate" && record.url) navigate = record.url;
				// Anything else is from a newer server and is ignored.
			}
		} catch (error) {
			return { fellBack: true, reason: "network" };
		}
		// A stream that ended with no terminator was cut off rather than finished.
		if (!ended) return { fellBack: true, reason: "truncated" };
		if (ended.reason === "failed") {
			// Content already applied stays; what was not described is not
			// claimed, so the manifest is not updated.
			emit("failed", { error: ended.error });
			return { navigate: navigate };
		}
		replaceManifest(pending);
		return { navigate: navigate, live: ended.reason === "live_pending" };
	}

	function scrollState() {
		return { pwScroll: [window.scrollX, window.scrollY] };
	}

	// The entry being left records where the user actually was. Writing that
	// position into the entry being pushed — which is what this used to do —
	// describes the page they are arriving at, so back restored the scroll of the
	// page it came from and the entry a session opened on held nothing at all.
	function rememberScroll() {
		history.replaceState(scrollState(), "", location.href);
	}

	function commitHistory(target, mode) {
		// A pop already moved the browser. Writing an entry here would overwrite
		// the position that is about to be restored from it.
		if (mode === "pop") return;
		if (mode === "push") history.pushState(scrollState(), "", target.href);
		else history.replaceState(scrollState(), "", target.href);
	}

	// Where the user is left, once the regions have landed.
	//
	// A pushed navigation is arriving somewhere new, so it starts where a document
	// load would have started it: at the top, or at the fragment it named. A
	// replaced one is the same page with different arguments — a filter, a sort, a
	// page number — so moving the viewport would throw away the place the user was
	// reading. A pop restores what its entry recorded, and does it in the popstate
	// handler because only that has the entry.
	function settlePlace(target, mode) {
		if (mode !== "push") return;
		const anchor = target.hash ? locateAnchor(target.hash.slice(1)) : null;
		if (anchor && anchor.scrollIntoView) anchor.scrollIntoView();
		else if (window.scrollTo) window.scrollTo(0, 0);
		settleFocus(anchor);
		// The title is installed by the head records before this runs, so what is
		// read out is the page the user arrived at rather than the one they left.
		announce(document.title);
	}

	function locateAnchor(name) {
		if (!name) return null;
		return document.getElementById(name);
	}

	// Focus that was on a node the delta removed is now on the body, where a
	// keyboard is at the top of nothing. Sending it to the landmark the page
	// already has is the documented destination; a page with no main gets the
	// body, which is what a document load would have given it anyway.
	function settleFocus(anchor) {
		const active = document.activeElement;
		if (active && active !== document.body && active.isConnected) return;
		const landing = anchor || (document.querySelector ? document.querySelector("main, [role=main]") : null);
		if (!landing || !landing.focus) return;
		// A landmark is not focusable by default, and making it programmatically
		// focusable must not put it in the tab order.
		if (landing.hasAttribute && !landing.hasAttribute("tabindex")) {
			landing.setAttribute("tabindex", "-1");
		}
		landing.focus({ preventScroll: true });
	}

	// A redraw re-renders one registered component with the values the browser
	// holds. It is addressed at the page's own URL, so it inherits whatever
	// guards that page rather than needing a rule of its own.
	async function redraw(elementId, params, options) {
		const target = document.getElementById(elementId);
		if (!target) return { applied: false, reason: "no such element" };
		const kind = target.getAttribute(kindAttr);
		// Refusing locally costs no request. A component that is not reloadable
		// carries no kind, and a page that changed under this client is a
		// different question answered by the fallback below.
		if (!kind) return { applied: false, reason: "not a reloadable component" };
		const url = new URL((options && options.url) || location.href, document.baseURI);
		if (url.origin !== location.origin) return fall(url.href, "cross-origin");
		writeQuery(url, params);
		const request = claim("redraw:" + elementId);
		markBusy(true);
		try {
			return await redrawClaimed(elementId, kind, url, request);
		} finally {
			markBusy(false);
			request.release();
		}
	}

	async function redrawClaimed(elementId, kind, url, request) {
		const current = request.current;
		emit("start", { id: elementId });
		let response;
		try {
			response = await fetch(url.href, {
				headers: withCSRF(headers("redraw", { [kindHeader]: kind, [instanceHeader]: elementId })),
				credentials: "same-origin",
				redirect: "error",
				signal: request.signal,
			});
		} catch (error) {
			if (!current()) return { applied: false, superseded: true };
			return fall(location.href, "network");
		}
		// 404 means this deployment publishes no such kind, which makes the page
		// stale rather than the region. Every refusal ends the same way.
		if (!response.ok) return fall(location.href, "status:" + response.status);
		if (served(response) !== "redraw") return fall(location.href, "not-a-redraw");
		// Since system:tinybind v0.4.4 a redraw answers with the same body an
		// action does — head, operations, and the manifest entries it rendered.
		// The head has left its header, so no base64 decode is needed and a
		// component's tags travel where every other path's do; and the validators
		// now come back, where a redrawn region used to leave a stale one behind
		// and cost the next navigation a re-send of markup already on screen.
		let body;
		try {
			body = await response.json();
		} catch (error) {
			return fall(location.href, "unreadable");
		}
		if (!current()) return { applied: false, superseded: true };
		// An open composition is waited out before anything lands, and the wait
		// yields: applyOperation locates its target again on the other side, so
		// a region replaced in the meantime is found or reported missing rather
		// than written over a stale node.
		const composition = afterComposition();
		if (composition) {
			await composition;
			if (!current()) return { applied: false, superseded: true };
		}
		installHead(body.head);
		for (const operation of body.ops || []) {
			if (!(await applyOperation(operation))) return fall(location.href, "missing-target");
		}
		recordManifest(body.manifest);
		emit("redrawn", { id: elementId });
		return { applied: true };
	}

	// apply installs what a mutating fetch returned. The application issues that
	// request itself, because it is the application's endpoint and its body.
	async function apply(response) {
		if (!response) return { applied: false, reason: "no response" };
		if (served(response) !== "action") return { applied: false, reason: "not an action response" };
		let body;
		try {
			body = await response.json();
		} catch (error) {
			return fall(location.href, "unreadable");
		}
		const composition = afterComposition();
		if (composition) await composition;
		installHead(body.head);
		for (const operation of body.ops || []) {
			// A rewritten region drops its stored validator, or a later navigation
			// could find that boundary unchanged and leave this markup in place.
			manifest.delete(operation.id);
			if (!(await applyOperation(operation))) return fall(location.href, "missing-target");
		}
		if (body.navigate) {
			const destination = resolveNavigable(body.navigate);
			if (!destination) return fall(location.href, "unsafe-navigate");
			location.assign(destination);
			return { applied: false, navigated: true };
		}
		emit("applied", {});
		// The status is not a signal to skip applying: a rejected submission
		// returns 4xx and the regions it carries are the validation errors.
		return { applied: true };
	}

	// callPageAction is a server function called by name rather than by gesture.
	//
	// The body is JSON because api:request-binding accepts it for the same input
	// struct a form posts, so one Go handler serves both and the caller passes
	// the value it already holds rather than assembling fields.
	//
	// The address is the direct endpoint, which carries no path parameter: a
	// handler needing one reads it from what the caller sent. There is no
	// element to mark and no gesture to guard, so the in-flight state is the
	// caller's — it started this and knows what it is waiting for.
	async function callPageAction(url, input) {
		const target = new URL(url, document.baseURI);
		if (target.origin !== location.origin) throw new Error("Popcorn Web: refusing a cross-origin action");
		markBusy(true);
		let response;
		try {
			// The call header says who is holding the answer. The mode still
			// says action, so a handler answering with regions is applied here
			// exactly as it is for a gesture; this is what lets one that has a
			// value to return know there is somewhere to put it.
			const headers = Object.assign(updateHeaders(), { [callHeader]: "1" });
			if (input !== undefined) headers["Content-Type"] = "application/json";
			response = await fetch(target.href, {
				method: "POST",
				body: input === undefined ? null : JSON.stringify(input),
				headers: headers,
				credentials: "same-origin",
				redirect: "error",
			});
		} finally {
			markBusy(false);
		}
		// An update response is applied the way a gesture's is. Anything else is
		// returned to the caller: it asked for this and can read a value, where
		// a gesture had nobody to hand one to.
		if (served(response) === "action") return apply(response);
		if (!response.ok) throw new Error("Popcorn Web: the action failed with " + response.status);
		const type = response.headers.get("Content-Type") || "";
		return type.includes("json") ? response.json() : response.text();
	}

	// runAction performs the mutation an element names and installs what came
	// back, which is what makes a lowered server-action a working gesture rather
	// than an address the application still has to call.
	//
	// The element is marked for the duration and a second activation of it is
	// ignored rather than queued. A redraw supersedes an older request for the
	// same id because rendering twice costs only the render; a mutation is not
	// idempotent, so the same rule inverted is the right one here.
	const running = new WeakSet();
	async function runAction(element, url, body) {
		if (running.has(element)) return { applied: false, reason: "in flight" };
		running.add(element);
		// aria-disabled rather than the disabled property: disabling the focused
		// control moves focus to the body and loses the user's place, where this
		// says the same thing to assistive technology and moves nothing. What
		// actually blocks a second activation is the guard above, which works on
		// any element rather than only on a form control.
		element.setAttribute("aria-busy", "true");
		markBusy(true);
		try {
			let response;
			try {
				response = await fetch(url, {
					method: "POST",
					body: body,
					headers: updateHeaders(),
					credentials: "same-origin",
					// A handler redirecting outside the update protocol is not
					// something a fetch can follow usefully: the target would be
					// read as this page's regions. pw.Redirect answers an update
					// request with the navigate directive instead, which apply
					// below performs, so this only trips on a handler that chose
					// the raw redirect.
					redirect: "error",
				});
			} catch (error) {
				// The request may or may not have reached the handler, and
				// nothing here can tell. Reloading shows whichever it was, where
				// resubmitting would perform the mutation a second time if the
				// first one landed.
				return fall(location.href, "unreachable");
			}
			return await apply(response);
		} finally {
			markBusy(false);
			element.removeAttribute("aria-busy");
			running.delete(element);
		}
	}

	// actionTarget is the URL a gesture on this element posts to, or nothing when
	// the element names no server function.
	//
	// A submitter's own formaction wins, which is the native per-button override
	// and the channel the lowering uses when one form dispatches to several
	// handlers. A form's own action is absent by construction — the lowering
	// writes none, so the browser posts to the document URL, which already holds
	// this page's path parameters.
	function actionTarget(element, submitter) {
		const named = attributeOf(submitter, actionAttr) || attributeOf(element, actionAttr);
		if (!named) return "";
		let target = named;
		if (element.tagName === "FORM") {
			target = attributeOf(submitter, "formaction") || element.getAttribute("action") || location.href;
		}
		// Resolved rather than passed through, because every other request this
		// client issues names an absolute URL and because the origin is worth
		// checking even for an address generation wrote: a page is markup, and
		// markup is what an injection reaches.
		let url;
		try {
			url = new URL(target, document.baseURI);
		} catch (error) {
			return "";
		}
		return url.origin === location.origin ? url.href : "";
	}

	// The headers an application fetch must carry to ask for an action response.
	function updateHeaders() {
		return withCSRF(headers("action"));
	}

	// Same-origin navigation is intercepted so a link refines the page it is on.
	// Everything the browser should keep is left to it: a modified click, a
	// target, a download, a non-GET submission — which is what keeps
	// post-redirect-get working exactly as it did.
	function intercept() {
		document.addEventListener("click", (event) => {
			if (event.defaultPrevented || event.button !== 0) return;
			if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
			// An element naming a server function, where activating it fires no
			// submit event for the hook below to take: a button outside a form,
			// or one declaring type=button inside one. A submit control is left
			// alone here precisely because that hook is the better place — it
			// sees the fields.
			const acting = event.target && event.target.closest ? event.target.closest("[" + actionAttr + "]") : null;
			if (acting && acting.tagName !== "FORM" && !(acting.form && acting.type === "submit")) {
				if (acting.closest("[" + ignoreAttr + "]")) return;
				event.preventDefault();
				// Nothing in the markup says what else to send, and assembling a
				// payload from attributes would be an addressing mechanism this
				// framework does not publish. The handler reads what the URL and
				// the session already carry.
				runAction(acting, actionTarget(acting), null);
				return;
			}
			const link = event.target && event.target.closest ? event.target.closest("a[href]") : null;
			if (!link || link.target || link.hasAttribute("download")) return;
			if (link.closest("[" + ignoreAttr + "]")) return;
			const url = new URL(link.getAttribute("href"), document.baseURI);
			if (url.origin !== location.origin) return;
			// A fragment on the document already loaded is the browser's. It has
			// the element, it knows where to put it, and a round trip could only
			// arrive at the same page — while leaving it alone is also what keeps
			// the scroll, the :target styling, and back out of a deep link working
			// the way they do on every other page.
			if (sameDocument(url) && url.hash !== new URL(location.href).hash) return;
			event.preventDefault();
			go(url.href, "push");
		});
		document.addEventListener("submit", (event) => {
			if (event.defaultPrevented) return;
			const form = event.target;
			if (!form || !form.getAttribute) return;
			if (form.closest && form.closest("[" + ignoreAttr + "]")) return;
			// A server action is decided before the navigation rules below,
			// because those are about a GET that refines the page it is on and
			// this is a mutation. Leaving it to them would read the lowered
			// form's method and hand it back to the browser.
			const submitter = event.submitter;
			const actionURL = actionTarget(form, submitter);
			if (actionURL) {
				event.preventDefault();
				const fields = new FormData(form);
				// Which button was pressed is not a property of the form, so
				// FormData leaves every submit button out and the submitter's own
				// pair is added by hand, exactly as the query path below does.
				if (submitter && submitter.name) fields.append(submitter.name, submitter.value);
				runAction(submitter || form, actionURL, fields);
				return;
			}
			// The submitter's own overrides decide the method, the action and the
			// target before this runtime decides whether it owns the submission at
			// all. Reading only the form is how a button declaring formmethod=post
			// inside a GET form used to be intercepted and re-sent as a GET.
			if ((attributeOf(submitter, "formmethod") || form.method || "get").toLowerCase() !== "get") return;
			if (attributeOf(submitter, "formtarget") || form.target) return;
			const action = attributeOf(submitter, "formaction") || form.getAttribute("action") || location.href;
			const url = new URL(action, document.baseURI);
			if (url.origin !== location.origin) return;
			event.preventDefault();
			// A search form's fields become the query, which is how a form refines
			// the page it is on rather than leaving it. The submitter's own pair
			// belongs in that query and is added by hand: the FormData constructor
			// leaves every submit button out, because which one was pressed is not
			// a property of the form.
			const fields = new FormData(form);
			if (submitter && submitter.name) fields.append(submitter.name, submitter.value);
			url.search = new URLSearchParams(fields).toString();
			go(url.href, "push");
		});
		window.addEventListener("popstate", (event) => {
			const scroll = event.state && event.state.pwScroll;
			go(location.href, "pop").then(() => {
				// Restored after the delta, because the position only exists once
				// the content that makes the page that tall is back on it.
				if (scroll && window.scrollTo) window.scrollTo(scroll[0], scroll[1]);
			});
		});
	}

	function attributeOf(node, name) {
		return node && node.getAttribute ? node.getAttribute(name) : null;
	}

	function sameDocument(url) {
		const here = new URL(location.href);
		return url.pathname === here.pathname && url.search === here.search;
	}

	// writeQuery sets one URL's search from a parameter object. An array value
	// becomes one pair per element, in the order given, which is the repeated
	// key a GET form submits for a checkbox group and the one array spelling
	// the server reads. Joining them into a single value would produce a query
	// no form could have written, and a comma inside an element could never be
	// told from the join.
	function writeQuery(url, params) {
		url.search = "";
		for (const name of Object.keys(params || {})) {
			const value = params[name];
			if (Array.isArray(value)) {
				for (const item of value) url.searchParams.append(name, String(item));
				continue;
			}
			url.searchParams.set(name, String(value));
		}
	}

	intercept();
	installAnnouncer();
	seedManifest();

	return {
		// Re-render the current route with different search parameters, replacing
		// the history entry rather than stacking one, and leaving the viewport
		// where it is because this is the page already on screen.
		//
		// The parameters given are the whole query: what is not named is dropped,
		// which is what a GET form submission does by specification and is why
		// this matches it rather than merging.
		update(params) {
			const url = new URL(location.href);
			writeQuery(url, params);
			return go(url.href, "replace");
		},
		navigate(url) {
			return go(url, "push");
		},
		redraw: redraw,
		apply: apply,
		updateHeaders: updateHeaders,
		subscribe(listener) {
			listeners.add(listener);
			return () => listeners.delete(listener);
		},
		// The signal table, from the half above. It is published here because
		// this object is the one an application reaches, and it is one table
		// rather than a second surface beside subscribe: a handler cares what
		// happened rather than which side noticed, and the reserved prefix is
		// what keeps the framework's own lifecycle names out of reach.
		//
		// subscribe stays what it is — the outcome of an update this caller
		// asked for, reported back to the caller that asked.
		registerEvent: registerEvent,
		unregisterEvent: unregisterEvent,
		activeScope: activeScope,
		// definePage separates a page's evaluation, which happens once, from its
		// activation, which happens every time it is entered. It is the shape a
		// page script wants, because an ES module cannot be re-evaluated on a
		// return visit and this needs it not to be.
		definePage: definePage,
		// The attribute names are exposed because an application writing a
		// preserve marker from script needs the same name its templates use, and
		// guessing it from a prefix it did not choose is how the two drift.
		idAttribute: idAttr,
		kindAttribute: kindAttr,
		preserveAttribute: "data-" + config.attr + "-preserve",
		ignoreAttribute: ignoreAttr,
	};
}
