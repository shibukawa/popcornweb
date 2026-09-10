---
id: requirement:formatter-settle-failure-report
type: requirement
title: A Non-Settling Template Is Reproducible From Its Report
---
When api:cli-fmt refuses a .pw.html source because formatting does not settle, the defect is fixed upstream in system:tinybind and the report a user can make carries what that fix needs, because the guard already protects the file and the remaining cost is the round trip.

```yaml
status: proposed; reported by an application on 2026-09-10 against a pages/page.pw.html that generates and builds
priority: should
defect:
  message: "templatefmt: <path>: formatting does not settle, so nothing was changed; this is a formatter bug, please report the file"
  origin: SourceAs in the system:tinybind templatefmt package formats twice and refuses a result that differs, the idempotence guard requirement:template-formatting relies on
  consequence: the file is left untouched and pw fmt exits 1, so a CI gate on this project cannot pass until upstream ships a fix
  generation_unaffected: the parser accepted the source, so api:cli-generate and api:cli-build are correct to succeed; a formatter defect is never a compile error
reproduction:
  local: every api:cli-init scaffold variant tried on 2026-09-10 and every example project settle under system:tinybind v0.5.31, so the trigger is a construct in the reported file that no fixture holds
  needed: the source file itself, or a reduced excerpt that still fails, plus the tinybind version pinned in the project's go.mod
  known_classes:
    escape_round_trip: a brace run in template text or an attribute value that is escaped on print and decoded on the next parse
    raw_text_braces: a brace in a script or style body near the insertion gate, the class fixed by a700e67 upstream
    whitespace_run: a run reshaped on the first pass and dropped or recreated on the second, which the fidelity rules forbid
    line_width: a line that wraps under the soft width on pass one and re-glues on pass two
  method: format once with the guard bypassed, diff the first and second outputs, and reduce the source to the smallest region that keeps the diff
report_shape:
  today: the message names the file and asks for a report, and the reporter has nothing else to attach
  proposed: pw fmt appends the pinned system:tinybind version and the unified diff of the two passes to stderr for this error alone, so a report is complete on the first send and a maintainer reproduces without the whole file
  guard_unchanged: the file is still left as it was; the diff is diagnostic output, not a written result
  placement: upstream, in the templatefmt error, so the editor path of decision:formatter-delivery prints the same thing
  bounded: the diff is of formatted output, which the reporter was about to write to disk anyway, so nothing secret is added
fix_path:
  upstream: the printer defect is fixed in system:tinybind with the reduced source as a fixture in its settle test set
  downstream: this repository moves the pin and re-runs pw fmt --check on every example, per requirement:template-formatting
  workaround: none in pw fmt; naming other paths skips the file, and the file stays unformatted until the pin moves
acceptance:
  - the reported file, once obtained, fails under the pinned version and settles under the fixed one
  - the reduced source is a fixture upstream, so the class cannot regress silently
  - pw fmt on a project containing a non-settling source reports it once, formats every other source, and exits 1
  - the settle error output includes the tinybind version and a diff of the two passes
  - pw fmt --check on every example project stays clean after the pin moves
non_goals:
  - a Popcorn Web layout rule or a local settle bypass; requirement:template-formatting keeps the layout upstream
  - relaxing the guard to write the first pass, which would corrupt the file a little on every save
  - treating the failure as a generate or build error
```
