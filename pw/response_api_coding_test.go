package pw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	tinybind "github.com/shibukawa/tinybind-go/httpbind"
)

type codingProbe struct {
	Message string
}

// codingProbeBody is long enough that encoding it is observably smaller. A
// document of a few dozen bytes comes out larger under every coding, because
// the frame header alone outweighs what there is to save, which is why the
// framework compresses unconditionally rather than above some threshold: the
// bodies where it would lose are the ones where losing costs nothing.
var codingProbeBody = func() string {
	body := `{"items":[`
	for i := 0; i < 40; i++ {
		if i > 0 {
			body += ","
		}
		body += `{"name":"widget","state":"ready","note":"nothing to report"}`
	}
	return body + `]}`
}()

func init() {
	tinybind.RegisterWrite(func(w http.ResponseWriter, _ *http.Request, _ codingProbe) error {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(codingProbeBody))
		return err
	})
}

// TestWriteAPIEncodesJSON covers the scope this change added: a JSON document
// is negotiated exactly as HTML is, rather than being the one dynamic response
// that always went out uncompressed.
func TestWriteAPIEncodesJSON(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		acceptEncoding string
		want           string
	}{
		{name: "gzip", acceptEncoding: "gzip, deflate", want: "gzip"},
		{name: "zstd leads", acceptEncoding: "gzip, zstd", want: "zstd"},
		{name: "identity", acceptEncoding: "br", want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := compressedRequest(t, testCase.acceptEncoding)
			recorder := httptest.NewRecorder()

			WriteAPI(recorder, request, codingProbe{Message: "hello"})

			if got := recorder.Header().Get("Content-Encoding"); got != testCase.want {
				t.Fatalf("Content-Encoding = %q, want %q", got, testCase.want)
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q", got)
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
			if body != codingProbeBody {
				t.Fatalf("decoded body = %q", body)
			}
			if testCase.want != "" && recorder.Body.Len() >= len(codingProbeBody) {
				t.Errorf("encoded body is not smaller: %d >= %d", recorder.Body.Len(), len(codingProbeBody))
			}
		})
	}
}

// TestWriteAPILeavesCompressionOffWhenDisabled keeps the default honest: the
// switch is off unless a deployment turns it on, because something in front of
// the application is usually already compressing.
func TestWriteAPILeavesCompressionOffWhenDisabled(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Encoding", "gzip, zstd")
	recorder := httptest.NewRecorder()

	WriteAPI(recorder, request, codingProbe{Message: "hello"})

	if got := recorder.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none", got)
	}
	if got := recorder.Header().Get("Vary"); got != "" {
		t.Fatalf("Vary = %q, want none when nothing negotiates", got)
	}
	if recorder.Body.String() != codingProbeBody {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}
