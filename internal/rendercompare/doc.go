// Package rendercompare measures what a generated component buys over
// html/template, on one table written both ways.
//
// It exists because the claim is a number and a number that cannot be taken
// again is an anecdote. Why Popcorn Web sets an html/template parsed at startup
// against a typed component and quotes time, bytes and allocations for one,
// fifty and five hundred rows. The first harness behind that table was not kept,
// so the table could not be repeated when the toolchain moved; this is the
// second, and it stays.
//
// # What is held constant
//
// Both renderers read the same []Item and write to io.Discard, so what separates
// the rows is the rendering and nothing beneath it: no socket, no response
// writer, no framework entry. TestBothWritersProduceTheSameBytes fails if the two
// documents differ by a single byte, which is what makes a benchmark run beside a
// passing test a comparison of two renderers rather than of two documents.
//
// One row in five carries markup characters in its name, so escaping is part of
// what is measured and not a case only one side was ever asked to handle.
//
// # Running it
//
//	go test ./internal/rendercompare -run '^$' -bench . -benchmem -count 15
//
// The page quotes the median of fifteen runs, which benchstat computes from this
// output. allocs/op does not move when the machine is busy and is the column to
// trust first; ns/op does move, so take it on a quiet machine.
//
// # Regenerating
//
// pw generate compiles the component to Go and the result is committed, as for
// the other fixtures under internal/. A change to the emitter then shows up as a
// diff here, and this is the fixture that says whether it moved the numbers:
//
//	go test ./internal/pwcli -run RenderCompare -update
package rendercompare
