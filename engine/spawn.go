package engine

import (
	"context"
	"time"
)

// SpawnPlan is the declarative description a driver returns (via Spawner) instead
// of spawning OS processes itself. Core's runtime executes and supervises it: it
// starts each spec in dependency order, gates each on its readiness probe, runs any
// post-ready hooks, then supervises them as one unit (a crash in any spec tears the
// rest down). Most engines return a single spec; composites like documentdb return
// several (Postgres → a CREATE EXTENSION hook → FerretDB). This is the contract the
// out-of-process plugin protocol serializes — the plugin describes how to run, core
// owns the (hardened) supervision.
type SpawnPlan struct {
	Specs []SpawnSpec
}

// SpawnSpec is one process within a plan.
type SpawnSpec struct {
	Name  string   // logical name (logs/ordering), unique within the plan
	Dir   string   // working directory ("" inherits)
	Bin   string   // executable path (absolute, from the Toolchain) or "sh"
	Args  []string // command arguments
	Env   []string // full child environment (KEY=VALUE); nil inherits os.Environ
	Tree  bool     // kill the whole process group on stop (StartTree)
	After []string // names of specs that must be ready before this one starts
	Ready *Ready   // readiness gate; nil means "stayed alive briefly" (liveness)
	Hooks []string // sh -c commands run (in Dir, with Env) after this spec is ready
}

// Ready describes how core decides a spec has become ready.
type Ready struct {
	Kind     string        // "http" | "tcp" | "exec" | "log_line" | "socket"
	Target   string        // url / host:port / command / regex / socket path
	Interval time.Duration // poll interval (defaulted by core if zero)
	Timeout  time.Duration // per-probe timeout (defaulted by core if zero)
	Retries  int           // readiness budget = Interval × Retries (defaulted if zero)
}

// Spawner is implemented by engines that describe how to run via a SpawnPlan — core
// executes and supervises it — rather than spawning a Process themselves. When a
// driver implements Spawner, the runtime uses it in preference to Spawn/WaitReady.
type Spawner interface {
	Plan(ctx context.Context, inst Instance, tc Toolchain) (SpawnPlan, error)
}
