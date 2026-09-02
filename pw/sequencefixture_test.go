package pw

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shibukawa/tinybind-go/htmlbind"
	"github.com/shibukawa/tinybind-go/htmlbind/delta"
)

// updateSequenceFixtures rewrites the committed fixtures instead of comparing
// against them, for the one case where an upstream sequence encoding changed on
// purpose:
//
//	go test ./pw -run SequenceFixtures -update-sequences
var updateSequenceFixtures = flag.Bool("update-sequences", false, "rewrite the committed sequence fixtures")

// sequenceFixturePath holds trees and values the module produced, so the browser
// walk is checked against the server's own reassembly rather than against a
// reading of the specification.
//
// A wrong walk is silent: consuming the wrong number of values at one node puts
// every later value in the wrong place and still produces markup. Nothing but a
// round trip against the reference catches that.
const sequenceFixturePath = "testdata/sequence_fixtures.json"

type sequenceFixture struct {
	Name    string          `json:"name"`
	Address string          `json:"address"`
	Nodes   json.RawMessage `json:"nodes"`
	Values  []string        `json:"values"`
	Want    string          `json:"want"`
}

// The shapes worth covering are the ones whose value stream is not one entry per
// hole: a conditional spends one on the branch, a loop one on the count, and a
// component call one on whether it opened a boundary.
type badgeParams struct{ Label string }

type panelParams struct {
	Title string
	Rows  []string
	Note  string
}

type rowScope struct {
	Outer panelParams
	Item  string
	Index int
}

var (
	badgeOps = htmlbind.Builder[badgeParams]{}
	// A reloadable component, so a call to it opens a boundary and its frame in
	// the parent's sequence is a placeholder rather than the badge's markup.
	badgePlan = &htmlbind.Plan[badgeParams]{
		Boundary: &htmlbind.Boundary[badgeParams]{
			ComponentID: "pw.test.Badge@v1",
			Attr:        "data-" + UpdateAttributePrefix + "-id",
			Instance:    func(p badgeParams) string { return "badge-" + p.Label },
			Input:       func(p badgeParams) string { return delta.CanonString(p.Label) },
		},
		Ops: []htmlbind.Op[badgeParams]{
			badgeOps.Static("<b"), badgeOps.BoundaryAttr(), badgeOps.Static(">"),
			badgeOps.Text(func(p badgeParams) string { return p.Label }),
			badgeOps.Static("</b>"),
		},
	}

	panelOps    = htmlbind.Builder[panelParams]{}
	rowScopeOps = htmlbind.Builder[rowScope]{}
	panelPlan   = &htmlbind.Plan[panelParams]{
		Boundary: &htmlbind.Boundary[panelParams]{
			ComponentID: "pw.test.Panel@v1",
			Attr:        "data-" + UpdateAttributePrefix + "-id",
			Instance:    func(panelParams) string { return "panel" },
			Input:       func(p panelParams) string { return delta.CanonString(p.Title) },
		},
		Ops: []htmlbind.Op[panelParams]{
			panelOps.Static("<section"), panelOps.BoundaryAttr(),
			panelOps.Attr("title", func(p panelParams) (string, bool) {
				// An optional attribute: absent, its name and quotes go with it,
				// which is why presence is structure rather than a value.
				return htmlbind.Escape(p.Note), p.Note != ""
			}),
			panelOps.Static("><h3>"),
			panelOps.Text(func(p panelParams) string { return p.Title }),
			panelOps.Static("</h3>"),
			panelOps.If(func(p panelParams) bool { return len(p.Rows) > 0 },
				[]htmlbind.Op[panelParams]{
					panelOps.Static("<ul>"),
					panelOps.For(
						func(p panelParams) []string { return p.Rows },
						func(p panelParams, item string, index int) rowScope {
							return rowScope{Outer: p, Item: item, Index: index}
						},
						[]htmlbind.Op[rowScope]{
							rowScopeOps.Static("<li>"),
							rowScopeOps.Text(func(s rowScope) string { return s.Item }),
							rowScopeOps.Static(" "),
							rowScopeOps.Component(func(s rowScope) htmlbind.Fragment {
								return badgePlan.Bind(badgeParams{Label: strconv.Itoa(s.Index)})
							}),
							rowScopeOps.Static("</li>"),
						}),
					panelOps.Static("</ul>"),
				},
				[]htmlbind.Op[panelParams]{panelOps.Static("<p>nothing yet</p>")}),
			panelOps.Static("</section>"),
		},
	}
)

// buildSequenceFixtures renders each shape and pairs what the module split with
// what the module reassembles from it.
func buildSequenceFixtures(t *testing.T) []sequenceFixture {
	t.Helper()
	cases := []struct {
		name   string
		params panelParams
	}{
		{"empty", panelParams{Title: "Inbox"}},
		{"one-row", panelParams{Title: "Inbox", Rows: []string{"first"}}},
		{"rows-and-attribute", panelParams{Title: "In<box>", Rows: []string{"a", "b", "c"}, Note: `say "hi"`}},
	}
	fixtures := make([]sequenceFixture, 0, len(cases))
	for _, one := range cases {
		// An empty known manifest makes every boundary a replacement, which is
		// what carries a sequence and its values.
		diff, err := delta.RenderDelta([]byte("fixture-key"), delta.Manifest{}, nil,
			panelPlan.Bind(one.params),
			htmlbind.WithBoundaryPrefix(UpdateAttributePrefix), htmlbind.WithValidatorTag("fixture"))
		if err != nil {
			t.Fatalf("%s: %v", one.name, err)
		}
		var operation *delta.Operation
		for index := range diff.Operations {
			if diff.Operations[index].InstanceID == "panel" {
				operation = &diff.Operations[index]
			}
		}
		if operation == nil {
			t.Fatalf("%s: the panel produced no operation", one.name)
		}
		if operation.Sequence == "" {
			t.Fatalf("%s: the operation carries no sequence address", one.name)
		}
		sequence, known := htmlbind.LookupSequence(operation.Sequence)
		if !known {
			t.Fatalf("%s: the address the render published is not registered", one.name)
		}
		want, err := sequence.Reassemble(operation.Values)
		if err != nil {
			t.Fatalf("%s: the module could not reassemble its own split: %v", one.name, err)
		}
		if want != operation.HTML {
			t.Fatalf("%s: the split does not reproduce the render:\n%q\n%q", one.name, want, operation.HTML)
		}
		var encoded struct {
			Nodes json.RawMessage `json:"nodes"`
		}
		if err := json.Unmarshal(sequence.AppendJSON(nil), &encoded); err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, sequenceFixture{
			Name: one.name, Address: operation.Sequence,
			Nodes: encoded.Nodes, Values: operation.Values, Want: want,
		})
	}
	return fixtures
}

// TestSequenceFixturesAreCurrent keeps the committed fixtures in step with what
// the module emits, so a change to the sequence encoding shows up as a diff a
// reviewer reads rather than as a browser walk that quietly stops matching.
func TestSequenceFixturesAreCurrent(t *testing.T) {
	fixtures := buildSequenceFixtures(t)
	encoded, err := json.MarshalIndent(fixtures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if *updateSequenceFixtures {
		if err := os.MkdirAll(filepath.Dir(sequenceFixturePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sequenceFixturePath, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("wrote", sequenceFixturePath)
		return
	}
	committed, err := os.ReadFile(sequenceFixturePath)
	if err != nil {
		t.Fatalf("%v; run go test ./pw -run SequenceFixtures -update-sequences", err)
	}
	if string(committed) != string(encoded) {
		t.Errorf("the committed sequence fixtures are stale; run go test ./pw -run SequenceFixtures -update-sequences")
	}
}

// The shapes the fixtures are meant to cover, asserted rather than assumed: a
// fixture set that lost its conditional or its loop would still pass the round
// trip above and stop testing what it exists to test.
func TestSequenceFixturesCoverTheInterestingShapes(t *testing.T) {
	fixtures := buildSequenceFixtures(t)
	joined := ""
	for _, fixture := range fixtures {
		joined += string(fixture.Nodes)
	}
	for kind, name := range map[string]string{
		`"k":2`: "a conditional",
		`"k":3`: "a loop",
		`"k":4`: "a component call",
	} {
		if !strings.Contains(joined, kind) {
			t.Errorf("no fixture carries %s", name)
		}
	}
	for _, fixture := range fixtures {
		if len(fixture.Values) == 0 {
			t.Errorf("%s: no values, so the walk it drives consumes nothing", fixture.Name)
		}
	}
}
