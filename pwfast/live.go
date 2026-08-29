package pwfast

import (
	"bufio"
	"context"
	"errors"
	"io"

	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinybind-go/htmlbind/delta"
	"github.com/shibukawa/tinygodriver/fasthttp"
)

// LiveManifestHeader carries what each boundary on the client is showing, so a
// reconnect re-sends only what changed.
const LiveManifestHeader = "Pw-Live-Manifest"

// ResponseModeHeader selects deliveries over a document, and LiveResponseMode
// is the token that does it. They are declared here rather than imported from
// the other half for the reason LiveManifestHeader is: the two transports share
// a wire, not a package.
const ResponseModeHeader = "Pw-Response-Mode"

// LiveResponseMode is the token that selects a delivery stream.
const LiveResponseMode = "live"

// ServeLive answers a live mode request with the deliveries of one chain, and
// reports whether it did.
//
// Every decision it makes is pwruntime's: the admission bound, the watchdog,
// the digest suppression seeded from the client's manifest, the record framing
// and the close reasons. What is written here is the transport — obtaining a
// writer that flushes, and reading the two request values the loop needs — so
// the two runtimes cannot disagree about the wire.
//
// The body writer runs after the handler has returned, which is this
// transport's whole shape, and it changes one thing the other half relies on:
// the request value is pooled and gone by then, so nothing may be read from it
// inside the loop and the render is bounded by the watchdog rather than by the
// request context. A client that goes away is noticed on the next write, which
// is the same signal the other half falls back to once a record fails.
func ServeLive(r *fasthttp.RequestCtx, wrappers []HTMLWrapper, leaf HTMLFragment, options ...HTMLOption) bool {
	settings, ok := pwruntime.ResolvedUpdateSettings()
	if !ok || !settings.Live {
		// The live switch, not the update one. They are separate settings
		// answering separate requests, and reading the wrong one made every
		// subscription fall through to a document.
		return false
	}
	if !liveModeRequested(r) {
		return false
	}

	// Everything the loop needs is read here, while the request value is still
	// alive. What crosses into the callback is values.
	manifest := string(r.Request.Header.Peek(LiveManifestHeader))
	clientKey := liveClientKey(r)
	digestKey := pwruntime.LiveDigestKey(settings.ValidatorKey)
	maxBoundaries := settings.LiveMaxBoundaries
	onScreen := pwruntime.ParseLiveManifest(manifest, digestKey, manifestEntries(settings))

	release, admitted := pwruntime.AdmitLive(clientKey, settings.LiveMaxResponses)
	if !admitted {
		writeLiveHeaders(r)
		_, _ = pwruntime.WriteLiveClose(r, nil, pwruntime.LiveCloseRetry, pwruntime.LiveRetryHint)
		return true
	}

	if !htmlbind.HasLiveBlock(wrappers, leaf) {
		// The client should not have asked; the document marker told it so.
		// Answering immediately beats holding a connection open for deliveries
		// that cannot exist.
		release()
		writeLiveHeaders(r)
		_, _ = pwruntime.WriteLiveClose(r, nil, pwruntime.LiveCloseDone, 0)
		return true
	}

	writeLiveHeaders(r)
	head, headErr := delta.DeltaStreamHead(wrappers, leaf, settings.RenderOptions(context.Background())...)
	if headErr != nil {
		// A chain whose head cannot be assembled is not a reason to refuse the
		// stream: the tags a delivery newly needs are the rare case.
		head = nil
	}
	r.SetBodyStreamWriter(func(w *bufio.Writer) {
		defer release()
		runLiveStream(w, wrappers, leaf, settings, digestKey, onScreen, maxBoundaries, head, options)
	})
	return true
}

// runLiveStream is the loop, over the shared protocol.
// runLiveStream writes the deliveries of one chain until the source is done or
// the watchdog closes it.
//
// The writer is buffered, which is this transport's shape rather than a choice:
// a body stream writer is handed a bufio.Writer. So every record is flushed the
// moment it is written. A live stream that filled a buffer and sent nothing
// would hold a connection open, log deliveries, and show a reader a page that
// never changes — which is what it did before the flushes were here.
func runLiveStream(w *bufio.Writer, wrappers []HTMLWrapper, leaf HTMLFragment,
	settings pwruntime.UpdateSettings, digestKey []byte, onScreen map[string]string,
	maxBoundaries int, head []string, options []HTMLOption,
) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog := pwruntime.StartLiveWatchdog(cancel,
		pwruntime.LiveLifetime(settings.LiveMaxDuration, settings.LiveDurationJitter),
		settings.LiveIdleTimeout)
	defer watchdog.Stop()

	var scratch []byte
	scratch, err := pwruntime.WriteLiveHead(w, scratch, settings.BuildID, head)
	if err != nil || w.Flush() != nil {
		return
	}
	// One HMAC state serves every delivery of this stream rather than one per
	// digest.
	digester := pwruntime.NewLiveDigester(digestKey)
	reason := pwruntime.LiveCloseDone
	boundaries := map[string]struct{}{}
	signalBytes := 0
	render := append(settings.RenderOptions(ctx), options...)
	for content, err := range htmlbind.RenderChainLive(ctx, io.Discard, wrappers, leaf, render...) {
		if err != nil {
			// Classified ahead of every failure branch, exactly as the net/http
			// half does. A signal travels the error slot the way fs.SkipDir
			// does: it is not a fault and it ends nothing.
			//
			// This loop is a second reading of the same protocol, which is the
			// thing pwruntime exists to prevent, so the two must agree — a
			// backend that ended its stream on the first signal would answer a
			// different wire from the same page.
			if signal, ok := htmlbind.AsSignal(err); ok {
				if pwruntime.ReservedSignalName(signal.Name()) {
					// This framework's namespace carries the lifecycle names its
					// client runtime dispatches, and a handler trusts one because
					// application data has no route to it.
					continue
				}
				// The same budget the other half enforces, for the same reason: a
				// payload is the one size an application chooses directly, on a
				// connection that lives as long as a tab.
				signalBytes += len(signal.Payload())
				if settings.LiveMaxSignalBytes > 0 && signalBytes > settings.LiveMaxSignalBytes {
					reason = pwruntime.LiveCloseRetry
					break
				}
				var signalErr error
				if scratch, signalErr = pwruntime.WriteLiveSignal(w, scratch, signal); signalErr != nil {
					return
				}
				// A signal is activity: the source produced something, and a
				// screen driven entirely by signals must not close as idle.
				watchdog.Delivered()
				continue
			}
			var unrecovered *htmlbind.UnrecoveredError
			if errors.As(err, &unrecovered) {
				// A boundary that failed with no recover clause has nothing
				// left to deliver, and reconnecting would only reproduce it.
				reason = pwruntime.LiveCloseDone
				break
			}
			reason = pwruntime.LiveCloseRetry
			break
		}
		if _, known := boundaries[content.BoundaryID]; !known {
			if maxBoundaries > 0 && len(boundaries) >= maxBoundaries {
				reason = pwruntime.LiveCloseDone
				break
			}
			boundaries[content.BoundaryID] = struct{}{}
		}
		digest := digester.Digest(content.HTML)
		if digest != "" && onScreen[content.BoundaryID] == digest {
			// This region is already showing these bytes. The watchdog still
			// counts it: the source produced a value, so the stream is not idle.
			watchdog.Delivered()
			continue
		}
		if scratch, err = pwruntime.WriteLiveDelivery(w, scratch, content, digest); err != nil {
			// A write failure is the client going away, and there is nobody
			// left to send a close record to.
			return
		}
		if w.Flush() != nil {
			return
		}
		if digest != "" {
			onScreen[content.BoundaryID] = digest
		}
		watchdog.Delivered()
	}
	if ctx.Err() != nil && reason == pwruntime.LiveCloseDone {
		// A bound this server chose ended a healthy stream, so the client is
		// expected back rather than told to stop.
		reason = pwruntime.LiveCloseRetry
	}
	hint := pwruntime.LiveRetryHint
	if reason != pwruntime.LiveCloseRetry {
		hint = 0
	}
	_, _ = pwruntime.WriteLiveClose(w, scratch, reason, hint)
	// The close record is the one a client acts on — it says whether to
	// reconnect — so it is flushed like every other.
	_ = w.Flush()
}

// writeLiveHeaders commits the response. A delivery stream is never shareable,
// and what it omits depends on what the request claimed, so a cache that
// ignored the manifest could serve one screen's suppressions to another.
func writeLiveHeaders(r *fasthttp.RequestCtx) {
	r.Response.Header.SetContentType(pwruntime.LiveMediaType)
	r.Response.Header.Set("Cache-Control", "no-store")
	r.Response.Header.Set("X-Content-Type-Options", "nosniff")
	r.Response.Header.Add("Vary", "Pw-Render")
	r.Response.Header.Add("Vary", LiveManifestHeader)
	r.SetStatusCode(fasthttp.StatusOK)
}

// liveClientKey names the client the admission bound counts against: the
// authenticated subject where there is one, and the resolved caller otherwise,
// because an anonymous screen is still one browser and grouping every anonymous
// client together would refuse the second visitor of the day.
func liveClientKey(r *fasthttp.RequestCtx) string {
	if subject := pwruntime.RequestAuthentication(r); subject.Authenticated && subject.Subject != "" {
		return "subject:" + subject.Subject
	}
	// The address the chain resolved rather than the peer, which is what the
	// rate limiter of this same transport already counts against: behind a
	// terminating proxy the peer is the proxy, and every anonymous visitor would
	// collapse into one bucket and be refused from the fifth of them at the
	// shipped default. The peer is the fallback for a handler served outside the
	// framework's own frames, where nothing resolved one.
	if address := pwruntime.ReadClientAddress(r); address != "" {
		return "remote:" + address
	}
	return "remote:" + r.RemoteIP().String()
}

func manifestEntries(settings pwruntime.UpdateSettings) int {
	if settings.LiveMaxBoundaries > 0 {
		return settings.LiveMaxBoundaries
	}
	return pwruntime.DefaultLiveManifestEntries
}

// liveModeRequested reports whether this request asked for deliveries instead
// of a document.
//
// It reads the header the client sends, which is this framework's own rather
// than the update module's mode token: the browser runtime this framework ships
// sets Pw-Response-Mode, and the other transport reads exactly that. Reading
// the module's header instead recognized no subscription at all, and a request
// nobody recognized was answered with a page.
//
// An unknown value is not an error: it answers the document, so an older client
// meeting a newer server stays functional.
func liveModeRequested(r *fasthttp.RequestCtx) bool {
	return string(r.Request.Header.Peek(pwruntime.ResponseModeHeader)) == pwruntime.LiveResponseMode
}
