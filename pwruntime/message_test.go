package pwruntime

import (
	"strings"
	"testing"
)

func TestRenderMessageInterpolatesByPosition(t *testing.T) {
	row := []Segment{{Lit: "Welcome, "}, {Arg: 1}, {Lit: "!"}}
	if got := RenderMessage(row, "Ada"); got != "Welcome, Ada!" {
		t.Errorf("RenderMessage = %q", got)
	}
}

// A row whose arguments run in a different order is the case the segment shape
// exists for; nothing reorders at render because the row already did.
func TestRenderMessageFollowsTheRowsOwnOrder(t *testing.T) {
	row := []Segment{{Arg: 2}, {Lit: "へ"}, {Arg: 1}, {Lit: "から"}}
	if got := RenderMessage(row, "東京", "大阪"); got != "大阪へ東京から" {
		t.Errorf("RenderMessage = %q", got)
	}
}

// A message value is unescaped on purpose: the template escapes it once, with
// its arguments, for the position it lands in.
func TestRenderMessageDoesNotEscape(t *testing.T) {
	row := []Segment{{Lit: "Tom & Jerry, "}, {Arg: 1}}
	if got := RenderMessage(row, "<b>"); got != "Tom & Jerry, <b>" {
		t.Errorf("RenderMessage = %q; escaping here would double-escape at the template", got)
	}
}

// A row referring to an argument the caller did not pass renders nothing there
// rather than panicking. Generation checks arity, so reaching this means the
// table and the signature disagree, and a blank is a better production outcome
// than a crash inside a render.
func TestRenderMessageToleratesAMissingArgument(t *testing.T) {
	row := []Segment{{Lit: "a"}, {Arg: 3}, {Lit: "b"}}
	if got := RenderMessage(row, "x"); got != "ab" {
		t.Errorf("RenderMessage = %q", got)
	}
}

func TestRenderRichMessageMergesRunsWithinAHole(t *testing.T) {
	row := []RichSegment{
		{Lit: "Please "},
		{Hole: "a", Lit: "get "},
		{Hole: "a", Arg: 1},
		{Lit: " now"},
	}
	segments := RenderRichMessage(row, "started")
	if len(segments) != 3 {
		t.Fatalf("segments = %d, want 3 after merging the two runs inside the hole: %+v", len(segments), segments)
	}
	if segments[1].Hole != "a" || segments[1].Text != "get started" {
		t.Errorf("hole segment = %+v, want the merged text", segments[1])
	}
	var rendered strings.Builder
	for _, segment := range segments {
		rendered.WriteString(segment.Text)
	}
	if got := rendered.String(); got != "Please get started now" {
		t.Errorf("flattened = %q", got)
	}
}

func TestRenderRichMessageKeepsHoleBoundaries(t *testing.T) {
	row := []RichSegment{{Hole: "a", Lit: "x"}, {Lit: "y"}, {Hole: "a", Lit: "z"}}
	segments := RenderRichMessage(row)
	if len(segments) != 3 {
		t.Fatalf("segments = %+v; adjacent merging must not join two separate holes", segments)
	}
}

// Grouping is per locale, and plural selection reads the value rather than this
// string, so an untabulated locale still selects the right form.
func TestFormatIntGroupsPerLocale(t *testing.T) {
	withLocales(t, []string{"en", "de", "fr", "ja", "xx"}, "en")

	cases := []struct{ tag, want string }{
		{"en", "1,234,567"},
		{"ja", "1,234,567"},
		{"de", "1.234.567"},
		{"fr", "1 234 567"},
		// Untabulated: the comma-and-period convention rather than an error. A
		// number grouped the wrong way is legible; a page that failed is not.
		{"xx", "1,234,567"},
	}
	for _, tc := range cases {
		if got := FormatInt(MustParseLocale(tc.tag), 1234567); got != tc.want {
			t.Errorf("FormatInt(%s) = %q, want %q", tc.tag, got, tc.want)
		}
	}
	for _, tc := range []struct {
		value int
		want  string
	}{{0, "0"}, {999, "999"}, {1000, "1,000"}, {-12345, "-12,345"}} {
		if got := FormatInt(MustParseLocale("en"), tc.value); got != tc.want {
			t.Errorf("FormatInt(%d) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
