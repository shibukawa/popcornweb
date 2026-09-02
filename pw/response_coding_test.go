package pw

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	kgzip "github.com/klauspost/compress/gzip"
	kzstd "github.com/klauspost/compress/zstd"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/htmlbind"
)

func compressedRequest(t *testing.T, acceptEncoding string, codings ...string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if acceptEncoding != "" {
		request.Header.Set("Accept-Encoding", acceptEncoding)
	}
	config := MiddlewareConfig{Compression: true, CompressionCodings: codings}
	return request.WithContext(pwruntime.WithResources(request.Context(), pwruntime.Resources{
		Configs: map[reflect.Type]any{reflect.TypeFor[MiddlewareConfig](): config},
	}))
}

func decodeZstd(t *testing.T, encoded []byte) string {
	t.Helper()
	decoder, err := kzstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	body, err := decoder.DecodeAll(encoded, nil)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func decodeGzip(t *testing.T, encoded []byte) string {
	t.Helper()
	reader, err := kgzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func staticLeaf(markup string) htmlbind.Fragment {
	builder := htmlbind.Builder[struct{}]{}
	return (&htmlbind.Plan[struct{}]{Ops: []htmlbind.Op[struct{}]{
		builder.Static(markup),
	}}).Bind(struct{}{})
}

// TestWriteHTMLNegotiatesTheConfiguredOrder is the point of the whole change: a
// client that names only gzip is no longer answered with identity bytes, and a
// client that names both still receives the coding the order leads with.
func TestWriteHTMLNegotiatesTheConfiguredOrder(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		acceptEncoding string
		codings        []string
		want           string
	}{
		{name: "gzip only", acceptEncoding: "gzip, deflate", want: "gzip"},
		{name: "safari over plain http", acceptEncoding: "gzip, deflate", want: "gzip"},
		{name: "both accepted takes the leader", acceptEncoding: "gzip, zstd", want: "zstd"},
		{name: "configured order wins over header order", acceptEncoding: "gzip, zstd", codings: []string{"gzip", "zstd"}, want: "gzip"},
		{name: "zstd refused falls through", acceptEncoding: "zstd;q=0, gzip", want: "gzip"},
		{name: "wildcard takes the leader", acceptEncoding: "*", want: "zstd"},
		{name: "coding removed from the list is not offered", acceptEncoding: "gzip, zstd", codings: []string{"gzip"}, want: "gzip"},
		{name: "nothing acceptable stays identity", acceptEncoding: "br", want: ""},
		{name: "everything refused stays identity", acceptEncoding: "*;q=0", want: ""},
		{name: "no header stays identity", acceptEncoding: "", want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := compressedRequest(t, testCase.acceptEncoding, testCase.codings...)
			recorder := httptest.NewRecorder()

			WriteHTML(recorder, request, staticLeaf("<main>compressed</main>"))

			if got := recorder.Header().Get("Content-Encoding"); got != testCase.want {
				t.Fatalf("Content-Encoding = %q, want %q", got, testCase.want)
			}
			// Vary is added whether or not the body ended up encoded, so a cache
			// holding the identity form cannot answer a request for a coding.
			if got := recorder.Header().Get("Vary"); got == "" {
				t.Error("Vary is missing")
			}
			var body string
			switch testCase.want {
			case "zstd":
				body = decodeZstd(t, recorder.Body.Bytes())
			case "gzip":
				body = decodeGzip(t, recorder.Body.Bytes())
			default:
				body = recorder.Body.String()
			}
			if body != "<main>compressed</main>" {
				t.Fatalf("decoded body = %q", body)
			}
		})
	}
}

// TestWriteHTMLDropsContentLengthWhenEncoding covers the header the encoder
// invalidates: a length is only known after Close, and the headers are long
// gone by then.
func TestWriteHTMLDropsContentLengthWhenEncoding(t *testing.T) {
	request := compressedRequest(t, "gzip")
	recorder := httptest.NewRecorder()

	WriteHTML(recorder, request, staticLeaf("<main>compressed</main>"))

	if got := recorder.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length = %q, want none", got)
	}
}

// TestWriteHTMLLeavesAnAlreadyEncodedResponseAlone guards the rule that nothing
// is encoded twice.
func TestWriteHTMLLeavesAnAlreadyEncodedResponseAlone(t *testing.T) {
	request := compressedRequest(t, "gzip, zstd")
	recorder := httptest.NewRecorder()
	recorder.Header().Set("Content-Encoding", "br")

	WriteHTML(recorder, request, staticLeaf("<main>compressed</main>"))

	if got := recorder.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, want the one already set", got)
	}
	if recorder.Body.String() != "<main>compressed</main>" {
		t.Fatalf("body = %q, want the bytes as written", recorder.Body.String())
	}
}

func TestNegotiateResponseCodingReadsQualityAndWildcards(t *testing.T) {
	order := availableResponseCodings
	for _, testCase := range []struct {
		name   string
		header []string
		want   string
	}{
		{name: "explicit beats wildcard", header: []string{"*;q=0.1, gzip;q=0.9"}, want: "zstd"},
		{name: "wildcard zero excludes the unnamed", header: []string{"*;q=0, gzip"}, want: "gzip"},
		{name: "repeated header lines are one list", header: []string{"deflate", "gzip"}, want: "gzip"},
		{name: "malformed quality is unacceptable", header: []string{"zstd;q=bogus, gzip"}, want: "gzip"},
		{name: "out of range quality is unacceptable", header: []string{"zstd;q=7, gzip"}, want: "gzip"},
		{name: "case is ignored", header: []string{"GZIP"}, want: "gzip"},
		{name: "whitespace is ignored", header: []string{"  gzip ; q=0.5 "}, want: "gzip"},
		{name: "empty entries are skipped", header: []string{",,gzip,,"}, want: "gzip"},
		{name: "nothing offered", header: []string{"br, deflate"}, want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			coding, ok := negotiateResponseCoding(testCase.header, order)
			got := ""
			if ok {
				got = coding.token
			}
			if got != testCase.want {
				t.Fatalf("coding = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestOrderedResponseCodingsResolvesConfiguration(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		names []string
		want  []string
	}{
		{name: "empty takes the framework order", names: nil, want: []string{"zstd", "gzip"}},
		{name: "one comma-joined entry splits", names: []string{"gzip,zstd"}, want: []string{"gzip", "zstd"}},
		{name: "separate entries are kept in order", names: []string{"gzip", "zstd"}, want: []string{"gzip", "zstd"}},
		{name: "duplicates collapse to the first", names: []string{"gzip", "gzip", "zstd"}, want: []string{"gzip", "zstd"}},
		{name: "an omitted coding is dropped", names: []string{"gzip"}, want: []string{"gzip"}},
		{name: "blank entries are ignored", names: []string{"", " ", "zstd"}, want: []string{"zstd"}},
		{name: "only unknown names falls back", names: []string{"snappy"}, want: []string{"zstd", "gzip"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var scratch [maxResponseCodings]responseCoding
			resolved := orderedResponseCodings(testCase.names, &scratch)
			got := make([]string, 0, len(resolved))
			for _, coding := range resolved {
				got = append(got, coding.token)
			}
			if len(got) != len(testCase.want) {
				t.Fatalf("codings = %v, want %v", got, testCase.want)
			}
			for i := range got {
				if got[i] != testCase.want[i] {
					t.Fatalf("codings = %v, want %v", got, testCase.want)
				}
			}
		})
	}
}

func TestValidateCompressionCodingsRefusesAnUnknownName(t *testing.T) {
	if err := validateCompressionCodings(MiddlewareConfig{CompressionCodings: []string{"zstd", "gzip"}}); err != nil {
		t.Fatalf("the shipped default was refused: %v", err)
	}
	// A build tag removes an encoder, not the vocabulary, so a name this
	// framework knows is accepted whatever this binary can produce.
	if err := validateCompressionCodings(MiddlewareConfig{CompressionCodings: []string{"gzip"}}); err != nil {
		t.Fatalf("a known coding was refused: %v", err)
	}
	err := validateCompressionCodings(MiddlewareConfig{CompressionCodings: []string{"zstd", "brotli"}})
	if err == nil {
		t.Fatal("an unknown coding was accepted")
	}
	// brotli is the likely typo, since it is a real coding this project serves
	// statically, so the message has to name what the dynamic set actually is.
	if !bytes.Contains([]byte(err.Error()), []byte("brotli")) {
		t.Fatalf("error does not name the offending coding: %v", err)
	}
}
