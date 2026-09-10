package pwcheck

// PW04xx: wiring, configuration values, secret material, and the identity
// provider. Severity turns on the diagnosed token, never on a hard-coded list
// of deployment names.
const (
	// Wiring: configuration selects something the binary does not link.
	MissingSessionPlugin = "PW0401"
	MissingSQLDriver     = "PW0402"
	UnclaimedConfigKey   = "PW0403"
	DevIdPLinked         = "PW0404"
	UnusedLinkedPlugin   = "PW0405"
	MissingNetdev        = "PW0406"

	// Dependency: one feature needs another that is off.
	AuthWithoutSession  = "PW0407"
	SessionRDBWithoutMW = "PW0408"
	ReadonlyFrameworkDB = "PW0409"

	// Secret material, keyed to the configbind secret classification rather
	// than to a list of field names, so a signing key added later is covered
	// the moment its metadata marks it secret.
	LiteralSecretInFile  = "PW0412"
	PlaceholderSecret    = "PW0413"
	SecretSharedBetween  = "PW0414"
	SecretFileNotIgnored = "PW0415"
	SecretFilePerms      = "PW0416"
	// The dotenv template is the one file of its family .gitignore lets
	// through, so a value in it is committed whatever the environment.
	EnvTemplateHoldsSecret = "PW0438"

	// Environment-appropriate values.
	InsecureSessionCookie = "PW0410"
	ResponseHeadersWeak   = "PW0411"
	// Cross-origin admission. Every one of these fails in a browser and
	// nowhere else, which is what makes them worth reporting from here: the
	// deployment sees a network error somebody else's console recorded.
	CORSCredentialedOrigin = "PW0417"
	CORSPlaintextOrigin    = "PW0418"
	QueryDiagnosticsOn     = "PW0420"
	BindValuesOn           = "PW0421"
	VerboseLogLevel        = "PW0422"
	MemoryDatabase         = "PW0423"
	LocalPublicRead        = "PW0424"
	APICatalogWithoutAPI   = "PW0425"
	PlaintextLogs          = "PW0426"
	TelemetryDisabled      = "PW0427"
	// PW0428 named the single-DSN form of middleware.rdb, which no longer
	// exists. The identifier is retired rather than reused, so that an
	// identifier printed by one build never means two different things.
	APICatalogRelativeLinks = "PW0429"

	// Identity provider: dev may authenticate against the local emulator, and
	// a deployment must not.
	DevIdPEnabled        = "PW0430"
	DevelopmentIssuer    = "PW0431"
	InsecureIssuer       = "PW0432"
	RedirectDisagreement = "PW0433"
	ProviderNotDeclared  = "PW0434"
	// PW0435 stated that pw dev injects the provider credentials in dev. That
	// is the arrangement working, not a finding, so the identifier is retired
	// alongside PW0428.
	LoopbackPairing     = "PW0436"
	DynamicOIDCRedirect = "PW0437"
)

func init() {
	register(
		Check{
			ID: MissingSessionPlugin, Group: GroupConfig,
			Title:    "selected session backend is not linked",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: Config | ImportGraph, Phase: Doctor,
			Remedy: "add the blank import of the backend plugin",
		},
		Check{
			ID: MissingSQLDriver, Group: GroupConfig,
			Title:    "no database/sql driver answers the configured DSN scheme",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: Config | ImportGraph, Phase: Doctor,
			Remedy: "add the blank import of the driver package for that scheme",
		},
		Check{
			ID: UnclaimedConfigKey, Group: GroupConfig,
			Title:    "configured key belongs to no linked binding",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: Config | ImportGraph, Phase: Doctor,
			Remedy: "import the plugin that owns the key, or correct the key",
		},
		Check{
			ID: DevIdPLinked, Group: GroupConfig,
			Title:    "the development identity provider is linked into the application",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: ImportGraph, Phase: Doctor,
			Remedy: "remove the contrib/devidp import; pw dev runs it as a separate process",
		},
		Check{
			ID: UnusedLinkedPlugin, Group: GroupConfig,
			Title:    "a linked plugin is selected by no configuration",
			Severity: Note, DevSeverity: Note, Scope: Every,
			Inputs: Config | ImportGraph, Phase: Doctor,
			Remedy: "drop the import, or select it in configuration",
		},
		Check{
			ID: MissingNetdev, Group: GroupConfig,
			Title:    "a TinyGo project registers no Netdev",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: ProjectFiles, Phase: Doctor,
			Remedy: "restore tinygohelper.go with the tinygodriver/netdev blank import",
		},
		Check{
			ID: AuthWithoutSession, Group: GroupConfig,
			Title:    "authentication is enabled without a session store",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: Config, Phase: Doctor | Startup,
			Remedy: "enable session, whose rdb backend holds the login session",
		},
		Check{
			ID: SessionRDBWithoutMW, Group: GroupConfig,
			Title:    "the session store reuses a database middleware that is off",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: Config, Phase: Doctor | Startup,
			Remedy: "enable middleware.rdb, or give the session a dedicated source",
		},
		Check{
			ID: ReadonlyFrameworkDB, Group: GroupConfig,
			Title:    "a framework-owned write resolves to a readonly group",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: Config, Phase: Doctor | Startup,
			Remedy: "name a writable group in middleware.rdb.write_group",
		},
		Check{
			ID: InsecureSessionCookie, Group: GroupConfig,
			Title:    "the session cookie is not marked secure",
			Severity: Error, DevSeverity: Error, Scope: Deployed,
			Inputs: Config, Phase: Doctor | Startup,
			// api:cli-init writes the false into the development configuration
			// on purpose, because loopback development serves plain http.
			// Repeating it there is a note nobody can act on, so the check is
			// silent in dev and the process refuses to start with it anywhere
			// else. A cookie that is cross-site as well is broken in every
			// environment, and that form is reported at its own severity.
			Remedy: "set session.cookie.secure; false is a development-only exception, and the process refuses to start with it outside dev",
		},
		Check{
			ID: ResponseHeadersWeak, Group: GroupConfig,
			Title:    "browser response headers are disabled",
			Severity: Warning, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "enable security.headers",
		},
		Check{
			ID: CORSCredentialedOrigin, Group: GroupConfig,
			Title:    "a credentialed cross-origin caller is not trusted by the CSRF check",
			Severity: Warning, DevSeverity: Warning, Scope: Every,
			Inputs: Config, Phase: Doctor,
			// The two lists answer different questions and a credentialed
			// deployment needs both. Without the second one every unsafe
			// request from that origin is refused by the origin comparison, and
			// the browser reports the 403 as a cross-origin failure — so the
			// deployment looks for the mistake in the wrong policy.
			Remedy: "add each security.cors.allowed_origins entry to security.csrf.trusted_origins while allow_credentials is on",
		},
		Check{
			ID: CORSPlaintextOrigin, Group: GroupConfig,
			Title:    "a plain-http origin is admitted",
			Severity: Warning, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "name https origins in security.cors.allowed_origins outside development",
		},
		// Where a secret is kept — in the file, in version control, in a file
		// anyone on the host can read — is diagnosed for a deployment only.
		// A development machine keeps its secrets in config.dev.toml on
		// purpose: the password of a database devbox runs beside the
		// application is a development fixture, and that file is written to be
		// shared with the people who run it. PW0412, PW0415, and PW0416 are
		// therefore silent in dev.
		//
		// What a secret *is* stays a finding everywhere: a placeholder value is
		// published in the framework source, and a value shared with a
		// deployment is that deployment's secret whichever file it was read
		// from.
		Check{
			ID: LiteralSecretInFile, Group: GroupConfig,
			Title:    "a secret is set from the configuration file",
			Severity: Error, DevSeverity: Error, Scope: Deployed,
			Inputs: Config | ProjectFiles, Phase: Doctor,
			Remedy: "move the value to an environment variable, or reference one with ${NAME}",
		},
		Check{
			ID: PlaceholderSecret, Group: GroupConfig,
			Title:    "a secret still holds a scaffolded or placeholder value",
			Severity: Error, DevSeverity: Warning, Scope: Every,
			Inputs: Config, Phase: Doctor,
			Remedy: "replace it; a scaffold value is published in the framework source",
		},
		Check{
			ID: SecretSharedBetween, Group: GroupConfig,
			Title:    "one secret value is shared between environments",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: Config | OtherEnvironments, Phase: Doctor,
			Remedy: "give each environment its own value",
		},
		Check{
			ID: SecretFileNotIgnored, Group: GroupConfig,
			Title:    "a file holding a secret is tracked by version control",
			Severity: Error, DevSeverity: Error, Scope: Deployed,
			Inputs: ProjectFiles, Phase: Doctor,
			Remedy: "untrack the file and read the value from the environment instead",
		},
		Check{
			ID: SecretFilePerms, Group: GroupConfig,
			Title:    "a file holding a secret is readable beyond its owner",
			Severity: Warning, DevSeverity: Warning, Scope: Deployed,
			Inputs: ProjectFiles, Phase: Doctor,
			Remedy: "chmod 600 the file",
		},
		Check{
			ID: EnvTemplateHoldsSecret, Group: GroupConfig,
			Title:    "the dotenv template assigns a secret",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: ProjectFiles, Phase: Doctor,
			Remedy: "leave the value empty in .env.example and put it in .env or .env.{env}, which git ignores",
		},
		Check{
			ID: QueryDiagnosticsOn, Group: GroupConfig,
			Title:    "query diagnostics are enabled outside dev",
			Severity: Warning, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "set observability.query.enabled to off",
		},
		Check{
			ID: BindValuesOn, Group: GroupConfig,
			Title:    "query bind values are logged outside dev",
			Severity: Warning, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			// Bind values are the only path by which application row data
			// enters a framework SQL record.
			Remedy: "set observability.query.bind_values to off",
		},
		Check{
			ID: VerboseLogLevel, Group: GroupConfig,
			Title:    "the log level is below info outside dev",
			Severity: Warning, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "set observability.minimum_level to info or higher",
		},
		Check{
			ID: MemoryDatabase, Group: GroupConfig,
			Title:    "an in-memory database is configured outside dev",
			Severity: Warning, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "point the DSN at a durable database; the schema and every row are lost at restart",
		},
		Check{
			ID: LocalPublicRead, Group: GroupConfig,
			Title:    "public assets are served from the local filesystem outside dev",
			Severity: Warning, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "set server.public.read_local to false; pw dev forces it on its own",
		},
		Check{
			// A catalog with nothing to link would serve a document carrying a
			// status link, a self-referential item link, and no link to any API
			// description, which meets RFC 9727 section 4.1 on no reading. The
			// runtime refuses it, so this reports it before a start is tried.
			ID: APICatalogWithoutAPI, Group: GroupConfig,
			Title:    "the API catalog is enabled with no API document to link",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: Config, Phase: Doctor,
			Remedy: "set server.openapi, or turn server.api_catalog off",
		},
		Check{
			// The catalog's links are absolute, because RFC 9264 asks for
			// references that are not relative, so that a catalog kept after
			// the exchange that delivered it still resolves. With no origin
			// configured the links are relative instead: correct for the client
			// fetching them, and context-dependent for anything that stores
			// them. This is a note rather than a warning because the served
			// document is conformant and works; what it is missing is the
			// self-containment one setting would buy.
			ID: APICatalogRelativeLinks, Group: GroupConfig,
			Title:    "the API catalog publishes relative links",
			Severity: Note, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "name the deployment's canonical origin in server.api_catalog_origin",
		},
		Check{
			ID: PlaintextLogs, Group: GroupConfig,
			Title:    "logs are plaintext outside dev",
			Severity: Note, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "set observability.stdout_format to json for a log collector",
		},
		Check{
			ID: TelemetryDisabled, Group: GroupConfig,
			Title:    "telemetry export is disabled outside dev",
			Severity: Note, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "set observability.otel.endpoint to export traces and logs",
		},
		Check{
			ID: DevIdPEnabled, Group: GroupConfig,
			Title:    "the development identity provider is enabled outside dev",
			Severity: Error, DevSeverity: Note, Scope: Deployed,
			Inputs: ProjectFiles, Phase: Doctor,
			Remedy: "set dev.idp.enabled to false; it authenticates nobody",
		},
		Check{
			ID: DevelopmentIssuer, Group: GroupConfig,
			Title:    "the OIDC issuer is a development one outside dev",
			Severity: Error, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "set auth.oidc.issuer to the deployment provider",
		},
		Check{
			ID: InsecureIssuer, Group: GroupConfig,
			Title:    "the provider is reached over http outside dev",
			Severity: Error, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "use an https issuer and clear allow_loopback_http in the provider section auth.mode selects",
		},
		Check{
			ID: RedirectDisagreement, Group: GroupConfig,
			Title:    "the login redirect URL does not match the callback path",
			Severity: Error, DevSeverity: Error, Scope: Every,
			Inputs: Config, Phase: Doctor,
			// The provider would redirect to a URL the application does not
			// serve, and a loopback redirect that works locally hides it.
			Remedy: "make auth.oidc.redirect_url, or auth.oauth.redirect_url under oauth_only, end with auth.callback_path",
		},
		Check{
			ID: ProviderNotDeclared, Group: GroupConfig,
			Title:    "no provider values are declared for a deployed environment",
			Severity: Note, DevSeverity: Note, Scope: Deployed,
			Inputs: Config | ProcessEnv, Phase: Doctor,
			Remedy: "confirm the deployment sets AUTH_OIDC_ISSUER, AUTH_OIDC_CLIENT_ID, and AUTH_OIDC_CLIENT_SECRET under an OIDC mode, or AUTH_OAUTH_CLIENT_ID and AUTH_OAUTH_CLIENT_SECRET under oauth_only",
		},
		Check{
			ID: LoopbackPairing, Group: GroupConfig,
			Title:    "the loopback development pairing is still set outside dev",
			Severity: Error, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "clear allow_loopback_http in the provider section auth.mode selects and set session.cookie.secure",
		},
		Check{
			ID: DynamicOIDCRedirect, Group: GroupConfig,
			Title:    "the login redirect URL is derived from a request outside dev",
			Severity: Error, DevSeverity: Note, Scope: Deployed,
			Inputs: Config, Phase: Doctor,
			Remedy: "set auth.oidc.redirect_url, or auth.oauth.redirect_url under oauth_only, to the absolute URL registered with the deployed provider",
		},
	)
}
