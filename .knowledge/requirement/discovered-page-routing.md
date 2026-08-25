---
id: requirement:discovered-page-routing
type: requirement
title: Discovered Page Routing
---
Popcorn Web must serve an HTML website from a page tree whose directories are the routes, alongside the registered router rather than instead of it.

```yaml
dependency: system:tinybind v0.2.6 routetree, from v0.2.3 which is what the module pins today
layers:
  tree: concept:page-tree and rule:page-directory-naming
  coexistence: decision:dual-router-coexistence
  scaffold: decision:page-router-scaffold-choice
  rendering: decision:page-render-binding over api:page-render-runtime
  surface: api:page-registry, data:page-route-table, and api:page-action-endpoint
  generation: flow:page-route-generation
delivery:
  order:
    - api:page-render-runtime, then the emitter symbols and the render block, then one generated page served through api:html-response
    - the api:cli-init router question and the page tree scaffold
    - the generate.pages purpose in api:cli-generate, api:cli-dev, and api:cli-add
    - api:page-action-endpoint registration, its binder run, and policy:csrf-protection over its prefix
    - the action capability module of requirement:framework-script-assets
  reason: a page that renders through the framework response path is what every later rung depends on, and the scaffold is worth nothing before it
  upstream_effect: v0.2.5 turned the first item from three replaced generation templates into symbols, one block, and one small pw package
  placement: this sits beside the classic layers of decision:web-runtime-delivery-order and needs none of the modern-UI rungs above streaming
acceptance:
  - a directory holding page.pw.html serves its route with no registration written by the application
  - a page renders inside the registered document shell and the project error pages answer a decode failure
  - an ancestor layout wraps every page below it, outermost first
  - a page binds its own loader in the template and a handler-rung Load owns its whole response, per decision:page-rung-ladder
  - a page tree and a handlers tree serve from one mux in the same project
  - a page route appears in neither the generated OpenAPI document nor the rule:static-route-discovery diagnostics, verified with a project whose page tree has been through the binder run
  - an action handler reads a typed request through pw.Parse
  - an absent query parameter is distinguishable from an explicit zero
  - api:cli-init writes a page tree for the answers that ask for one and an empty generate.pages list for the ones that do not
  - api:cli-check fails on a stale page tree artifact
  - api:cli-dev regenerates when a page directory is created, not only when a watched file changes
  - a rejected directory name reports the reason rather than breaking the module build
  - a page tree with no page.pw.html anywhere generates an empty registry rather than failing
known_gaps:
  - scriptless form submission, without which adopting actions costs the no-runtime property of requirement:classic-web-acceptance
  - typed and cache-invalidating actions, which remain api:server-action
  - server-action outside a page tree, for which the upstream resolver seam now exists
  - page metadata, sitemap, and robots artifacts, for which data:page-route-table is the material
  - a query key reaching a form control again, and a repeated key reaching an input at all, per requirement:filter-form-state-round-trip and requirement:repeated-query-parameter
closed_upstream:
  v0_2_5:
    - api:request-binding inside an action
    - an optional query parameter distinguishable from a zero one
    - the router type as its own symbol
    - the render call as a replaceable block
  v0_2_6:
    - a branded generated header that discovery still skips
    - the failure entry as a symbol, so pw keeps the name WriteProblem
  effect: nothing in this requirement needs a whole-file generation template or a renamed pw function any more
non_goals:
  - replacing flow:handler-registration
  - a page route with a method other than GET
  - OpenAPI for page routes
```
