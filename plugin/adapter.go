package plugin

import (
	"context"
	"errors"
	"sync"

	"github.com/zclconf/go-cty/cty"
	"google.golang.org/grpc/status"

	"github.com/doze-dev/doze-sdk/engine"
	"github.com/doze-dev/doze-sdk/plugin/proto"
)

// userError strips the gRPC transport framing ("rpc error: code = Unknown
// desc = …") from an error a plugin returned, so a config-validation message
// reads as the plugin wrote it. Non-status errors pass through unchanged.
func userError(err error) error {
	if err == nil {
		return nil
	}
	if s, ok := status.FromError(err); ok && s.Message() != "" {
		return errors.New(s.Message())
	}
	return err
}

var _ engine.RemoteDecoder = (*pluginDriver)(nil)

// pluginDriver adapts a plugin's Engine gRPC client back to the in-tree
// engine.Driver + capability interfaces. It implements every optional capability
// method but no-ops the ones the plugin did not advertise, so the runtime's
// type-assertion dispatch keeps working while only advertised work crosses the
// wire. Compile-time: it is a Driver and a Spawner (every engine plugin runs via a
// SpawnPlan).
var (
	_ engine.Driver  = (*pluginDriver)(nil)
	_ engine.Spawner = (*pluginDriver)(nil)
)

type pluginDriver struct {
	client     proto.EngineClient
	engineType string
	caps       map[string]bool

	wireOnce sync.Once
	wireSock string // the plugin's fd-handoff socket, resolved once via WireAddr
}

func newPluginDriver(c proto.EngineClient) engine.Driver {
	d := &pluginDriver{client: c, caps: map[string]bool{}}
	ctx := context.Background()
	if resp, err := c.Type(ctx, &proto.Empty{}); err == nil {
		d.engineType = resp.Type
	}
	if resp, err := c.Capabilities(ctx, &proto.Empty{}); err == nil {
		for _, cp := range resp.Capabilities {
			d.caps[cp] = true
		}
	}
	// Versionless, Templater, Admin, and the convergence trio (Converger+Inventory+
	// Pruner) change behaviour by interface *presence* (the runtime type-asserts for
	// them), so they can't be no-op methods on the base driver — every plugin would
	// falsely claim them, and a falsely-claimed Converger silently skips structure.
	// Wrap to add exactly the advertised set. The wrappers embed *pluginDriver
	// (concrete) so all other capability assertions (Spawner, ProxyFilter, Lifecycle,
	// …) still resolve. The builtins (s3/sqs/sns) are versionless + admin + structural
	// and postgres is templater + structural, so the combinations are real; compose
	// them by embedding so each wrapper's method set is the union.
	return wrapCaps(d)
}

// wrapCaps returns d decorated with exactly the presence-discovered capabilities it
// advertised. The convergence trio is keyed on capConverger and added as a unit
// (the three co-occur in every structural engine); objects/prune RPCs are guarded
// server-side for the rare converger-only plugin.
func wrapCaps(d *pluginDriver) engine.Driver {
	v, t, a := d.caps[capVersionless], d.caps[capTemplater], d.caps[capAdmin]
	s := d.caps[capConverger]
	if !s {
		switch {
		case v && t && a:
			return versionlessTemplaterAdminDriver{templaterAdminDriver{adminDriver{d}}}
		case v && t:
			return versionlessTemplaterDriver{d}
		case v && a:
			return versionlessAdminDriver{adminDriver{d}}
		case t && a:
			return templaterAdminDriver{adminDriver{d}}
		case v:
			return versionlessDriver{d}
		case t:
			return templaterDriver{d}
		case a:
			return adminDriver{d}
		}
		return d
	}
	st := structural{d}
	switch {
	case v && t && a:
		return versionlessTemplaterAdminStructuralDriver{versionlessTemplaterAdminDriver{templaterAdminDriver{adminDriver{d}}}, st}
	case v && t:
		return versionlessTemplaterStructuralDriver{versionlessTemplaterDriver{d}, st}
	case v && a:
		return versionlessAdminStructuralDriver{versionlessAdminDriver{adminDriver{d}}, st}
	case t && a:
		return templaterAdminStructuralDriver{templaterAdminDriver{adminDriver{d}}, st}
	case v:
		return versionlessStructuralDriver{versionlessDriver{d}, st}
	case t:
		return templaterStructuralDriver{templaterDriver{d}, st}
	case a:
		return adminStructuralDriver{adminDriver{d}, st}
	}
	return structuralDriver{d, st}
}

// structural adds the convergence trio (engine.Converger/Inventory/Pruner) to a
// plugin that advertised capConverger. It holds d as a plain field (not embedded)
// so combining it with the versionless/templater/admin wrappers — which embed
// *pluginDriver — never produces an ambiguous selector for the base methods.
type structural struct{ d *pluginDriver }

func (s structural) Converge(ctx context.Context, inst engine.Instance, tc engine.Toolchain, ep engine.Endpoint) error {
	return s.d.converge(ctx, inst, tc, ep)
}
func (s structural) Objects(inst engine.Instance) []engine.Object { return s.d.objects(inst) }
func (s structural) Prune(ctx context.Context, inst engine.Instance, tc engine.Toolchain, ep engine.Endpoint, removed []engine.Object) error {
	return s.d.prune(ctx, inst, tc, ep, removed)
}

var (
	_ engine.Converger = structural{}
	_ engine.Inventory = structural{}
	_ engine.Pruner    = structural{}
)

// The eight structural variants pair the convergence trio with every subset of the
// versionless/templater/admin presence-capabilities. Each embeds the matching v/t/a
// wrapper (which embeds *pluginDriver, so Driver/Spawner/etc. promote through) plus
// structural (which adds the trio).
type structuralDriver struct {
	*pluginDriver
	structural
}
type versionlessStructuralDriver struct {
	versionlessDriver
	structural
}
type templaterStructuralDriver struct {
	templaterDriver
	structural
}
type adminStructuralDriver struct {
	adminDriver
	structural
}
type versionlessTemplaterStructuralDriver struct {
	versionlessTemplaterDriver
	structural
}
type versionlessAdminStructuralDriver struct {
	versionlessAdminDriver
	structural
}
type templaterAdminStructuralDriver struct {
	templaterAdminDriver
	structural
}
type versionlessTemplaterAdminStructuralDriver struct {
	versionlessTemplaterAdminDriver
	structural
}

// adminDriver adds engine.Admin to a plugin driver that advertised it, so the
// runtime's type-assertion (drv.(engine.Admin)) only succeeds for engines that
// actually expose data operations.
type adminDriver struct{ *pluginDriver }

func (a adminDriver) Actions() []engine.Action { return a.pluginDriver.actions() }
func (a adminDriver) Resources(ctx context.Context, inst engine.Instance, ep engine.Endpoint) ([]engine.Resource, error) {
	return a.pluginDriver.resources(ctx, inst, ep)
}
func (a adminDriver) Run(ctx context.Context, inst engine.Instance, ep engine.Endpoint, action, resource, input string) (string, error) {
	return a.pluginDriver.runAction(ctx, inst, ep, action, resource, input)
}

// versionlessAdminDriver / templaterAdminDriver / versionlessTemplaterAdminDriver
// compose admin with the other presence-capabilities by embedding, so the method
// set is the union (the s3/sqs/sns builtins are versionless + admin).
type versionlessAdminDriver struct{ adminDriver }

func (versionlessAdminDriver) Versionless() {}

type templaterAdminDriver struct{ adminDriver }

func (t templaterAdminDriver) EnsureTemplate(ctx context.Context, tc engine.Toolchain, templateDir string) error {
	return t.pluginDriver.ensureTemplate(ctx, tc, templateDir)
}
func (t templaterAdminDriver) CloneTemplate(ctx context.Context, templateDir, destDir string) error {
	return t.pluginDriver.cloneTemplate(ctx, templateDir, destDir)
}

type versionlessTemplaterAdminDriver struct{ templaterAdminDriver }

func (versionlessTemplaterAdminDriver) Versionless() {}

// versionlessDriver adds engine.Versionless to a plugin driver that advertised it
// (embedding keeps every other Driver/Spawner/capability method).
type versionlessDriver struct{ *pluginDriver }

func (versionlessDriver) Versionless() {}

// templaterDriver adds engine.Templater to a plugin driver that advertised it.
type templaterDriver struct{ *pluginDriver }

func (t templaterDriver) EnsureTemplate(ctx context.Context, tc engine.Toolchain, templateDir string) error {
	return t.pluginDriver.ensureTemplate(ctx, tc, templateDir)
}
func (t templaterDriver) CloneTemplate(ctx context.Context, templateDir, destDir string) error {
	return t.pluginDriver.cloneTemplate(ctx, templateDir, destDir)
}

// versionlessTemplaterDriver advertises both presence-sensitive capabilities.
type versionlessTemplaterDriver struct{ *pluginDriver }

func (versionlessTemplaterDriver) Versionless() {}
func (t versionlessTemplaterDriver) EnsureTemplate(ctx context.Context, tc engine.Toolchain, templateDir string) error {
	return t.pluginDriver.ensureTemplate(ctx, tc, templateDir)
}
func (t versionlessTemplaterDriver) CloneTemplate(ctx context.Context, templateDir, destDir string) error {
	return t.pluginDriver.cloneTemplate(ctx, templateDir, destDir)
}

func (d *pluginDriver) has(cap string) bool { return d.caps[cap] }

// ── engine.Driver ────────────────────────────────────────────────────────────
func (d *pluginDriver) Type() string { return d.engineType }

func (d *pluginDriver) Resolve(ctx context.Context, spec engine.VersionSpec, plat engine.Platform, lk engine.Locker, _ engine.Fetcher) (engine.Toolchain, error) {
	// Ship the whole lock so the plugin can read pins for every component binary
	// it resolves (a composite pins several); fall back to just this engine's pin
	// if the Locker can't enumerate.
	var locked []engine.LockEntry
	if ll, ok := lk.(engine.LockLister); ok {
		locked = ll.Entries()
	} else if pin, ok := lk.Get(d.engineType, spec, plat); ok {
		locked = []engine.LockEntry{{Engine: d.engineType, Spec: spec, Pin: pin}}
	}
	resp, err := d.client.Resolve(ctx, &proto.ResolveRequest{
		Spec: string(spec), Platform: platformToProto(plat), Locked: lockEntriesToProto(locked),
	})
	if err != nil {
		return engine.Toolchain{}, err
	}
	// Record each pin the plugin reported under its own (engine, spec) key, so a
	// composite's components don't collapse into one and core's doze.lock is exact.
	for _, e := range lockEntriesFromProto(resp.Recorded) {
		lk.Record(e.Engine, e.Spec, plat, e.Pin)
	}
	return toolchainFromProto(resp.Toolchain), nil
}

func (d *pluginDriver) Provision(ctx context.Context, inst engine.Instance, tc engine.Toolchain) error {
	pi, err := instanceToProto(inst)
	if err != nil {
		return err
	}
	_, err = d.client.Provision(ctx, &proto.ProvisionRequest{Instance: pi, Toolchain: toolchainToProto(tc)})
	return userError(err)
}

func (d *pluginDriver) Provisioned(dataDir string) bool {
	resp, err := d.client.Provisioned(context.Background(), &proto.ProvisionedRequest{DataDir: dataDir})
	return err == nil && resp.Provisioned
}

func (d *pluginDriver) BackendSocket(socketDir string, port int) string {
	resp, err := d.client.BackendSocket(context.Background(), &proto.BackendSocketRequest{SocketDir: socketDir, Port: int32(port)})
	if err != nil {
		return ""
	}
	return resp.Path
}

func (d *pluginDriver) ConnString(inst engine.Instance, ep engine.Endpoint) (string, string) {
	pi, err := instanceToProto(inst)
	if err != nil {
		return "", ""
	}
	resp, err := d.client.ConnString(context.Background(), &proto.ConnStringRequest{Instance: pi, Endpoint: endpointToProto(ep)})
	if err != nil {
		return "", ""
	}
	return resp.EnvVar, resp.Url
}

// DecodeRemote sends the block's source file + flattened variables + declared
// engine version to the plugin, which decodes its own config and returns it as
// opaque gob bytes (a RawSpec).
func (d *pluginDriver) DecodeRemote(file []byte, blockType, blockLabel string, vars map[string]cty.Value, baseDir string, version engine.VersionSpec) (any, error) {
	vj, err := varsToJSON(vars)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.DecodeConfig(context.Background(), &proto.DecodeRequest{
		File: file, BlockType: blockType, BlockLabel: blockLabel, Variables: vj, BaseDir: baseDir,
		EngineVersion: string(version),
	})
	if err != nil {
		return nil, userError(err)
	}
	return &RawSpec{Bytes: resp.Spec}, nil
}

// ── engine.Spawner ───────────────────────────────────────────────────────────
func (d *pluginDriver) Plan(ctx context.Context, inst engine.Instance, tc engine.Toolchain) (engine.SpawnPlan, error) {
	pi, err := instanceToProto(inst)
	if err != nil {
		return engine.SpawnPlan{}, err
	}
	resp, err := d.client.Plan(ctx, &proto.PlanRequest{Instance: pi, Toolchain: toolchainToProto(tc)})
	if err != nil {
		return engine.SpawnPlan{}, userError(err)
	}
	return planFromProto(resp), nil
}

// ── engine.Templater (exposed only via the templater wrappers) ───────────────
func (d *pluginDriver) ensureTemplate(ctx context.Context, tc engine.Toolchain, templateDir string) error {
	_, err := d.client.EnsureTemplate(ctx, &proto.EnsureTemplateRequest{Toolchain: toolchainToProto(tc), TemplateDir: templateDir})
	return userError(err)
}
func (d *pluginDriver) cloneTemplate(ctx context.Context, templateDir, destDir string) error {
	_, err := d.client.CloneTemplate(ctx, &proto.CloneTemplateRequest{TemplateDir: templateDir, DestDir: destDir})
	return userError(err)
}

// ── optional capabilities ────────────────────────────────────────────────────
// converge/objects/prune forward the convergence capabilities over gRPC. They are
// reached only via the structural wrappers, installed only when capConverger was
// advertised (the three travel as a unit), so no per-call capability guard is
// needed — mirroring actions/resources/runAction for engine.Admin.
func (d *pluginDriver) converge(ctx context.Context, inst engine.Instance, tc engine.Toolchain, ep engine.Endpoint) error {
	pi, err := instanceToProto(inst)
	if err != nil {
		return err
	}
	_, err = d.client.Converge(ctx, &proto.ConvergeRequest{Instance: pi, Toolchain: toolchainToProto(tc), Endpoint: endpointToProto(ep)})
	return userError(err)
}

// actions/resources/runAction forward the engine.Admin capability over gRPC. They
// are reached only via adminDriver, which is installed only when capAdmin was
// advertised, so no extra capability guard is needed here.
func (d *pluginDriver) actions() []engine.Action {
	resp, err := d.client.Actions(context.Background(), &proto.Empty{})
	if err != nil {
		return nil
	}
	return actionsFromProto(resp.Actions)
}

func (d *pluginDriver) resources(ctx context.Context, inst engine.Instance, ep engine.Endpoint) ([]engine.Resource, error) {
	pi, err := instanceToProto(inst)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Resources(ctx, &proto.ResourcesRequest{Instance: pi, Endpoint: endpointToProto(ep)})
	if err != nil {
		return nil, userError(err)
	}
	return resourcesFromProto(resp.Resources), nil
}

func (d *pluginDriver) runAction(ctx context.Context, inst engine.Instance, ep engine.Endpoint, action, resource, input string) (string, error) {
	pi, err := instanceToProto(inst)
	if err != nil {
		return "", err
	}
	resp, err := d.client.RunAction(ctx, &proto.RunActionRequest{Instance: pi, Endpoint: endpointToProto(ep), Action: action, Resource: resource, Input: input})
	if err != nil {
		return "", userError(err)
	}
	return resp.Result, nil
}

func (d *pluginDriver) Attributes(inst engine.Instance, ep engine.Endpoint) map[string]cty.Value {
	if !d.has(capAttributer) {
		return nil
	}
	pi, err := instanceToProto(inst)
	if err != nil {
		return nil
	}
	resp, err := d.client.Attributes(context.Background(), &proto.AttributesRequest{Instance: pi, Endpoint: endpointToProto(ep)})
	if err != nil {
		return nil
	}
	attrs, err := varsFromJSON(resp.Attrs)
	if err != nil {
		return nil
	}
	return attrs
}

func (d *pluginDriver) objects(inst engine.Instance) []engine.Object {
	pi, err := instanceToProto(inst)
	if err != nil {
		return nil
	}
	resp, err := d.client.Objects(context.Background(), &proto.ObjectsRequest{Instance: pi})
	if err != nil {
		return nil
	}
	return objectsFromProto(resp.Objects)
}

func (d *pluginDriver) prune(ctx context.Context, inst engine.Instance, tc engine.Toolchain, ep engine.Endpoint, removed []engine.Object) error {
	pi, err := instanceToProto(inst)
	if err != nil {
		return err
	}
	_, err = d.client.Prune(ctx, &proto.PruneRequest{Instance: pi, Toolchain: toolchainToProto(tc), Endpoint: endpointToProto(ep), Removed: objectsToProto(removed)})
	return userError(err)
}

func (d *pluginDriver) BackendURL(inst engine.Instance) string {
	if !d.has(capBackendURL) {
		return ""
	}
	pi, err := instanceToProto(inst)
	if err != nil {
		return ""
	}
	resp, err := d.client.BackendURL(context.Background(), &proto.BackendURLRequest{Instance: pi})
	if err != nil {
		return ""
	}
	return resp.Url
}

func (d *pluginDriver) Supervised(inst engine.Instance) bool {
	if !d.has(capLifecycle) {
		return false
	}
	pi, err := instanceToProto(inst)
	if err != nil {
		return false
	}
	resp, err := d.client.Supervised(context.Background(), &proto.SupervisedRequest{Instance: pi})
	return err == nil && resp.Supervised
}

func (d *pluginDriver) PreStart(ctx context.Context, inst engine.Instance) error {
	return d.hook(ctx, inst, "pre_start")
}
func (d *pluginDriver) PostStart(ctx context.Context, inst engine.Instance) error {
	return d.hook(ctx, inst, "post_start")
}
func (d *pluginDriver) PreStop(ctx context.Context, inst engine.Instance) error {
	return d.hook(ctx, inst, "pre_stop")
}
func (d *pluginDriver) hook(ctx context.Context, inst engine.Instance, phase string) error {
	if !d.has(capHooked) {
		return nil
	}
	pi, err := instanceToProto(inst)
	if err != nil {
		return err
	}
	_, err = d.client.Hook(ctx, &proto.HookRequest{Instance: pi, Phase: phase})
	return userError(err)
}

func (d *pluginDriver) CheckHealth(ctx context.Context, inst engine.Instance) error {
	if !d.has(capHealth) {
		return nil
	}
	pi, err := instanceToProto(inst)
	if err != nil {
		return err
	}
	_, err = d.client.CheckHealth(ctx, &proto.HealthRequest{Instance: pi})
	return userError(err)
}

func (d *pluginDriver) RestartPolicy(inst engine.Instance) engine.RestartSpec {
	if !d.has(capRestartable) {
		return engine.RestartSpec{Policy: engine.RestartNo}
	}
	pi, err := instanceToProto(inst)
	if err != nil {
		return engine.RestartSpec{Policy: engine.RestartNo}
	}
	resp, err := d.client.RestartPolicy(context.Background(), &proto.RestartPolicyRequest{Instance: pi})
	if err != nil {
		return engine.RestartSpec{Policy: engine.RestartNo}
	}
	return restartFromProto(resp)
}

func (d *pluginDriver) AdvertisedAddr(inst engine.Instance) (string, bool) {
	if !d.has(capPortBinder) {
		return "", false
	}
	pi, err := instanceToProto(inst)
	if err != nil {
		return "", false
	}
	resp, err := d.client.AdvertisedAddr(context.Background(), &proto.AddrRequest{Instance: pi})
	if err != nil {
		return "", false
	}
	return resp.Addr, resp.Ok
}
