// Package firestore opens the application's Firestore client from
// configuration and keeps it as process state every operation reaches through
// [Handle].
//
// Importing it registers the [middleware.firestore] binding, so a project that
// does not use Firestore gains no configuration key and links no driver:
//
//	import _ "github.com/shibukawa/popcornweb/database/firestore"
//
// The database must have been created in Datastore mode, which is chosen at
// creation and cannot be changed. Startup checks it, because a native-mode
// database answers this API with the same status a missing composite index
// produces and the two would otherwise be indistinguishable.
//
// It wraps no operation. A handler calls tinybind's firestorebind directly,
// handing it the process handle. A generated .pw.firestore query resolves the
// same handle itself, so its call sites stay context-only:
//
//	h, err := firestore.Handle(ctx)
//	reading, err := h.Load[Reading](ctx, datastore.NameKey("Reading", id))
//
// The client is a deployment fact fixed for a process, so nothing is installed
// into request contexts: no context.Value stands between a call site and the
// client.
//
// A kind belongs to the type rather than to the deployment, so nothing here
// maps a name. The namespace is the one isolation dimension, and it is a client
// option set once.
package firestore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/shibukawa/popcornweb/pwextension"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/firestorebind"
	"github.com/shibukawa/tinygodriver/cloud/google"
	"github.com/shibukawa/tinygodriver/nosql/datastore"
)

func init() {
	pwextension.Register(pwextension.Extension{
		Name:  "database.firestore",
		Slot:  pwruntime.SlotStorage,
		Setup: setup,
		Close: closeRuntime,
	})
}

// audience is what a self-signed JWT is minted for. It is per-service and
// carries the trailing slash the token endpoint requires; a token minted for
// another API is refused by this one.
const audience = "https://datastore.googleapis.com/"

// probeKind and probeName address the entity the startup and readiness checks
// look up. Nothing writes it, and the lookup passing is not about finding it.
const (
	probeKind = "popcornweb_probe"
	probeName = "reachability"
)

// state is the process client. It is rebuilt whenever framework initialization
// runs.
var state struct {
	sync.RWMutex
	client *datastore.Client
	source *google.CachedSource
	handle firestorebind.Handle
}

// Handle returns the Datastore client bound to the tenancy of this deployment,
// which is what the "On"-suffixed firestorebind entries take and what every
// generated .pw.firestore query resolves through.
//
// The client is a deployment fact fixed for a process, so the common path
// reads process state and walks no context chain. When the process holds no
// client — a unit test building its own context, or a tool running without
// this extension — a handle installed with firestorebind.WithClient or
// WithHandle is honoured instead.
func Handle(ctx context.Context) (firestorebind.Handle, error) {
	state.RLock()
	handle := state.handle
	state.RUnlock()
	if handle.Client() != nil {
		return handle, nil
	}
	return firestorebind.HandleFromContext(ctx)
}

// tokenSource is a token supplied by the process, for credentials = "static".
// It is set before framework initialization runs.
var tokenSource struct {
	sync.RWMutex
	source google.TokenSource
}

// SetTokenSource installs the bearer tokens a static credential uses.
//
// It exists for a deployment whose token comes from a companion service rather
// than from a key file or the metadata server, and for a test that wants a
// client without one. Call it before the application starts.
func SetTokenSource(source google.TokenSource) {
	tokenSource.Lock()
	tokenSource.source = source
	tokenSource.Unlock()
}

// EnsureClient returns a context on which firestorebind's context-form entries
// resolve the process client, reporting false when none can be reached.
//
// A pw call site does not need it: Handle reads the process state directly, so
// neither a request context nor a setup context carries a client node. It
// remains for code handing a context to something that still calls the
// context-form firestorebind entries. A context that already carries a client
// is returned unchanged.
func EnsureClient(ctx context.Context) (context.Context, bool) {
	if _, err := firestorebind.ClientFromContext(ctx); err == nil {
		return ctx, true
	}
	state.RLock()
	handle := state.handle
	state.RUnlock()
	if handle.Client() == nil {
		return ctx, false
	}
	return firestorebind.WithHandle(ctx, handle), true
}

// Client returns the process client, for an operation firestorebind does not
// wrap.
//
// Reach for this only when calling the driver directly, and pass keys through
// the handle's KeyFor when you do — the client applies no namespace of its
// own. Everything firestorebind wraps takes the whole Handle instead.
func Client(ctx context.Context) (*datastore.Client, error) {
	handle, err := Handle(ctx)
	if err != nil {
		return nil, err
	}
	return handle.Client(), nil
}

// setup opens the client and proves the database is reachable and in Datastore
// mode. It returns no middleware: the request path reads the process handle
// through Handle, so no context node is installed per request.
func setup(ctx context.Context) (pwextension.Middleware, error) {
	config := pwruntime.ResolveConfig[Config](ctx)
	if err := config.validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, nil
	}

	client, source, err := open(config)
	if err != nil {
		return nil, err
	}

	if err := probe(ctx, client, config); err != nil {
		_ = client.Close()
		if source != nil {
			source.Invalidate()
		}
		return nil, err
	}

	state.Lock()
	state.client = client
	state.source = source
	state.handle = firestorebind.NewHandle(client)
	state.Unlock()

	return nil, nil
}

// open builds the client from configuration.
func open(config Config) (*datastore.Client, *google.CachedSource, error) {
	project := config.ProjectID
	if project == "" {
		project = google.ProjectIDFromEnv()
	}
	if project == "" {
		return nil, nil, errors.New(
			"middleware.firestore.project_id is empty and neither GOOGLE_CLOUD_PROJECT nor DATASTORE_PROJECT_ID is set")
	}

	options := []datastore.Option{datastore.WithTimeout(config.Timeout)}
	if config.Database != "" {
		options = append(options, datastore.WithDatabase(config.Database))
	}
	if config.Namespace != "" {
		options = append(options, datastore.WithNamespace(config.Namespace))
	}
	if config.Endpoint != "" {
		options = append(options, datastore.WithEndpoint(config.Endpoint))
	}
	if config.MaxIdleConns > 0 {
		options = append(options, datastore.WithMaxIdleConns(config.MaxIdleConns))
	}

	source, err := resolveTokenSource(config)
	if err != nil {
		return nil, nil, err
	}
	if source != nil {
		options = append(options, datastore.WithTokenSource(source))
	}

	client, err := datastore.New(project, options...)
	if err != nil {
		// The driver's error names the missing project or credential and holds
		// no key material, so it is wrapped rather than replaced.
		return nil, nil, fmt.Errorf("middleware.firestore: %w", err)
	}
	return client, source, nil
}

// resolveTokenSource builds the bearer tokens the configured source mints.
//
// An emulator endpoint resolves a placeholder instead of a credential. The
// emulator ignores Authorization entirely, so minting a real token would be
// pretending to exercise the credential path; the driver drops the header
// altogether when it recognises DATASTORE_EMULATOR_HOST, but an endpoint named
// in configuration is not that case and it still wants a source.
func resolveTokenSource(config Config) (*google.CachedSource, error) {
	if usingEmulator(config) {
		return google.Cached(google.StaticTokenSource(google.Token{Value: "emulator"})), nil
	}
	switch config.credentials() {
	case CredentialsMetadata:
		return google.Cached(google.MetadataTokenSource()), nil

	case CredentialsStatic:
		tokenSource.RLock()
		supplied := tokenSource.source
		tokenSource.RUnlock()
		if supplied == nil {
			return nil, errors.New(
				`middleware.firestore.credentials = "static" needs a token source; ` +
					"call database/firestore.SetTokenSource before starting the application")
		}
		return google.Cached(supplied), nil

	case CredentialsOAuth2:
		credentials, err := readCredentials(config)
		if err != nil {
			return nil, err
		}
		oauth, err := google.OAuth2TokenSource(credentials, "https://www.googleapis.com/auth/datastore")
		if err != nil {
			return nil, fmt.Errorf("middleware.firestore: %w", err)
		}
		return google.Cached(oauth), nil

	default:
		credentials, err := readCredentials(config)
		if err != nil {
			return nil, err
		}
		jwt, err := google.JWTTokenSource(credentials, audience)
		if err != nil {
			return nil, fmt.Errorf("middleware.firestore: %w", err)
		}
		return google.Cached(jwt), nil
	}
}

// readCredentials loads the service account key. The path is named in an error
// and the file's contents never are.
func readCredentials(config Config) (google.Credentials, error) {
	path := strings.TrimSpace(config.CredentialsFile)
	if path == "" {
		credentials, err := google.CredentialsFromEnv()
		if err != nil {
			return google.Credentials{}, fmt.Errorf(
				"middleware.firestore.credentials_file is empty and GOOGLE_APPLICATION_CREDENTIALS did not resolve: %w", err)
		}
		return credentials, nil
	}
	credentials, err := google.CredentialsFromFile(path)
	if err != nil {
		return google.Credentials{}, fmt.Errorf("middleware.firestore.credentials_file %q: %w", path, err)
	}
	return credentials, nil
}

// usingEmulator reports whether the client will talk to the emulator, which is
// the one case where no credential is resolved.
func usingEmulator(config Config) bool {
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = google.EmulatorHost("datastore")
	}
	if endpoint == "" {
		return false
	}
	return !strings.HasPrefix(endpoint, "https://")
}

// probe looks up one reserved key.
//
// Any answer passes, including the ordinary miss: the point is not to find the
// entity but to prove the project, the database, the credential, the token, the
// permission and the mode in one round trip. It is what replaces the schema
// verification a store with a schema would do — nothing here reports a kind, so
// a shape mismatch is not detectable and the guide says so rather than letting
// this look like it covers one.
func probe(ctx context.Context, client *datastore.Client, config Config) error {
	_, err := client.Get(ctx, datastore.NameKey(probeKind, probeName))
	switch {
	case err == nil, errors.Is(err, datastore.ErrNoSuchEntity):
		return nil

	case errors.Is(err, datastore.ErrFailedPrecondition):
		// The probe carries no filter and no order, so a composite index cannot
		// be what the service is objecting to. That is what lets this name the
		// mode instead of repeating the status.
		return fmt.Errorf(
			"middleware.firestore: database %s is not in Datastore mode. "+
				"Firestore serves either Datastore mode or native mode, the mode is chosen when the "+
				"database is created and cannot be changed, and this driver speaks Datastore mode only. "+
				"Create a database in Datastore mode and name it in middleware.firestore.database: %w",
			describeDatabase(config), err)

	case errors.Is(err, datastore.ErrUnauthenticated):
		// The status points at the credential, and in the field the credential
		// is usually fine: a self-signed JWT is only valid against the server's
		// clock, and a clock hours out mints a token that reads as expired.
		return fmt.Errorf(
			"middleware.firestore: the service rejected the credential for %s. "+
				"A token is signed against this host's clock, so check the clock before the key: %w",
			describeDatabase(config), err)

	case errors.Is(err, datastore.ErrPermissionDenied):
		return fmt.Errorf(
			"middleware.firestore: the credential has no Datastore access to %s: %w",
			describeDatabase(config), err)

	default:
		return fmt.Errorf("middleware.firestore: cannot reach %s: %w", describeDatabase(config), err)
	}
}

// describeDatabase names the target of a message without naming a credential.
func describeDatabase(config Config) string {
	project := config.ProjectID
	if project == "" {
		project = google.ProjectIDFromEnv()
	}
	if config.Database == "" {
		return fmt.Sprintf("the default database of project %q", project)
	}
	return fmt.Sprintf("database %q of project %q", config.Database, project)
}

// Ready reports whether the store answers, for a readiness probe.
//
// It is the same reserved-key lookup startup makes, because there is no table
// listing and no ping on this API: a probe has to be an ordinary read. That
// costs one small read per call, which is worth knowing when setting the
// interval.
func Ready(ctx context.Context) error {
	state.RLock()
	client := state.client
	state.RUnlock()
	if client == nil {
		return errors.New("middleware.firestore is not enabled")
	}
	_, err := client.Get(ctx, datastore.NameKey(probeKind, probeName))
	if err == nil || errors.Is(err, datastore.ErrNoSuchEntity) {
		return nil
	}
	return err
}

// closeRuntime releases the pooled connections during shutdown.
func closeRuntime(context.Context) error {
	state.Lock()
	client, source := state.client, state.source
	state.client = nil
	state.source = nil
	state.handle = firestorebind.Handle{}
	state.Unlock()
	if source != nil {
		source.Invalidate()
	}
	if client == nil {
		return nil
	}
	return client.Close()
}
