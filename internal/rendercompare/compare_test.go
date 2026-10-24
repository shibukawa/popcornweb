package rendercompare

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"testing"

	"github.com/shibukawa/tinybind-go/htmlbind"
)

// view is what the template ranges over. The component takes the same rows as
// its own parameter.
type view struct{ Items []Item }

// stdTable is the table as html/template, parsed once at startup, which is how
// an application holds one.
//
// The component's body sits between two line breaks, which it renders as one
// space each; the template carries the same two spaces so that both write the
// same bytes.
var stdTable = template.Must(template.New("inventory").Parse(
	` <table class="inventory"><thead><tr><th>Code</th><th>Name</th><th>Qty</th><th>Price</th><th>Stock</th></tr></thead><tbody>` +
		`{{range .Items}}<tr class="{{if .InStock}}in{{else}}out{{end}}"><td>{{.Code}}</td><td>{{.Name}}</td><td>{{.Qty}}</td><td>{{.Price}}</td><td>{{if .InStock}}<em>in stock</em>{{end}}</td></tr>{{end}}` +
		`</tbody></table> `))

// sizes are the row counts the page quotes: one row is the fixed cost of a
// render, fifty is a page, five hundred is a page nobody should send.
var sizes = []int{1, 50, 500}

// data builds n rows. One row in five carries markup characters in its name so
// that escaping is part of what is measured.
func data(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		name := fmt.Sprintf("Widget %d", i)
		if i%5 == 0 {
			name = fmt.Sprintf("Widget <XL> & Co %d", i)
		}
		items[i] = Item{
			Code:    fmt.Sprintf("SKU-%04d", i),
			Name:    name,
			Qty:     i % 97,
			Price:   fmt.Sprintf("%d.%02d", 10+i%90, i%100),
			InStock: i%3 != 0,
		}
	}
	return items
}

// A comparison of two renderers is only that if the two write the same document.
// The benchmarks below say nothing about this, so this test does: a change that
// made either side cheaper by writing less would otherwise read as a win.
func TestBothWritersProduceTheSameBytes(t *testing.T) {
	for _, n := range sizes {
		items := data(n)
		var fromTemplate, fromComponent bytes.Buffer
		if err := stdTable.Execute(&fromTemplate, view{items}); err != nil {
			t.Fatal(err)
		}
		if err := htmlbind.Render(&fromComponent, Table(TableParams{Items: items})); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fromTemplate.Bytes(), fromComponent.Bytes()) {
			t.Fatalf("rows=%d: html/template wrote %d bytes and the component %d, and they differ\n%q\n%q",
				n, fromTemplate.Len(), fromComponent.Len(), head(fromTemplate.Bytes()), head(fromComponent.Bytes()))
		}
		t.Logf("rows=%d: %d bytes from both", n, fromTemplate.Len())
	}
}

func head(b []byte) []byte {
	if len(b) > 400 {
		return b[:400]
	}
	return b
}

func BenchmarkHTMLTemplate(b *testing.B) {
	for _, n := range sizes {
		items := data(n)
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := stdTable.Execute(io.Discard, view{items}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGenerated(b *testing.B) {
	for _, n := range sizes {
		items := data(n)
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := htmlbind.Render(io.Discard, Table(TableParams{Items: items})); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
