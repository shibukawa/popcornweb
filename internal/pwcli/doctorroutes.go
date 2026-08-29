package pwcli

import (
	"errors"

	"github.com/shibukawa/popcornweb/internal/pwcheck"
	"github.com/shibukawa/popcornweb/internal/pwroutes"
	"github.com/shibukawa/popcornweb/pwruntime"
)

// checkRoutes runs the PW02xx entries of rule:route-and-template-checks over
// data:route-table.
//
// The table is a build product: a project that has not generated has none, and
// this reports that as a limit rather than as a clean route section. That is
// the failure mode the whole Input mechanism exists to prevent — a report that
// looks clean because it did not look.
func (r *checkRun) checkRoutes() *doctorLimit {
	table, err := pwroutes.Load(r.Root)
	switch {
	case errors.Is(err, pwroutes.ErrAbsent):
		return &doctorLimit{
			Subject: "routes",
			Reason:  "no route table has been generated; run pw generate",
			Effect:  "the route checks (PW02xx) did not run",
		}
	case err != nil:
		return &doctorLimit{
			Subject: "routes",
			Reason:  "the route table could not be read: " + err.Error(),
			Effect:  "the route checks (PW02xx) did not run",
		}
	}

	// The framework mounts are this environment's rather than the table's:
	// their paths are runtime configuration, and a mount that is off collides
	// with nothing.
	diagnosed := table.WithMounts(r.frameworkMounts())

	for pattern, entries := range diagnosed.Duplicates() {
		// Both sides, because the reader has to choose which one to remove and
		// a message naming one of them chooses for them.
		r.report(pwcheck.DuplicateRoutePattern,
			"two registrations serve "+pattern,
			describeSites(entries))
	}
	for _, clash := range diagnosed.MountClashes() {
		r.report(pwcheck.FrameworkMountClash,
			clash[0].Pattern+" is also the framework mount enabled by "+clash[1].EnabledBy,
			describeSites(clash[0:1]))
	}

	// An unresolved registration is a finding rather than a limit: the route
	// runs, and what is missing is that nothing else in the report knows about
	// it. rule:route-and-template-checks ranks it a note, which is what says so
	// without claiming the project is broken.
	for _, unresolved := range table.Unresolved {
		r.report(pwcheck.UnresolvedRegistration,
			"the pattern here is not a literal, so no route check covers it",
			unresolved.Site.File+":"+itoa(unresolved.Site.Line)+" ("+unresolved.Reason+")")
	}
	return nil
}

// frameworkMounts is what this environment's configuration turns on.
func (r *checkRun) frameworkMounts() []pwroutes.Mount {
	return []pwroutes.Mount{
		{Pattern: r.Config.raw("server.health"), EnabledBy: "server.health"},
		{Pattern: r.Config.raw("server.readiness"), EnabledBy: "server.readiness"},
		{Pattern: r.Config.raw("server.openapi"), EnabledBy: "server.openapi"},
		{Pattern: r.Config.raw("server.api_doc_path"), EnabledBy: "server.api_doc"},
		// The catalog's path is RFC 9727's rather than a key's, so it is named
		// here: the route table is where a reader learns which addresses the
		// framework answers on, and this is the one they cannot read off the
		// configuration file.
		{Pattern: r.apiCatalogMount(), EnabledBy: "server.api_catalog"},
	}
}

// apiCatalogMount is the fixed path when the switch is on, and the empty
// pattern a disabled mount contributes otherwise.
func (r *checkRun) apiCatalogMount() string {
	if enabled, resolved := r.Config.boolValue("server.api_catalog"); resolved && enabled {
		return pwruntime.APICatalogPath
	}
	return ""
}

// describeSites names where entries were written, which is the evidence a
// reader needs to open one of them.
func describeSites(entries []pwroutes.Entry) string {
	places := make([]string, 0, len(entries))
	for _, entry := range entries {
		switch {
		case entry.Site != nil:
			places = append(places, entry.Site.File+":"+itoa(entry.Site.Line))
		case entry.Page != "":
			places = append(places, entry.Page)
		default:
			places = append(places, string(entry.Origin))
		}
	}
	sortStrings(places)
	return joinWith(places, " and ")
}

func joinWith(parts []string, separator string) string {
	out := ""
	for index, part := range parts {
		if index > 0 {
			out += separator
		}
		out += part
	}
	return out
}

// countOf words a number of things, because "1 registrations" reads as a
// defect in the report rather than in the project.
func countOf(count int, noun string) string {
	if count == 1 {
		return "one " + noun
	}
	return itoa(count) + " " + noun + "s"
}
