package pw

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

// browserUserAgents is the guard behind the one rule every catalog entry has to
// obey: no token may appear in a User-Agent a real browser sends. It is what
// keeps a well-meaning addition like "bot" from turning an Android phone into a
// crawler.
var browserUserAgents = []string{
	chromeUserAgent,
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPad; CPU OS 18_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36",
	// A phone whose model name contains "bot". This entry is the whole reason
	// the catalog matches vendor-qualified tokens instead of the bare word.
	"Mozilla/5.0 (Linux; Android 10; CUBOT NOTE 20) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 OPR/125.0.0.0",
}

func TestCatalogTokensDoNotMatchBrowsers(t *testing.T) {
	for _, agent := range browserUserAgents {
		lowered := strings.ToLower(agent)
		for _, token := range botUserAgents {
			if strings.Contains(lowered, token) {
				t.Errorf("token %q matches the browser agent %q", token, agent)
			}
		}
		if classifyUserAgent(agent, nil) {
			t.Errorf("browser classified as a bot: %q", agent)
		}
	}
}

func TestClassifyUserAgent(t *testing.T) {
	bots := map[string]string{
		"search crawler":     "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"bing":               "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)",
		"ai crawler":         "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.2; +https://openai.com/gptbot",
		"anthropic":          "Mozilla/5.0 (compatible; ClaudeBot/1.0; +claudebot@anthropic.com)",
		"ogp spider":         "facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)",
		"x preview":          "Twitterbot/1.0",
		"slack unfurl":       "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)",
		"discord preview":    "Mozilla/5.0 (compatible; Discordbot/2.0; +https://discordapp.com)",
		"curl":               "curl/8.7.1",
		"wget":               "Wget/1.21.4",
		"python requests":    "python-requests/2.32.3",
		"python urllib":      "Python-urllib/3.12",
		"go client":          "Go-http-client/1.1",
		"java client":        "Java/17.0.9",
		"okhttp":             "okhttp/4.12.0",
		"php guzzle":         "GuzzleHttp/7",
		"perl":               "libwww-perl/6.72",
		"node":               "node-fetch/1.0 (+https://github.com/bitinn/node-fetch)",
		"ruby":               "Faraday v2.9.0",
		"powershell":         "Mozilla/4.0 (compatible; Win32; WinHttp.WinHttpRequest.5)",
		"postman":            "PostmanRuntime/7.42.0",
		"scrapy":             "Scrapy/2.11.2 (+https://scrapy.org)",
		"empty":              "",
		"whitespace only":    "   ",
		"unknown cli":        "some-tool/3.1",
		"unnamed but polite": "SomeAggregator (+https://example.com/about-our-crawler)",
	}
	for name, agent := range bots {
		if !classifyUserAgent(agent, nil) {
			t.Errorf("%s was not classified as a bot: %q", name, agent)
		}
	}

	// PowerShell's WinHttp agent claims Mozilla/4.0, so it is caught by a
	// catalog token rather than the prefix rule. Verify the prefix rule alone
	// would not have caught it, so the assertion above is not accidental.
	if !strings.HasPrefix(strings.ToLower(bots["powershell"]), "mozilla/") {
		t.Error("the WinHttp fixture no longer exercises token matching")
	}
}

func TestExtraBotUserAgentsExtendTheCatalog(t *testing.T) {
	capture := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/140.0.0.0 Safari/537.36"
	if classifyUserAgent(capture, nil) {
		t.Fatal("a headless browser runs the runtime, so it must not be a bot by default")
	}
	if !classifyUserAgent(capture, []string{"headlesschrome"}) {
		t.Error("a configured addition did not classify")
	}
	// Additions are lowercased on the way in, so an operator writing the agent
	// the way the vendor spells it still matches.
	if !classifyUserAgent(capture, normalizeBotUserAgents([]string{" HeadlessChrome "})) {
		t.Error("a configured addition was not normalized")
	}
}

func TestIsBotHonorsConfiguration(t *testing.T) {
	crawler := httptest.NewRequest(http.MethodGet, "/", nil)
	crawler.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")

	if !isBotRequest(crawler, HTMLConfig{BotDetection: true}) {
		t.Error("a crawler was not classified")
	}
	if isBotRequest(crawler, HTMLConfig{BotDetection: false}) {
		t.Error("detection disabled must classify every client as a browser")
	}
	if isBotRequest(browserRequest("/"), HTMLConfig{BotDetection: true}) {
		t.Error("a browser was classified as a bot")
	}
	// IsBot reads the binding the way a handler does. The registered default
	// enables detection, and httptest.NewRequest sends no User-Agent, which is
	// the "script that never set one" case rather than an unconfigured one.
	if !IsBot(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Error("a request with no User-Agent should classify as a bot")
	}
	if IsBot(browserRequest("/")) {
		t.Error("a browser request should not classify as a bot")
	}
	if IsBot(nil) {
		t.Error("a nil request must not panic or classify")
	}
}

// TestBotRequestRendersTheSettledDocument is the point of the feature: a
// crawler receives the page rather than the fallback it can never replace.
func TestBotRequestRendersTheSettledDocument(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)")
	request = request.WithContext(withTestHTMLConfig(request.Context(),
		HTMLConfig{Streaming: true, BotDetection: true}))

	WriteHTML(recorder, request, asyncPage(asyncPageParams{Body: Resolved("ready")}))

	body := recorder.Body.String()
	if body != "<main><p>ready</p></main>" {
		t.Fatalf("body = %q", body)
	}
	if strings.Contains(body, "tb-apply") || strings.Contains(body, "tb-boundary") {
		t.Error("boundary framing reached a client that cannot apply it")
	}
	if recorder.Header().Get("Content-Length") == "" {
		t.Error("the buffered branch should declare a Content-Length")
	}
}

// TestAwaitCapableResponseVariesOnUserAgent guards the cache correctness the two
// branches create: one URL now has two byte representations.
func TestAwaitCapableResponseVariesOnUserAgent(t *testing.T) {
	for name, agent := range map[string]string{"browser": chromeUserAgent, "bot": "curl/8.7.1"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("User-Agent", agent)
		request = request.WithContext(withTestHTMLConfig(request.Context(),
			HTMLConfig{Streaming: true, BotDetection: true}))

		WriteHTML(recorder, request, asyncPage(asyncPageParams{Body: Resolved("ready")}))
		if !strings.Contains(recorder.Header().Get("Vary"), "User-Agent") {
			t.Errorf("%s: Vary = %q", name, recorder.Header().Get("Vary"))
		}
	}
}

// TestStaticResponseDoesNotVaryOnUserAgent is the other half of that rule: a
// page with one representation keeps a shared cache entry that is worth having.
func TestStaticResponseDoesNotVaryOnUserAgent(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("User-Agent", "curl/8.7.1")

	builder := htmlbind.Builder[struct{}]{}
	page := (&htmlbind.Plan[struct{}]{Ops: []htmlbind.Op[struct{}]{
		builder.Static("<main>static</main>"),
	}}).Bind(struct{}{})

	WriteHTML(recorder, request, page)
	if strings.Contains(recorder.Header().Get("Vary"), "User-Agent") {
		t.Errorf("Vary = %q, but this page renders the same for every client",
			recorder.Header().Get("Vary"))
	}
}

// TestBotAsyncTimeoutReplacesTheBrowserBound covers the bound a bot request
// actually needs: it waits for every boundary before a byte leaves, so the
// browser bound — sized for how long a fallback may stay on screen — is
// answering a different question.
func TestBotAsyncTimeoutReplacesTheBrowserBound(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("User-Agent", "curl/8.7.1")
	config := HTMLConfig{
		Streaming:       true,
		BotDetection:    true,
		AsyncTimeout:    10 * time.Millisecond,
		BotAsyncTimeout: 2 * time.Second,
	}
	request = request.WithContext(withTestHTMLConfig(request.Context(), config))

	slow := Go(request.Context(), func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(120 * time.Millisecond):
			return "late", nil
		}
	})
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, request, asyncPage(asyncPageParams{Body: slow}))

	if body := recorder.Body.String(); body != "<main><p>late</p></main>" {
		t.Fatalf("body = %q, want the value the browser bound would have cut off", body)
	}
}

// TestZeroBotAsyncTimeoutFallsBackToTheBrowserBound keeps a misread key from
// holding a crawler connection open for the whole request deadline.
func TestZeroBotAsyncTimeoutFallsBackToTheBrowserBound(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("User-Agent", "curl/8.7.1")
	request = request.WithContext(withTestHTMLConfig(request.Context(), HTMLConfig{
		Streaming: true, BotDetection: true, AsyncTimeout: 20 * time.Millisecond,
	}))

	slow := Go(request.Context(), func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "late", nil
		}
	})
	recorder := httptest.NewRecorder()
	WriteHTML(recorder, request, asyncPage(asyncPageParams{Body: slow}))

	if !strings.Contains(recorder.Body.String(), `<p class=failed>timeout</p>`) {
		t.Fatalf("the browser bound did not apply: %q", recorder.Body.String())
	}
}

// TestBotSeesTheTruthfulStatus is the benefit of the branch rather than a
// feature of its own: nothing is committed, so a boundary that failed with no
// recover clause still carries a real status instead of the 200 a streamed
// document swap has to keep.
func TestBotSeesTheTruthfulStatus(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)")
	request = request.WithContext(withTestHTMLConfig(request.Context(),
		HTMLConfig{Streaming: true, BotDetection: true}))
	recorder := httptest.NewRecorder()

	WriteHTML(recorder, request, noRecoverPage(asyncPageParams{
		Body: Failed[string](errors.New("upstream is down"))}))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want the truthful status a crawler can act on", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "upstream is down") {
		t.Error("the raw Go error reached the page")
	}
}
