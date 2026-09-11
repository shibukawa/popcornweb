package pwrequest

import (
	"encoding/json"
	"strings"
	"testing"
)

func pairs(values ...string) []Pair {
	var out []Pair
	for _, value := range values {
		key, val, _ := strings.Cut(value, "=")
		out = append(out, Pair{Key: key, Value: val})
	}
	return out
}

func routedTo(plan Plan) map[string]Location {
	out := map[string]Location{}
	for _, routed := range plan.Routing {
		out[routed.Key] = routed.In
	}
	return out
}

func TestRouteAssemblesAJSONBodyCoercedByTheSchema(t *testing.T) {
	catalog := fixtureCatalog(t)
	plan, err := Route(catalog.ByID("CreateUser"), nil, Input{Pairs: pairs("name=Alice", "age=3", "admin=true", "tags=a", "tags=b")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Method != "POST" || plan.Path != "/api/users" || plan.ContentType != "application/json" {
		t.Errorf("plan = %s %s %s", plan.Method, plan.Path, plan.ContentType)
	}
	want := `{"name":"Alice","age":3,"admin":true,"tags":["a","b"]}`
	if string(plan.Body) != want {
		t.Errorf("body = %s, want %s", plan.Body, want)
	}
	for key, in := range routedTo(plan) {
		if in != InBody {
			t.Errorf("%s routed to %s, want body", key, in)
		}
	}
}

func TestRouteKeepsAValueTheSchemaCannotHoldAndSaysSo(t *testing.T) {
	catalog := fixtureCatalog(t)
	plan, err := Route(catalog.ByID("CreateUser"), nil, Input{Pairs: pairs("name=Alice", "age=three")})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(plan.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["age"] != "three" {
		t.Errorf("age = %v, want the string kept", body["age"])
	}
	var noted bool
	for _, routed := range plan.Routing {
		if routed.Key == "age" && strings.Contains(routed.Note, "not an integer") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("routing = %+v, want a note on age", plan.Routing)
	}
}

func TestRouteSendsEachPairWhereTheOperationReadsIt(t *testing.T) {
	catalog := fixtureCatalog(t)
	plan, err := Route(catalog.ByID("ShowUser"), nil, Input{Pairs: pairs("id=7", "verbose=true", "X-Trace=abc", "extra=1")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Path != "/api/users/7" {
		t.Errorf("path = %s", plan.Path)
	}
	if plan.Query.Get("verbose") != "true" || plan.Headers["X-Trace"] != "abc" {
		t.Errorf("query = %v, headers = %v", plan.Query, plan.Headers)
	}
	routed := routedTo(plan)
	if routed["id"] != InPath || routed["verbose"] != InQuery || routed["X-Trace"] != InHeader || routed["extra"] != InBody {
		t.Errorf("routing = %v", routed)
	}
	// A pair the operation does not name is reported, not dropped.
	var noted bool
	for _, entry := range plan.Routing {
		if entry.Key == "extra" && strings.Contains(entry.Note, "not a parameter") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("routing = %+v, want the unknown pair noted", plan.Routing)
	}
}

func TestRouteRefusesAnUnknownPairUnderStrict(t *testing.T) {
	catalog := fixtureCatalog(t)
	_, err := Route(catalog.ByID("ShowUser"), nil, Input{Pairs: pairs("id=7", "extra=1"), Strict: true})
	if err == nil || !strings.Contains(err.Error(), "extra names no parameter") {
		t.Fatalf("err = %v, want the unknown pair refused", err)
	}
}

func TestRouteRefusesAnUnfilledTemplateSegment(t *testing.T) {
	catalog := fixtureCatalog(t)
	_, err := Route(catalog.ByID("ShowUser"), nil, Input{Pairs: pairs("verbose=true")})
	if err == nil || !strings.Contains(err.Error(), "{id}") {
		t.Fatalf("err = %v, want the missing segment named", err)
	}
	// A literal path already filled it.
	plan, err := Route(catalog.ByID("ShowUser"), map[string]string{"id": "9"}, Input{})
	if err != nil || plan.Path != "/api/users/9" {
		t.Fatalf("plan = %+v, err = %v", plan, err)
	}
}

func TestRouteHonoursAnExplicitContentTypeTheOperationDeclares(t *testing.T) {
	catalog := fixtureCatalog(t)
	plan, err := Route(catalog.ByID("CreateUser"), nil, Input{
		Pairs:   pairs("name=Alice"),
		Headers: []Pair{{Key: "Content-Type", Value: "application/x-www-form-urlencoded"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ContentType != "application/x-www-form-urlencoded" || string(plan.Body) != "name=Alice" {
		t.Errorf("plan = %s %s", plan.ContentType, plan.Body)
	}
}

func TestRouteWithoutAnOperationMeansWhatCurlMeans(t *testing.T) {
	plan, err := Route(nil, nil, Input{Path: "/unknown", Pairs: pairs("a=b", "c=d")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Method != "POST" || plan.ContentType != "application/x-www-form-urlencoded" || string(plan.Body) != "a=b&c=d" {
		t.Errorf("plan = %s %s %s", plan.Method, plan.ContentType, plan.Body)
	}
	get, err := Route(nil, nil, Input{Path: "/unknown", Pairs: pairs("a=b"), Query: true})
	if err != nil {
		t.Fatal(err)
	}
	if get.Method != "GET" || get.Query.Get("a") != "b" || len(get.Body) != 0 {
		t.Errorf("-G plan = %s %v %s", get.Method, get.Query, get.Body)
	}
	asJSON, err := Route(nil, nil, Input{Path: "/unknown", Pairs: pairs("a=b"), JSON: true})
	if err != nil {
		t.Fatal(err)
	}
	if asJSON.ContentType != "application/json" || string(asJSON.Body) != `{"a":"b"}` {
		t.Errorf("--json plan = %s %s", asJSON.ContentType, asJSON.Body)
	}
}

func TestRouteRefusesFormsBesideData(t *testing.T) {
	_, err := Route(nil, nil, Input{Path: "/x", Pairs: pairs("a=b"), Forms: pairs("f=g")})
	if err == nil || !strings.Contains(err.Error(), "-F and -d") {
		t.Fatalf("err = %v", err)
	}
}

func TestRouteRefusesAMethodThatContradictsTheOperation(t *testing.T) {
	catalog := fixtureCatalog(t)
	_, err := Route(catalog.ByID("CreateUser"), nil, Input{Method: "PUT", Pairs: pairs("name=x")})
	if err == nil || !strings.Contains(err.Error(), "PUT does not match") {
		t.Fatalf("err = %v", err)
	}
}
