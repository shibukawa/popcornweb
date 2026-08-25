---
id: requirement:filter-form-state-round-trip
type: requirement
title: Filter Form State Round Trip
---
A GET form refining the page it sits on renders the current query back into its own controls, so a second refinement starts from what the page is showing instead of from an empty form that silently drops the rest of the query.

```yaml
source: the user's form-value question 2026-08-25
status: unbuilt; the shape is open, and the parameter-carrying half is a defect rather than a convenience
half_that_exists:
  direction: URL to typed page input
  where: requirement:discovered-page-routing, whose leading component parameters are the route segments in order and whose remaining ones are query keys by name
  optional: system:tinybind binds a trailing question mark as a pointer, so an absent key stays apart from an explicit zero
half_that_is_manual:
  direction: page input back to control state
  written: per control, by hand
  worked_example: the partial update example normalises the optional through an external and interpolates the result into value
  cost_is_uneven:
    text_input: one interpolation, and an optional supplying a whole attribute already omits it when nil
    checkbox_and_radio: checked takes a comparison against the control's own value, not the value itself
    select: the same comparison again, per option
    repeated_key: readable since requirement:repeated-query-parameter, so a checkbox group can be told what to check; writing the checked state is still the per-control comparison above, which leaves this requirement's own gap exactly where it was
this_is_not_only_ergonomics:
  fact: a GET form's fields become the whole query, and requirement:query-navigation-interception matches that deliberately so the intercepted path and the browser's own path agree
  consequence: a form carrying q, on a page also reading sort and page, drops both on submit unless the author renders a hidden field for each
  therefore: the round trip is what keeps the parameters a form does not display alive across a submission
  the_failure_is_silent: nothing at generation or at run time knows which of a page's inputs a given form was meant to carry, so a forgotten one loses state with no error anywhere
shape_is_open:
  form_level_flag:
    proposal: mark the form, and fill every control from the current query by matching the control name against the query key
    raised_by: the user 2026-08-25
    against_declared_inputs: requirement:discovered-page-routing makes the declared parameter list the only reader of the URL, and a form reading raw keys adds an input no signature carries; a component annotated cache computes its key from its parameters, so a page varying with an undeclared key returns a stale hit
    against_the_expensive_controls: a name-to-value fill cannot express checked or selected, which need a comparison rather than a value, so it misses exactly the controls that cost the most to write
    against_where_it_would_run: on the client it would break the invariant that enabling updates changes no byte a scriptless client renders, and on the server the declared inputs are already in scope, which is what makes the flag redundant there
  control_level_sugar:
    proposal: one attribute meaning compare this control's own value against this expression, lowering to checked or selected
    keeps: the declared input list as the only reader, since the expression names a parameter
    covers: the half a plain interpolation cannot say compactly
  carry_the_rest:
    proposal: a way to emit hidden fields for the page inputs a form does not otherwise carry
    why_separable: it answers the silent-drop failure alone, and that failure is the part no author workaround detects
  no_framework_change:
    shape: an external returning a struct of already-normalised fields, so the template reads them off it
    expressible_today: yes, and it is the recommendation until one of the above is chosen
acceptance:
  - a filter form on a page reading several query keys submits without dropping the keys it does not display
  - a checkbox, radio, or select filter renders in the state the current URL describes
  - whatever is added renders identically with the update runtime absent, per requirement:classic-web-acceptance
non_goals:
  - client-held filter state, since state that can live in the URL belongs there per flow:partial-refresh
  - reading a query key no page input declares
adjacent_and_distinct: system:tinybind reconciles form state across a region swap, carrying user-typed values through an update by comparing each control against its own default; it never seeds a control from the URL, and it runs only where a delta was applied
```
