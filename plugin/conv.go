package plugin

import (
	"bytes"
	"encoding/gob"
	"time"

	"github.com/nerdmenot/doze-sdk/engine"
	"github.com/nerdmenot/doze-sdk/plugin/proto"
)

// The engine config (engine.EngineConfig = any) crosses the wire as gob bytes.
// A plugin registers its concrete config type with gob (gob.Register) so the
// value round-trips; core treats the bytes as opaque.

func encodeSpec(spec any) ([]byte, error) {
	if spec == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&spec); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeSpec(b []byte) (any, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var spec any
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&spec); err != nil {
		return nil, err
	}
	return spec, nil
}

func platformToProto(p engine.Platform) *proto.Platform {
	return &proto.Platform{Os: p.OS, Arch: p.Arch, Triple: p.Triple}
}
func platformFromProto(p *proto.Platform) engine.Platform {
	if p == nil {
		return engine.Platform{}
	}
	return engine.Platform{OS: p.Os, Arch: p.Arch, Triple: p.Triple}
}

func toolchainToProto(t engine.Toolchain) *proto.Toolchain {
	return &proto.Toolchain{Engine: t.Engine, Full: t.Full, BinDir: t.BinDir, Tools: t.Tools}
}
func toolchainFromProto(t *proto.Toolchain) engine.Toolchain {
	if t == nil {
		return engine.Toolchain{}
	}
	return engine.Toolchain{Engine: t.Engine, Full: t.Full, BinDir: t.BinDir, Tools: t.Tools}
}

func pinToProto(p engine.Pin) *proto.Pin {
	return &proto.Pin{Resolved: p.Resolved, Source: p.Source, Hashes: p.Hashes}
}
func pinFromProto(p *proto.Pin) engine.Pin {
	if p == nil {
		return engine.Pin{}
	}
	return engine.Pin{Resolved: p.Resolved, Source: p.Source, Hashes: p.Hashes}
}

func lockEntriesToProto(es []engine.LockEntry) []*proto.LockEntry {
	if len(es) == 0 {
		return nil
	}
	out := make([]*proto.LockEntry, 0, len(es))
	for _, e := range es {
		out = append(out, &proto.LockEntry{Engine: e.Engine, Spec: string(e.Spec), Pin: pinToProto(e.Pin)})
	}
	return out
}

func lockEntriesFromProto(es []*proto.LockEntry) []engine.LockEntry {
	if len(es) == 0 {
		return nil
	}
	out := make([]engine.LockEntry, 0, len(es))
	for _, e := range es {
		out = append(out, engine.LockEntry{Engine: e.Engine, Spec: engine.VersionSpec(e.Spec), Pin: pinFromProto(e.Pin)})
	}
	return out
}

func endpointToProto(e engine.Endpoint) *proto.Endpoint {
	return &proto.Endpoint{UnixSocket: e.UnixSocket, TcpAddr: e.TCPAddr, Backend: e.Backend}
}
func endpointFromProto(e *proto.Endpoint) engine.Endpoint {
	if e == nil {
		return engine.Endpoint{}
	}
	return engine.Endpoint{UnixSocket: e.UnixSocket, TCPAddr: e.TcpAddr, Backend: e.Backend}
}

func instanceToProto(inst engine.Instance) (*proto.Instance, error) {
	// A plugin's config is already serialized (RawSpec) — ship it verbatim;
	// otherwise gob-encode an in-tree value.
	var spec []byte
	if rs, ok := inst.Spec.(*RawSpec); ok {
		spec = rs.Bytes
	} else {
		b, err := encodeSpec(inst.Spec)
		if err != nil {
			return nil, err
		}
		spec = b
	}
	deps := map[string]*proto.Dep{}
	for k, d := range inst.Deps {
		deps[k] = &proto.Dep{Name: d.Name, Engine: d.Engine, SocketDir: d.SocketDir, Backend: d.Backend, Url: d.URL}
	}
	return &proto.Instance{
		Name: inst.Name, Type: inst.Type, Version: inst.Version.String(),
		DataDir: inst.DataDir, SocketDir: inst.SocketDir, Port: int32(inst.Port),
		Endpoint: endpointToProto(inst.Endpoint), Spec: spec,
		Deps: deps, InjectedEnv: inst.InjectedEnv,
	}, nil
}

func instanceFromProto(p *proto.Instance) (engine.Instance, error) {
	if p == nil {
		return engine.Instance{}, nil
	}
	spec, err := decodeSpec(p.Spec)
	if err != nil {
		return engine.Instance{}, err
	}
	deps := map[string]engine.Dep{}
	for k, d := range p.Deps {
		deps[k] = engine.Dep{Name: d.Name, Engine: d.Engine, SocketDir: d.SocketDir, Backend: d.Backend, URL: d.Url}
	}
	return engine.Instance{
		Name: p.Name, Type: p.Type, Version: engine.VersionSpec(p.Version),
		DataDir: p.DataDir, SocketDir: p.SocketDir, Port: int(p.Port),
		Endpoint: endpointFromProto(p.Endpoint), Spec: spec,
		Deps: deps, InjectedEnv: p.InjectedEnv,
	}, nil
}

func readyToProto(r *engine.Ready) *proto.Ready {
	if r == nil {
		return nil
	}
	return &proto.Ready{
		Kind: r.Kind, Target: r.Target,
		IntervalNs: int64(r.Interval), TimeoutNs: int64(r.Timeout), Retries: int32(r.Retries),
	}
}
func readyFromProto(r *proto.Ready) *engine.Ready {
	if r == nil {
		return nil
	}
	return &engine.Ready{
		Kind: r.Kind, Target: r.Target,
		Interval: time.Duration(r.IntervalNs), Timeout: time.Duration(r.TimeoutNs), Retries: int(r.Retries),
	}
}

func planToProto(plan engine.SpawnPlan) *proto.SpawnPlan {
	out := &proto.SpawnPlan{}
	for _, s := range plan.Specs {
		out.Specs = append(out.Specs, &proto.SpawnSpec{
			Name: s.Name, Dir: s.Dir, Bin: s.Bin, Args: s.Args, Env: s.Env,
			Tree: s.Tree, After: s.After, Ready: readyToProto(s.Ready), Hooks: s.Hooks,
		})
	}
	return out
}
func planFromProto(p *proto.SpawnPlan) engine.SpawnPlan {
	var plan engine.SpawnPlan
	if p == nil {
		return plan
	}
	for _, s := range p.Specs {
		plan.Specs = append(plan.Specs, engine.SpawnSpec{
			Name: s.Name, Dir: s.Dir, Bin: s.Bin, Args: s.Args, Env: s.Env,
			Tree: s.Tree, After: s.After, Ready: readyFromProto(s.Ready), Hooks: s.Hooks,
		})
	}
	return plan
}

func objectsToProto(objs []engine.Object) []*proto.Object {
	var out []*proto.Object
	for _, o := range objs {
		out = append(out, &proto.Object{Kind: o.Kind, Name: o.Name, Hash: o.Hash})
	}
	return out
}
func objectsFromProto(ps []*proto.Object) []engine.Object {
	var out []engine.Object
	for _, p := range ps {
		out = append(out, engine.Object{Kind: p.Kind, Name: p.Name, Hash: p.Hash})
	}
	return out
}

func restartToProto(r engine.RestartSpec) *proto.RestartSpec {
	return &proto.RestartSpec{Policy: string(r.Policy), BackoffNs: int64(r.Backoff), MaxRetries: int32(r.MaxRetries)}
}
func restartFromProto(r *proto.RestartSpec) engine.RestartSpec {
	if r == nil {
		return engine.RestartSpec{Policy: engine.RestartNo}
	}
	return engine.RestartSpec{Policy: engine.RestartPolicy(r.Policy), Backoff: time.Duration(r.BackoffNs), MaxRetries: int(r.MaxRetries)}
}
