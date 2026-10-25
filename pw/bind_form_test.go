package pw

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tinybind "github.com/shibukawa/tinybind-go/httpbind"
)

type replayFormInput struct{ Owner string }

func TestParsePreservesFormValuesAcrossBinding(t *testing.T) {
	tinybind.RegisterBind[replayFormInput](func(r *http.Request) (replayFormInput, error) {
		form, _, err := tinybind.ReadFormBody(r, true, false)
		if err != nil {
			return replayFormInput{}, err
		}
		title, _ := tinybind.FormLookup(form, "title")
		if title == "" {
			return replayFormInput{}, tinybind.Validation(tinybind.Field("title", "required", "required"))
		}
		owner, _ := tinybind.FormLookup(form, "owner")
		return replayFormInput{Owner: owner}, nil
	})
	for _, test := range []struct {
		name, body    string
		preparse, bad bool
	}{
		{"valid", "title=Task&owner=ada%2Blovelace", false, false},
		{"invalid", "title=&owner=ada%2Blovelace", false, true},
		{"already-parsed", "title=Task&owner=ada%2Blovelace", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/tasks?owner=wrong", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
			if test.preparse {
				if err := request.ParseForm(); err != nil {
					t.Fatal(err)
				}
			}
			input, err := Parse[replayFormInput](request)
			if (err != nil) != test.bad {
				t.Fatalf("bind error = %v", err)
			}
			if !test.bad && input.Owner != "ada+lovelace" {
				t.Fatalf("input = %#v", input)
			}
			if value := FormValue(request, "owner"); value != "ada+lovelace" {
				t.Fatalf("form value = %q", value)
			}
			if value := FormValue(request, "owner"); value != "ada+lovelace" {
				t.Fatalf("repeated form value = %q", value)
			}
		})
	}
}

func TestFormReplayCaptureIsBounded(t *testing.T) {
	var body formBodyCapture
	data := strings.NewReader(strings.Repeat("x", 11<<20))
	if _, err := io.Copy(&body, data); err != nil {
		t.Fatal(err)
	}
	if body.Len() != (10<<20)+1 {
		t.Fatalf("replay held %d bytes", body.Len())
	}
}
