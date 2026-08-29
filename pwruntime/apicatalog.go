package pwruntime

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// The api-catalog well-known endpoint of RFC 9727.
//
// The path is the standard's rather than the deployment's, which is why it is a
// constant here and a boolean in the configuration: every other operational
// endpoint carries its path so an operator reading a settings file sees every
// address the deployment answers on, and this one is stated by its own name.
const (
	APICatalogPath      = "/.well-known/api-catalog"
	APICatalogProfile   = "https://www.rfc-editor.org/info/rfc9727"
	APICatalogMediaType = "application/linkset+json"
	// APICatalogContentType carries the profile parameter RFC 9727 section 4.2
	// recommends, which is what tells a reader that this particular Linkset is
	// an API catalog rather than any other set of links.
	APICatalogContentType = APICatalogMediaType + `; profile="` + APICatalogProfile + `"`
	// APICatalogLinkHeader is the Link header a HEAD answer carries, per
	// section 2. It names this same location because the document is served
	// here rather than resolved to somewhere else, which section 3 permits.
	APICatalogLinkHeader = "<" + APICatalogPath + `>; rel="api-catalog"`
)

// APICatalogSettings are the endpoints the catalog links, which is every
// endpoint the deployment already configured.
//
// Nothing here is authored for the catalog. RFC 9727 describes collating API
// metadata as the expensive step of publishing one, and that step is absent
// because the three targets below are the three the settings file already
// names.
type APICatalogSettings struct {
	Enabled bool
	// Origin is the absolute scheme-and-host the links are built from. Empty
	// writes them relative rather than guessing one; see buildAPICatalog.
	Origin string
	// OpenAPI, APIDoc and APIDocPath are the document and the UI over it;
	// Health is the liveness probe. Each empty setting removes its link.
	OpenAPI    string
	APIDoc     string
	APIDocPath string
	Health     string
}

// ResolvedAPICatalog is a validated catalog and the document it answers with.
//
// The two transports share it for the reason ResolveCORS is shared: a
// misconfiguration is an error before a port is bound, and neither transport
// computes a document of its own. The document is assembled once here, because
// it depends on the settings and on nothing about a request.
type ResolvedAPICatalog struct {
	enabled  bool
	document []byte
}

// Enabled reports whether the endpoint answers.
func (r ResolvedAPICatalog) Enabled() bool { return r.enabled }

// Document returns the catalog. It is the same bytes for every caller.
func (r ResolvedAPICatalog) Document() []byte { return r.document }

// ResolveAPICatalog validates the configuration and reduces it to what the
// frame writes.
//
// A catalog with nothing to link is refused rather than served: the document
// would carry a status link and a self-referential item link and no link to any
// API description, which satisfies the section 4.1 requirement on no reading.
func ResolveAPICatalog(settings APICatalogSettings) (ResolvedAPICatalog, error) {
	if !settings.Enabled {
		return ResolvedAPICatalog{}, nil
	}
	if strings.TrimSpace(settings.OpenAPI) == "" && strings.TrimSpace(settings.APIDoc) == "" {
		return ResolvedAPICatalog{}, errors.New(
			"server.api_catalog is on with neither server.openapi nor server.api_doc to link")
	}
	origin, err := ValidateAPICatalogOrigin(settings.Origin)
	if err != nil {
		return ResolvedAPICatalog{}, err
	}
	settings.Origin = origin
	return ResolvedAPICatalog{enabled: true, document: buildAPICatalog(origin, settings)}, nil
}

// ValidateAPICatalogOrigin accepts an absolute scheme-and-host origin and
// returns it without a trailing slash. The empty value is legal and means the
// links are written relative, per the note on buildAPICatalog.
func ValidateAPICatalogOrigin(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("server.api_catalog_origin %q is not a URL: %w", value, err)
	}
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return "", fmt.Errorf("server.api_catalog_origin %q must name http or https", value)
	case parsed.Host == "":
		return "", fmt.Errorf("server.api_catalog_origin %q names no host", value)
	case parsed.User != nil:
		return "", fmt.Errorf("server.api_catalog_origin %q carries userinfo", value)
	case parsed.RawQuery != "" || parsed.Fragment != "":
		return "", fmt.Errorf("server.api_catalog_origin %q carries a query or fragment", value)
	case strings.Trim(parsed.Path, "/") != "":
		return "", fmt.Errorf("server.api_catalog_origin %q carries a path", value)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// normalizeAPICatalogOrigin drops a trailing slash so the joins below produce
// one, and only one.
func normalizeAPICatalogOrigin(origin string) string {
	return strings.TrimSuffix(strings.TrimSpace(origin), "/")
}

// buildAPICatalog serializes the RFC 9264 Linkset.
//
// An empty origin writes every anchor and href as an absolute-path reference —
// /openapi.json rather than https://host/openapi.json — which is the answer for
// a deployment that has not named the origin it answers on.
//
// RFC 9264 says such a reference SHOULD NOT be used, and its reason is reuse:
// a link set passed around outside the HTTP exchange that delivered it has lost
// the context a relative reference resolves against. That is a real cost, and
// naming server.api_catalog_origin is what pays it.
//
// It is not a reason to guess. The only origin a framework could guess from is
// the request's own Host, which the caller chose; both forms then resolve to
// the same place for the client in front of us, and they differ only once the
// document outlives that exchange — exactly the case the recommendation is
// about. There, a guessed absolute URI is durable and may name a host this
// deployment does not own, while a relative reference can only ever mean
// wherever the document was read from. So an unnamed origin writes what it
// knows instead of asserting what it does not, and the document stays the same
// bytes for every caller.
//
// It is written by hand rather than marshalled so that both transports emit the
// same bytes for the same settings, and so that the shared leaf carries no
// reflection a second build has to link.
//
// The shape is RFC 9727 Appendix A.1 -- the typed relations of RFC 8631 -- plus
// one Appendix A.2 item link. A.1 alone leaves the API endpoint appearing only
// as an anchor, and section 4.1 reads on the strictest interpretation as
// requiring a link whose target is that endpoint; the item link costs one entry
// and leaves neither reading to be argued at a client.
func buildAPICatalog(origin string, settings APICatalogSettings) []byte {
	api := origin + "/"
	var out strings.Builder
	out.WriteString(`{"linkset":[{"anchor":`)
	writeJSONString(&out, origin+APICatalogPath)
	out.WriteString(`,"item":[{"href":`)
	writeJSONString(&out, api)
	out.WriteString(`}]}`)

	var links strings.Builder
	// Written in the order a reader wants them: the machine contract, then the
	// page over it, then the probe.
	writeAPICatalogLink(&links, "service-desc", origin, settings.OpenAPI, "application/json")
	if settings.APIDoc != "" {
		writeAPICatalogLink(&links, "service-doc", origin, settings.APIDocPath, "text/html")
	}
	// No type member: the probe answers with a status code and a word, and a
	// declared media type would be a claim about bytes a HEAD never sends.
	writeAPICatalogLink(&links, "status", origin, settings.Health, "")
	if links.Len() > 0 {
		out.WriteString(`,{"anchor":`)
		writeJSONString(&out, api)
		out.WriteString(links.String())
		out.WriteString(`}`)
	}
	out.WriteString(`]}`)
	return []byte(out.String())
}

// writeAPICatalogLink appends one relation member, or nothing when the setting
// it comes from is unset.
//
// The value is always an array. RFC 9264 section 4.2.2 requires one, and RFC
// 9727 contradicts itself here: its section 5.1 example writes a bare string
// where Appendix A.4 writes the array. A.4 is the conforming half, and section
// 5.1 is the example a reader lands on first.
func writeAPICatalogLink(out *strings.Builder, relation, origin, path, mediaType string) {
	if path = strings.TrimSpace(path); path == "" || !strings.HasPrefix(path, "/") {
		return
	}
	out.WriteString(`,"`)
	out.WriteString(relation)
	out.WriteString(`":[{"href":`)
	writeJSONString(out, origin+path)
	if mediaType != "" {
		out.WriteString(`,"type":`)
		writeJSONString(out, mediaType)
	}
	out.WriteString(`}]`)
}

// writeJSONString writes one JSON string literal, escaping what RFC 8259
// requires and leaving valid UTF-8 alone.
func writeJSONString(out *strings.Builder, value string) {
	out.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '"':
			out.WriteString(`\"`)
		case r == '\\':
			out.WriteString(`\\`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r < 0x20:
			fmt.Fprintf(out, `\u%04x`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
}
