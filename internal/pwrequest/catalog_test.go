package pwrequest

import (
	"strings"
	"testing"
)

// fixtureDocument is the shape tinybind generates: one operation with a query
// parameter and three body encodings, plus a template path and a framework
// endpoint with no parameters at all.
const fixtureDocument = `{
  "openapi": "3.1.0",
  "paths": {
    "/greet": {
      "get": {
        "operationId": "Greet",
        "summary": "Greet is the authored handler.",
        "parameters": [{"in": "query", "name": "name", "required": true, "schema": {"type": "string"}}],
        "requestBody": {"content": {
          "application/json": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}},
          "application/x-www-form-urlencoded": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}
        }},
        "responses": {"200": {"description": "OK"}, "400": {"description": "Validation"}}
      }
    },
    "/api/users": {
      "post": {
        "operationId": "CreateUser",
        "requestBody": {"content": {"application/json": {"schema": {"type": "object",
          "properties": {"name": {"type": "string"}, "age": {"type": "integer"}, "admin": {"type": "boolean"}, "tags": {"type": "array", "items": {"type": "string"}}},
          "required": ["name"]}},
          "application/x-www-form-urlencoded": {"schema": {"type": "object", "properties": {"name": {"type": "string"}}}}}},
        "responses": {"201": {"description": "Created"}}
      }
    },
    "/api/users/{id}": {
      "get": {
        "operationId": "ShowUser",
        "parameters": [
          {"in": "path", "name": "id", "required": true, "schema": {"type": "integer"}},
          {"in": "query", "name": "verbose", "schema": {"type": "boolean"}},
          {"in": "header", "name": "X-Trace", "schema": {"type": "string"}}
        ],
        "responses": {"200": {"description": "OK"}}
      },
      "delete": {
        "operationId": "DeleteUser",
        "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "integer"}}],
        "responses": {"204": {"description": "Gone"}}
      }
    },
    "/account/{section}": {
      "get": {"responses": {"200": {"description": "OK"}}}
    },
    "/healthz": {
      "get": {"responses": {"200": {"description": "OK"}}}
    }
  }
}`

func fixtureCatalog(t *testing.T) Catalog {
	t.Helper()
	catalog, err := ParseOpenAPI([]byte(fixtureDocument))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return catalog
}

func TestParseProjectsEveryOperationInPathOrder(t *testing.T) {
	catalog := fixtureCatalog(t)
	var ids []string
	for _, operation := range catalog.Operations {
		ids = append(ids, operation.ID)
	}
	want := "GET /account/{section}, CreateUser, DeleteUser, ShowUser, Greet, GET /healthz"
	if got := strings.Join(ids, ", "); got != want {
		t.Fatalf("ids = %s, want %s", got, want)
	}
}

func TestParseReadsParameterLocationsAndBodyFields(t *testing.T) {
	catalog := fixtureCatalog(t)
	greet := catalog.ByID("Greet")
	if greet == nil {
		t.Fatal("Greet not in the catalog")
	}
	if greet.ContentType != "application/json" || len(greet.ContentTypes) != 2 {
		t.Errorf("content types = %q %v, want JSON first of two", greet.ContentType, greet.ContentTypes)
	}
	if len(greet.Parameters) != 1 || greet.Parameters[0].In != InQuery || !greet.Parameters[0].Required {
		t.Errorf("parameters = %+v, want one required query parameter", greet.Parameters)
	}
	if len(greet.Body) != 1 || greet.Body[0].Name != "name" || !greet.Body[0].Required {
		t.Errorf("body = %+v, want the required name field", greet.Body)
	}
	if got := strings.Join(greet.Responses, ","); got != "200,400" {
		t.Errorf("responses = %s", got)
	}
	// A template segment the document did not declare still becomes a path
	// parameter, because the caller has to fill it either way.
	section := catalog.ByID("GET /account/{section}")
	if section == nil || len(section.Parameters) != 1 || section.Parameters[0].In != InPath || section.Parameters[0].Name != "section" {
		t.Errorf("undeclared template segment = %+v, want a path parameter named section", section)
	}
}

func TestMatchFillsTemplateSegmentsFromALiteralPath(t *testing.T) {
	catalog := fixtureCatalog(t)
	matches := catalog.Match("GET", "/api/users/7")
	if len(matches) != 1 || matches[0].Operation.ID != "ShowUser" || matches[0].Bindings["id"] != "7" {
		t.Fatalf("matches = %+v, want ShowUser with id=7", matches)
	}
	if got := catalog.Match("GET", "/api/users/7/extra"); len(got) != 0 {
		t.Errorf("a longer path matched %+v", got)
	}
	if got := catalog.Match("GET", "/api/users/{id}"); len(got) != 1 || len(got[0].Bindings) != 0 {
		t.Errorf("the template itself = %+v, want a match with nothing bound", got)
	}
	// An empty method reports every operation on the path, which is how an
	// ambiguity is surfaced rather than guessed at.
	if got := catalog.Match("", "/api/users/7"); len(got) != 2 {
		t.Errorf("methodless match = %d operations, want 2", len(got))
	}
}

func TestMarkProtectedProjectsTheGuardPatterns(t *testing.T) {
	catalog := fixtureCatalog(t)
	if err := catalog.MarkProtected([]string{"/account/**", "/api/**"}, []string{"/api/users"}); err != nil {
		t.Fatal(err)
	}
	protected := map[string]bool{}
	for _, operation := range catalog.Operations {
		protected[operation.ID] = operation.Protected
	}
	if !protected["GET /account/{section}"] || !protected["ShowUser"] {
		t.Errorf("template paths under an include were not marked: %v", protected)
	}
	if protected["CreateUser"] {
		t.Errorf("an excluded path was marked: %v", protected)
	}
	if protected["GET /healthz"] || protected["Greet"] {
		t.Errorf("a path outside every include was marked: %v", protected)
	}
}

func TestParseRefusesADocumentWithoutPaths(t *testing.T) {
	if _, err := ParseOpenAPI([]byte(`{"openapi": "3.1.0"}`)); err == nil {
		t.Fatal("a document with no paths parsed")
	}
	if _, err := ParseOpenAPI([]byte(`not json`)); err == nil {
		t.Fatal("garbage parsed")
	}
}
