// Package pwrequest is what pw request works from: the operation catalog
// projected out of the application's OpenAPI document, the routing of named
// parameters against one operation, and the one request that results.
//
// The catalog is data:operation-catalog and the routing is
// rule:request-parameter-routing. Nothing here reads a project or a
// configuration file; pw request feeds it the document and the pairs.
package pwrequest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/shibukawa/popcornweb/internal/pathpattern"
)

// Location is where a handler reads one parameter from.
type Location string

const (
	InPath   Location = "path"
	InQuery  Location = "query"
	InHeader Location = "header"
	InCookie Location = "cookie"
	InBody   Location = "body"
)

// Parameter is one named input of an operation.
type Parameter struct {
	Name     string   `json:"name"`
	In       Location `json:"in"`
	Type     string   `json:"type,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// Operation is one invocable entry of the catalog.
type Operation struct {
	ID         string      `json:"operationId"`
	Method     string      `json:"method"`
	Path       string      `json:"path"`
	Summary    string      `json:"summary,omitempty"`
	Parameters []Parameter `json:"parameters,omitempty"`
	// ContentType is the first media type the request body declares, which is
	// the one routing assembles pairs into. Empty when the operation takes no
	// body.
	ContentType string `json:"contentType,omitempty"`
	// ContentTypes is every media type the body declares, so a -H Content-Type
	// naming one of them is honoured with the matching encoding.
	ContentTypes []string `json:"contentTypes,omitempty"`
	// Body lists the top-level fields of an object body, with their schema
	// types, which drive coercion.
	Body []Parameter `json:"body,omitempty"`
	// Responses is the status codes the operation declares, for the report.
	Responses []string `json:"responses,omitempty"`
	// Protected is whether policy:authenticated-path-protection guards the
	// path in the environment the command read. It is a projection of
	// configuration, not of the document.
	Protected bool `json:"protected,omitempty"`

	segments []string
}

// Catalog is the projection of one OpenAPI document.
type Catalog struct {
	Operations []Operation `json:"operations"`
}

// openAPI is the subset of the document the projection reads. Everything else
// in the document is left alone: the catalog exists to route parameters, and a
// schema beyond the top level is not something a -d pair can name.
type openAPI struct {
	Paths map[string]map[string]json.RawMessage `json:"paths"`
}

type openAPIOperation struct {
	OperationID string             `json:"operationId"`
	Summary     string             `json:"summary"`
	Parameters  []openAPIParameter `json:"parameters"`
	RequestBody *struct {
		Content map[string]struct {
			Schema openAPISchema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]json.RawMessage `json:"responses"`
}

type openAPIParameter struct {
	Name     string        `json:"name"`
	In       string        `json:"in"`
	Required bool          `json:"required"`
	Schema   openAPISchema `json:"schema"`
}

type openAPISchema struct {
	Type       json.RawMessage          `json:"type"`
	Properties map[string]openAPISchema `json:"properties"`
	Required   []string                 `json:"required"`
	Items      *openAPISchema           `json:"items"`
}

// typeName reads a schema type, which OpenAPI 3.1 allows to be a string or a
// list; the first non-null entry of a list is what coercion wants.
func (s openAPISchema) typeName() string {
	if len(s.Type) == 0 {
		return ""
	}
	var single string
	if err := json.Unmarshal(s.Type, &single); err == nil {
		return single
	}
	var many []string
	if err := json.Unmarshal(s.Type, &many); err == nil {
		for _, name := range many {
			if name != "null" {
				return name
			}
		}
	}
	return ""
}

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// bodyMediaTypes is the order the projection prefers a declared media type
// in: JSON first, because it is what an API operation reads, then the two form
// encodings the generator emits beside it.
var bodyMediaTypes = []string{"application/json", "application/x-www-form-urlencoded", "multipart/form-data"}

// ParseOpenAPI projects a document into a catalog. Operations come out sorted
// by path and then method, so --list is stable across runs.
func ParseOpenAPI(document []byte) (Catalog, error) {
	var doc openAPI
	if err := json.Unmarshal(document, &doc); err != nil {
		return Catalog{}, fmt.Errorf("openapi document: %w", err)
	}
	if doc.Paths == nil {
		return Catalog{}, fmt.Errorf("openapi document: no paths object")
	}
	catalog := Catalog{}
	for path, item := range doc.Paths {
		for _, method := range httpMethods {
			raw, ok := item[method]
			if !ok {
				continue
			}
			var op openAPIOperation
			if err := json.Unmarshal(raw, &op); err != nil {
				return Catalog{}, fmt.Errorf("openapi document: %s %s: %w", strings.ToUpper(method), path, err)
			}
			catalog.Operations = append(catalog.Operations, projectOperation(strings.ToUpper(method), path, op))
		}
	}
	sort.Slice(catalog.Operations, func(i, j int) bool {
		a, b := catalog.Operations[i], catalog.Operations[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
	return catalog, nil
}

func projectOperation(method, path string, op openAPIOperation) Operation {
	out := Operation{
		ID:       op.OperationID,
		Method:   method,
		Path:     path,
		Summary:  op.Summary,
		segments: strings.Split(strings.TrimPrefix(path, "/"), "/"),
	}
	if out.ID == "" {
		out.ID = method + " " + path
	}
	for _, parameter := range op.Parameters {
		location := Location(parameter.In)
		switch location {
		case InPath, InQuery, InHeader, InCookie:
		default:
			continue
		}
		out.Parameters = append(out.Parameters, Parameter{
			Name:     parameter.Name,
			In:       location,
			Type:     parameter.Schema.typeName(),
			Required: parameter.Required,
		})
	}
	// A template segment the document did not list as a parameter is still a
	// segment the caller has to fill, so it joins the parameters as one.
	for _, segment := range out.segments {
		name, ok := templateSegment(segment)
		if !ok || out.parameter(name, InPath) != nil {
			continue
		}
		out.Parameters = append(out.Parameters, Parameter{Name: name, In: InPath, Type: "string", Required: true})
	}
	if op.RequestBody != nil {
		for _, mediaType := range bodyMediaTypes {
			if _, ok := op.RequestBody.Content[mediaType]; ok {
				out.ContentTypes = append(out.ContentTypes, mediaType)
			}
		}
		for mediaType := range op.RequestBody.Content {
			if !contains(out.ContentTypes, mediaType) {
				out.ContentTypes = append(out.ContentTypes, mediaType)
			}
		}
		if len(out.ContentTypes) > 0 {
			out.ContentType = out.ContentTypes[0]
			schema := op.RequestBody.Content[out.ContentType].Schema
			names := make([]string, 0, len(schema.Properties))
			for name := range schema.Properties {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				property := schema.Properties[name]
				out.Body = append(out.Body, Parameter{
					Name:     name,
					In:       InBody,
					Type:     property.typeName(),
					Required: contains(schema.Required, name),
				})
			}
		}
	}
	for status := range op.Responses {
		out.Responses = append(out.Responses, status)
	}
	sort.Strings(out.Responses)
	return out
}

// templateSegment reports the parameter name of a {name} segment, tolerating
// the net/http {name...} wildcard and the {$} end marker, which name nothing.
func templateSegment(segment string) (string, bool) {
	if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
	name = strings.TrimSuffix(name, "...")
	if name == "" || name == "$" {
		return "", false
	}
	return name, true
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// parameter finds one by name and location, or nil.
func (o *Operation) parameter(name string, in Location) *Parameter {
	for index := range o.Parameters {
		if o.Parameters[index].Name == name && o.Parameters[index].In == in {
			return &o.Parameters[index]
		}
	}
	return nil
}

// bodyField finds one body field by name, or nil.
func (o *Operation) bodyField(name string) *Parameter {
	for index := range o.Body {
		if o.Body[index].Name == name {
			return &o.Body[index]
		}
	}
	return nil
}

// Template reports whether the operation's path carries a segment the caller
// fills in.
func (o *Operation) Template() bool {
	for _, segment := range o.segments {
		if _, ok := templateSegment(segment); ok {
			return true
		}
	}
	return false
}

// MarkProtected sets Protected on every operation whose path the include and
// exclude patterns of policy:authenticated-path-protection guard.
//
// A template path is matched with its parameters replaced by a placeholder
// segment, which is what a real request would present once filled: the guard
// patterns match segments, and a single-segment wildcard matches any one.
func (c *Catalog) MarkProtected(include, exclude []string) error {
	includes, err := pathpattern.Compile(include)
	if err != nil {
		return err
	}
	excludes, err := pathpattern.Compile(exclude)
	if err != nil {
		return err
	}
	for index := range c.Operations {
		op := &c.Operations[index]
		op.Protected = pathpattern.Protected(includes, excludes, op.matchablePath())
	}
	return nil
}

func (o *Operation) matchablePath() string {
	parts := make([]string, len(o.segments))
	for index, segment := range o.segments {
		if _, ok := templateSegment(segment); ok {
			parts[index] = "_"
			continue
		}
		parts[index] = segment
	}
	return "/" + strings.Join(parts, "/")
}

// ByID selects the operation whose operationId is id, or nil.
func (c *Catalog) ByID(id string) *Operation {
	for index := range c.Operations {
		if c.Operations[index].ID == id {
			return &c.Operations[index]
		}
	}
	return nil
}

// Match selects the operations whose template matches a literal or template
// path, the way net/http matches segments. An empty method matches every
// method, which is how an ambiguity between methods is reported rather than
// resolved by a guess. The returned bindings are the path parameters a literal
// path filled.
func (c *Catalog) Match(method, path string) []Matched {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	var out []Matched
	for index := range c.Operations {
		op := &c.Operations[index]
		if method != "" && op.Method != method {
			continue
		}
		bindings, ok := op.matchSegments(segments)
		if !ok {
			continue
		}
		out = append(out, Matched{Operation: op, Bindings: bindings})
	}
	return out
}

// Matched is one operation a path selected, with the segments it filled.
type Matched struct {
	Operation *Operation
	Bindings  map[string]string
}

func (o *Operation) matchSegments(segments []string) (map[string]string, bool) {
	bindings := map[string]string{}
	for index, want := range o.segments {
		name, isTemplate := templateSegment(want)
		if isTemplate && strings.HasSuffix(strings.TrimSuffix(want, "}"), "...") {
			// A rest wildcard takes everything that remains.
			if index >= len(segments) {
				return nil, false
			}
			rest := strings.Join(segments[index:], "/")
			if rest != want {
				bindings[name] = rest
			}
			return bindings, true
		}
		if index >= len(segments) {
			return nil, false
		}
		got := segments[index]
		switch {
		case want == "{$}":
			if got != "" || index != len(segments)-1 {
				return nil, false
			}
		case isTemplate:
			if got == "" {
				return nil, false
			}
			if got != want {
				bindings[name] = got
			}
		case got != want:
			return nil, false
		}
	}
	if len(segments) != len(o.segments) {
		return nil, false
	}
	return bindings, true
}
