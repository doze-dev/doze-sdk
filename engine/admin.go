package engine

import "context"

// Admin is implemented by the built-in service engines (s3, sqs, sns) to expose
// their sub-resources and the runtime *data* operations the dashboard and CLI can
// run on them without an external AWS client — purge a queue, empty a bucket,
// publish a test message, peek at messages. It covers data, not structure:
// creating and deleting the resources themselves stays with apply/destroy
// (Converger/Inventory/Pruner). Optional capability interface, discovered by a
// type assertion like the others.
//
// Implementations talk to the instance's running backend over ep.Backend, reusing
// the same unix-socket HTTP path the Convergers use.
type Admin interface {
	// Resources lists the instance's sub-resources, each with a compact one-line
	// status (queue depth, bucket object count/size, topic subscription count).
	Resources(ctx context.Context, inst Instance, ep Endpoint) ([]Resource, error)
	// Actions are the data operations this engine offers. The dash binds them to
	// keys and the CLI to subcommands; the set is static per engine.
	Actions() []Action
	// Run performs an action on a named resource and returns a short, possibly
	// multi-line, human-readable result (e.g. "purged emails" or peeked message
	// bodies). input is non-empty only for actions whose InputHint is set.
	Run(ctx context.Context, inst Instance, ep Endpoint, action, resource, input string) (string, error)
}

// Resource is one sub-resource of a built-in service instance.
type Resource struct {
	Kind   string            // "queue" | "bucket" | "topic"
	Name   string            // resource name
	Status string            // compact metric line for a list ("3 msgs · 1 in-flight")
	Info   map[string]string // optional extra fields for a detail view
}

// Action is a runtime data operation an Admin engine offers for a resource kind.
type Action struct {
	ID          string // stable id passed to Run ("purge", "empty", "peek", …)
	Label       string // human label ("Purge", "Empty", "Peek")
	Kind        string // resource kind it applies to ("queue" | "bucket" | "topic")
	Destructive bool   // the dash/CLI should confirm before running
	InputHint   string // when set, the caller prompts for input with this hint
}
