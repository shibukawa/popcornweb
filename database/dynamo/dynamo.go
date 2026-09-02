// Package dynamo opens the application's DynamoDB client from configuration and
// keeps it as process state every operation reaches through [Handle].
//
// Importing it registers the [middleware.dynamo] binding, so a project that
// does not use DynamoDB gains no configuration key and links no driver:
//
//	import _ "github.com/shibukawa/popcornweb/database/dynamo"
//
// It wraps no operation. There is no database/sql here to hide three engines
// behind, so a handler calls tinybind's dynamobind directly, handing it the
// process handle. A generated .pw.dynamo query resolves the same handle
// itself, so its call sites stay context-only:
//
//	h, err := dynamo.Handle(ctx)
//	reading, err := h.Load[Reading](ctx, "reading", key)
//	for reading, err := range records.ReadingsSince(ctx, sensor, from) { ... }
//
// The client is a deployment fact fixed for a process, so nothing is installed
// into request contexts: no context.Value stands between a call site and the
// client.
//
// Table names in source are the declared ones. The deployed name comes from the
// configured prefix or mapping, carried by the handle and applied inside the
// runtime entry, so no call site builds a deployed name.
package dynamo

import (
	"context"
	"fmt"
	"sync"

	"github.com/shibukawa/popcornweb/pwconfig"
	"github.com/shibukawa/popcornweb/pwextension"
	"github.com/shibukawa/popcornweb/pwruntime"
	"github.com/shibukawa/tinybind-go/dynamobind"
	"github.com/shibukawa/tinygodriver/cloud/aws"
	"github.com/shibukawa/tinygodriver/nosql/dynamodb"
)

func init() {
	pwextension.Register(pwextension.Extension{
		Name:  "database.dynamo",
		Slot:  pwruntime.SlotStorage,
		Setup: setup,
		Close: closeRuntime,
	})
}

// state is the process client and the naming it was installed with. It is
// rebuilt whenever framework initialization runs.
var state struct {
	sync.RWMutex
	client   *dynamodb.Client
	resolver TableResolver
	handle   dynamobind.Handle
}

// Handle returns the DynamoDB client bound to the deployed table naming, which
// is what the "On"-suffixed dynamobind entries take and what every generated
// .pw.dynamo query resolves through.
//
// The client is a deployment fact fixed for a process, so the common path
// reads process state and walks no context chain. When the process holds no
// client — a unit test building its own context, or a tool running without
// this extension — a handle installed with dynamobind.WithClient or WithHandle
// is honoured instead.
func Handle(ctx context.Context) (dynamobind.Handle, error) {
	state.RLock()
	handle := state.handle
	state.RUnlock()
	if handle.Client() != nil {
		return handle, nil
	}
	return dynamobind.HandleFromContext(ctx)
}

// EnsureClient returns a context on which dynamobind's context-form entries
// resolve the process client, reporting false when none can be reached.
//
// A pw call site does not need it: Handle reads the process state directly, so
// neither a request context nor a setup context carries a client node. It
// remains for code handing a context to something that still calls the
// context-form dynamobind entries. A context that already carries a client is
// returned unchanged.
func EnsureClient(ctx context.Context) (context.Context, bool) {
	if _, err := dynamobind.ClientFromContext(ctx); err == nil {
		return ctx, true
	}
	state.RLock()
	handle := state.handle
	state.RUnlock()
	if handle.Client() == nil {
		return ctx, false
	}
	return dynamobind.WithHandle(ctx, handle), true
}

// Client returns the process client, for an operation dynamobind does not wrap.
//
// Reach for this only when calling the driver directly; everything dynamobind
// wraps takes the whole Handle instead, which also carries the table naming.
func Client(ctx context.Context) (*dynamodb.Client, error) {
	handle, err := Handle(ctx)
	if err != nil {
		return nil, err
	}
	return handle.Client(), nil
}

// setup opens the client and verifies the schema. It returns no middleware:
// the request path reads the process handle through Handle, so no context node
// is installed per request.
func setup(ctx context.Context) (pwextension.Middleware, error) {
	config := pwruntime.ResolveConfig[Config](ctx)
	if err := config.validate(pwconfig.Development()); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, nil
	}

	resolver := resolverFor(config)
	if err := validateTableNames(ctx, config, resolver); err != nil {
		return nil, err
	}

	client, err := open(config)
	if err != nil {
		return nil, err
	}

	state.Lock()
	state.client = client
	state.resolver = resolver
	state.handle = dynamobind.NewHandle(client, dynamobind.WithTableNames(resolver))
	state.Unlock()

	// The migrator and the verifier read the same process handle a request
	// does, so they cannot resolve a name differently from one.
	if config.AutoMigrate {
		if _, err := Migrate(ctx); err != nil {
			return nil, err
		}
	}
	if config.VerifySchema {
		if err := verify(ctx, client, resolver); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// open builds the client from configuration. Credentials fall back to the
// environment, which is what a deployment on an instance role wants.
func open(config Config) (*dynamodb.Client, error) {
	options := []dynamodb.Option{dynamodb.WithTimeout(config.Timeout)}
	if config.Region != "" {
		options = append(options, dynamodb.WithRegion(config.Region))
	}
	if config.Endpoint != "" {
		options = append(options, dynamodb.WithEndpoint(config.Endpoint))
	}
	if config.MaxIdleConns > 0 {
		options = append(options, dynamodb.WithMaxIdleConns(config.MaxIdleConns))
	}
	if config.AccessKeyID != "" {
		options = append(options, dynamodb.WithCredentials(aws.Credentials{
			AccessKeyID:     config.AccessKeyID,
			SecretAccessKey: config.SecretAccessKey,
			SessionToken:    config.SessionToken,
		}))
	}
	client, err := dynamodb.New(options...)
	if err != nil {
		// The driver's error names the missing region or credential and holds
		// no secret, so it is wrapped rather than replaced.
		return nil, fmt.Errorf("middleware.dynamo: %w", err)
	}
	return client, nil
}

// closeRuntime releases the pooled connections during shutdown.
func closeRuntime(context.Context) error {
	state.Lock()
	client := state.client
	state.client = nil
	state.resolver = nil
	state.handle = dynamobind.Handle{}
	state.Unlock()
	if client == nil {
		return nil
	}
	return client.Close()
}
