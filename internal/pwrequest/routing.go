package pwrequest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Pair is one -d key=value the caller gave.
type Pair struct {
	Key   string
	Value string
}

// Input is everything the caller said about one request, before routing.
type Input struct {
	// Method is the explicit -X value, or empty for the curl default: GET, or
	// POST once there is data.
	Method string
	// Path is the literal path the caller named, or empty when an operation
	// was selected by id and every segment comes from a pair.
	Path string
	// Pairs are the -d key=value arguments, in order.
	Pairs []Pair
	// Raw is a -d argument that was not a pair: a literal body, or @file.
	Raw    string
	RawSet bool
	// Forms are the -F fields, key=value or key=@file.
	Forms []Pair
	// Headers are the -H lines, already split.
	Headers []Pair
	// Cookies is the -b string, when it was not a file.
	Cookies string
	// Query moves pairs to the query string under -G, as curl does.
	Query bool
	// JSON sends pairs as a JSON object with no operation, the curl --json
	// spelling.
	JSON bool
	// Strict makes a pair that names nothing in the operation an error.
	Strict bool
}

// Routed says where one pair went.
type Routed struct {
	Key   string   `json:"key"`
	In    Location `json:"in"`
	Value string   `json:"value"`
	// Note explains a coercion that did not happen or a pair the operation did
	// not name.
	Note string `json:"note,omitempty"`
}

// Plan is the one request routing decided on.
type Plan struct {
	Method      string
	Path        string
	Query       url.Values
	Headers     map[string]string
	Cookies     []string
	Body        []byte
	ContentType string
	Routing     []Routed
	// Operation is what the pairs were routed against, or nil under curl
	// semantics.
	Operation *Operation
	// Reason says why no operation was used, when Operation is nil.
	Reason string
}

// Route applies rule:request-parameter-routing. With an operation, every pair
// goes where the operation reads it; without one, the pairs mean what they
// mean to curl. bindings are the path segments a literal path already filled.
func Route(op *Operation, bindings map[string]string, input Input) (Plan, error) {
	if op == nil {
		return routeCurl(input)
	}
	return routeOperation(op, bindings, input)
}

func routeOperation(op *Operation, bindings map[string]string, input Input) (Plan, error) {
	plan := Plan{Method: op.Method, Query: url.Values{}, Headers: map[string]string{}, Operation: op}
	if input.Method != "" && input.Method != op.Method {
		return plan, fmt.Errorf("%s does not match the matched operation %s; name the operation by id or drop -X", input.Method, op.ID)
	}
	if len(input.Forms) > 0 && (len(input.Pairs) > 0 || input.RawSet) {
		return plan, fmt.Errorf("-F and -d cannot be combined, as in curl")
	}
	filled := map[string]string{}
	for name, value := range bindings {
		filled[name] = value
		plan.Routing = append(plan.Routing, Routed{Key: name, In: InPath, Value: value})
	}
	var body []Pair
	for _, pair := range input.Pairs {
		switch {
		case op.parameter(pair.Key, InPath) != nil:
			filled[pair.Key] = pair.Value
			plan.Routing = append(plan.Routing, Routed{Key: pair.Key, In: InPath, Value: pair.Value})
		case op.parameter(pair.Key, InQuery) != nil:
			plan.Query.Add(pair.Key, pair.Value)
			plan.Routing = append(plan.Routing, Routed{Key: pair.Key, In: InQuery, Value: pair.Value})
		case op.parameter(pair.Key, InHeader) != nil:
			plan.Headers[pair.Key] = pair.Value
			plan.Routing = append(plan.Routing, Routed{Key: pair.Key, In: InHeader, Value: pair.Value})
		case op.parameter(pair.Key, InCookie) != nil:
			plan.Cookies = append(plan.Cookies, pair.Key+"="+pair.Value)
			plan.Routing = append(plan.Routing, Routed{Key: pair.Key, In: InCookie, Value: pair.Value})
		case op.bodyField(pair.Key) != nil:
			body = append(body, pair)
			plan.Routing = append(plan.Routing, Routed{Key: pair.Key, In: InBody, Value: pair.Value})
		default:
			if input.Strict {
				return plan, fmt.Errorf("%s names no parameter of %s; the operation takes %s", pair.Key, op.ID, op.parameterNames())
			}
			body = append(body, pair)
			plan.Routing = append(plan.Routing, Routed{Key: pair.Key, In: InBody, Value: pair.Value, Note: "not a parameter of " + op.ID + "; sent in the body"})
		}
	}
	// Every template segment has to be filled, because a request to the
	// template literally is never what was meant.
	var path []string
	for _, segment := range op.segments {
		name, ok := templateSegment(segment)
		if !ok {
			path = append(path, segment)
			continue
		}
		value, present := filled[name]
		if !present {
			return plan, fmt.Errorf("path segment {%s} of %s is not filled; pass -d %s=<value>", name, op.ID, name)
		}
		path = append(path, url.PathEscape(value))
	}
	plan.Path = "/" + strings.Join(path, "/")
	for _, parameter := range op.Parameters {
		if parameter.Required && parameter.In == InQuery && plan.Query.Get(parameter.Name) == "" {
			plan.Routing = append(plan.Routing, Routed{Key: parameter.Name, In: InQuery, Note: "required by " + op.ID + " and not given"})
		}
	}

	contentType := explicitContentType(input.Headers)
	switch {
	case len(input.Forms) > 0:
		encoded, boundary, err := encodeMultipart(input.Forms)
		if err != nil {
			return plan, err
		}
		plan.Body = encoded
		plan.ContentType = "multipart/form-data; boundary=" + boundary
	case input.RawSet:
		raw, err := readRaw(input.Raw)
		if err != nil {
			return plan, err
		}
		plan.Body = raw
		plan.ContentType = op.ContentType
		if plan.ContentType == "" {
			plan.ContentType = "application/x-www-form-urlencoded"
		}
	case len(body) > 0:
		mediaType := op.ContentType
		if contentType != "" && contains(op.ContentTypes, contentType) {
			mediaType = contentType
		}
		switch mediaType {
		case "application/json":
			encoded, notes := encodeJSONObject(op, body)
			plan.Body = encoded
			for index := range plan.Routing {
				if note, ok := notes[plan.Routing[index].Key]; ok && plan.Routing[index].In == InBody {
					plan.Routing[index].Note = note
				}
			}
		case "multipart/form-data":
			encoded, boundary, err := encodeMultipart(body)
			if err != nil {
				return plan, err
			}
			plan.Body = encoded
			mediaType = "multipart/form-data; boundary=" + boundary
		default:
			if mediaType == "" {
				mediaType = "application/x-www-form-urlencoded"
			}
			plan.Body = []byte(encodeForm(body))
		}
		plan.ContentType = mediaType
	}
	if contentType != "" {
		// An explicit header wins, so a caller testing a wrong content type
		// can, but the multipart boundary must survive it.
		if !strings.HasPrefix(plan.ContentType, "multipart/form-data; boundary=") {
			plan.ContentType = contentType
		}
	}
	return plan, nil
}

func routeCurl(input Input) (Plan, error) {
	plan := Plan{Method: input.Method, Path: input.Path, Query: url.Values{}, Headers: map[string]string{}}
	if len(input.Forms) > 0 && (len(input.Pairs) > 0 || input.RawSet) {
		return plan, fmt.Errorf("-F and -d cannot be combined, as in curl")
	}
	hasData := len(input.Pairs) > 0 || input.RawSet || len(input.Forms) > 0
	if plan.Method == "" {
		plan.Method = "GET"
		if hasData && !input.Query {
			plan.Method = "POST"
		}
	}
	switch {
	case len(input.Forms) > 0:
		encoded, boundary, err := encodeMultipart(input.Forms)
		if err != nil {
			return plan, err
		}
		plan.Body = encoded
		plan.ContentType = "multipart/form-data; boundary=" + boundary
		for _, form := range input.Forms {
			plan.Routing = append(plan.Routing, Routed{Key: form.Key, In: InBody, Value: form.Value})
		}
	case input.Query:
		for _, pair := range input.Pairs {
			plan.Query.Add(pair.Key, pair.Value)
			plan.Routing = append(plan.Routing, Routed{Key: pair.Key, In: InQuery, Value: pair.Value})
		}
		if input.RawSet {
			raw, err := readRaw(input.Raw)
			if err != nil {
				return plan, err
			}
			values, err := url.ParseQuery(string(raw))
			if err != nil {
				return plan, fmt.Errorf("-G with a body that is not a query string: %w", err)
			}
			for key, list := range values {
				for _, value := range list {
					plan.Query.Add(key, value)
				}
			}
		}
	case input.RawSet:
		raw, err := readRaw(input.Raw)
		if err != nil {
			return plan, err
		}
		plan.Body = raw
		plan.ContentType = "application/x-www-form-urlencoded"
		if input.JSON {
			plan.ContentType = "application/json"
		}
	case len(input.Pairs) > 0:
		if input.JSON {
			plan.Body, _ = encodeJSONObject(nil, input.Pairs)
			plan.ContentType = "application/json"
		} else {
			plan.Body = []byte(encodeForm(input.Pairs))
			plan.ContentType = "application/x-www-form-urlencoded"
		}
		for _, pair := range input.Pairs {
			plan.Routing = append(plan.Routing, Routed{Key: pair.Key, In: InBody, Value: pair.Value})
		}
	}
	if contentType := explicitContentType(input.Headers); contentType != "" &&
		!strings.HasPrefix(plan.ContentType, "multipart/form-data; boundary=") {
		plan.ContentType = contentType
	}
	if input.JSON {
		plan.Headers["Accept"] = "application/json"
	}
	return plan, nil
}

func (o *Operation) parameterNames() string {
	var names []string
	for _, parameter := range o.Parameters {
		names = append(names, parameter.Name+" ("+string(parameter.In)+")")
	}
	for _, field := range o.Body {
		names = append(names, field.Name+" (body)")
	}
	if len(names) == 0 {
		return "no parameter"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func explicitContentType(headers []Pair) string {
	for _, header := range headers {
		if strings.EqualFold(header.Key, "Content-Type") {
			return header.Value
		}
	}
	return ""
}

// readRaw reads a -d value that is a whole body: @file reads the file, @- reads
// standard input, and anything else is the bytes as given.
func readRaw(raw string) ([]byte, error) {
	if !strings.HasPrefix(raw, "@") {
		return []byte(raw), nil
	}
	name := strings.TrimPrefix(raw, "@")
	if name == "-" {
		return io.ReadAll(os.Stdin)
	}
	body, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return body, nil
}

func encodeForm(pairs []Pair) string {
	values := url.Values{}
	for _, pair := range pairs {
		values.Add(pair.Key, pair.Value)
	}
	return values.Encode()
}

// encodeJSONObject assembles pairs into one object, coercing each value by
// the schema type the operation declares for the field. A repeated key becomes
// an array. A value the schema cannot hold is kept as a string and noted, so
// the caller sees the coercion that did not happen rather than a silent 400.
func encodeJSONObject(op *Operation, pairs []Pair) ([]byte, map[string]string) {
	object := map[string]any{}
	notes := map[string]string{}
	var order []string
	for _, pair := range pairs {
		typeName := ""
		if op != nil {
			if field := op.bodyField(pair.Key); field != nil {
				typeName = field.Type
			}
		}
		value, note := coerce(pair.Value, typeName)
		if note != "" {
			notes[pair.Key] = note
		}
		existing, seen := object[pair.Key]
		switch {
		case !seen:
			object[pair.Key] = value
			order = append(order, pair.Key)
			if typeName == "array" {
				object[pair.Key] = []any{value}
			}
		default:
			list, isList := existing.([]any)
			if !isList {
				list = []any{existing}
			}
			object[pair.Key] = append(list, value)
		}
	}
	// Encode in first-seen order rather than map order, so the body a caller
	// reads back in the report is the body they wrote.
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range order {
		if index > 0 {
			buffer.WriteByte(',')
		}
		name, _ := json.Marshal(key)
		buffer.Write(name)
		buffer.WriteByte(':')
		encoded, _ := json.Marshal(object[key])
		buffer.Write(encoded)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), notes
}

// coerce turns a string into the JSON value the schema type names.
func coerce(value, typeName string) (any, string) {
	switch typeName {
	case "integer":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return value, "not an integer; sent as a string"
		}
		return parsed, ""
	case "number":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return value, "not a number; sent as a string"
		}
		return parsed, ""
	case "boolean":
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return value, "not a boolean; sent as a string"
		}
		return parsed, ""
	case "object", "array":
		var parsed any
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return value, "not JSON; sent as a string"
		}
		return parsed, ""
	default:
		return value, ""
	}
}

// encodeMultipart writes the fields as multipart/form-data. A value of @file
// attaches the file under its base name, as curl -F does.
func encodeMultipart(fields []Pair) ([]byte, string, error) {
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, field := range fields {
		if strings.HasPrefix(field.Value, "@") {
			name := strings.TrimPrefix(field.Value, "@")
			content, err := os.ReadFile(name)
			if err != nil {
				return nil, "", fmt.Errorf("read %s: %w", name, err)
			}
			part, err := writer.CreateFormFile(field.Key, filepath.Base(name))
			if err != nil {
				return nil, "", err
			}
			if _, err := part.Write(content); err != nil {
				return nil, "", err
			}
			continue
		}
		if err := writer.WriteField(field.Key, field.Value); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.Boundary(), nil
}
