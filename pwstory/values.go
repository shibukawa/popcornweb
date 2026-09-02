package pwstory

import (
	"reflect"
	"strings"
	"time"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

// synthesisDepth bounds how far a nested parameter type is filled. A template
// reads a few fields deep; a data model can be recursive, and a story is not
// worth a stack overflow.
const synthesisDepth = 4

// sampleSlice is how many elements a synthesized list gets. One would let a
// template that mishandles separators look correct, and many would make the
// page about the fixture rather than the markup.
const sampleSlice = 2

// sampleTime is fixed so a story rendered twice produces identical HTML. A
// story that changes every render reports nothing.
var sampleTime = time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)

// Synthesize fills a parameter value with representative data derived from its
// type and its field names.
//
// Representative rather than zero: a page of empty strings and zeros shows that
// a template compiles and nothing about what it renders. Deterministic, because
// the value of a story is that two renderings of it can be compared.
func Synthesize(target any) {
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return
	}
	fill(value.Elem(), "", 0)
}

func fill(value reflect.Value, name string, depth int) {
	if !value.CanSet() || depth > synthesisDepth {
		return
	}
	switch value.Type() {
	case reflect.TypeOf(time.Time{}):
		value.Set(reflect.ValueOf(sampleTime))
		return
	case reflect.TypeOf(htmlbind.Fragment{}):
		// A slot, which the story fills with marked content so that slot
		// placement is visible without inventing a child template.
		value.Set(reflect.ValueOf(slotFragment(name)))
		return
	}
	switch value.Kind() {
	case reflect.String:
		value.SetString(sampleString(name))
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(sampleInt(name))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(uint64(sampleInt(name)))
	case reflect.Float32, reflect.Float64:
		value.SetFloat(float64(sampleInt(name)) + 0.5)
	case reflect.Pointer:
		element := reflect.New(value.Type().Elem())
		fill(element.Elem(), name, depth+1)
		value.Set(element)
	case reflect.Slice:
		slice := reflect.MakeSlice(value.Type(), sampleSlice, sampleSlice)
		for index := range sampleSlice {
			fill(slice.Index(index), name, depth+1)
		}
		value.Set(slice)
	case reflect.Array:
		for index := range value.Len() {
			fill(value.Index(index), name, depth+1)
		}
	case reflect.Map:
		mapping := reflect.MakeMap(value.Type())
		key := reflect.New(value.Type().Key()).Elem()
		fill(key, name+" key", depth+1)
		element := reflect.New(value.Type().Elem()).Elem()
		fill(element, name, depth+1)
		mapping.SetMapIndex(key, element)
		value.Set(mapping)
	case reflect.Struct:
		for index := range value.NumField() {
			field := value.Type().Field(index)
			if !field.IsExported() {
				continue
			}
			fill(value.Field(index), field.Name, depth+1)
		}
	}
}

// sampleString reads the field name, so a story shows Title where a title goes
// rather than the same filler everywhere. The name is the only thing the type
// system knows about what the value means.
func sampleString(name string) string {
	if name == "" {
		return "Sample"
	}
	words := splitFieldName(name)
	switch strings.ToLower(strings.Join(words, " ")) {
	case "id", "uuid":
		return "01JQ0S3E7M9N2K4P6R8T0V2X4Z"
	case "email":
		return "sample@example.com"
	case "url", "href", "link":
		return "https://example.com/sample"
	case "slug":
		return "sample-entry"
	}
	return strings.Join(words, " ")
}

// sampleInt varies with the field name so two numeric fields in one story do
// not read as the same value copied twice, while staying deterministic.
func sampleInt(name string) int64 {
	sum := int64(0)
	for _, letter := range name {
		sum += int64(letter)
	}
	return 1 + sum%97
}

// splitFieldName turns a Go field name into words, so DisplayName reads as
// "Display Name" rather than as an identifier.
func splitFieldName(name string) []string {
	var words []string
	start := 0
	runes := []rune(name)
	for index := 1; index < len(runes); index++ {
		if runes[index] >= 'A' && runes[index] <= 'Z' {
			words = append(words, string(runes[start:index]))
			start = index
		}
	}
	return append(words, string(runes[start:]))
}

// slotParams carries nothing: the placeholder is a constant, and its whole job
// is to be visible where a child template would go.
type slotParams struct{}

var slotOps = htmlbind.Builder[slotParams]{}

var slotPlan = &htmlbind.Plan[slotParams]{
	Ops: []htmlbind.Op[slotParams]{
		slotOps.Static(`<div data-pw-story-slot style="border:1px dashed currentColor;opacity:.6;padding:.5rem;margin:.25rem 0">slot content</div>`),
	},
}

func slotFragment(string) htmlbind.Fragment { return slotPlan.Bind(slotParams{}) }
