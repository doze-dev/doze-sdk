package plugin

import (
	"reflect"
	"testing"

	"github.com/doze-dev/doze-sdk/engine"
)

func TestAConsoleLayoutSurvivesTheWire(t *testing.T) {
	in := engine.ConsoleSpec{
		Front:  "overview",
		Panels: []engine.Panel{{ID: "sessions", Title: "sessions", Kind: "table", Source: "session", Columns: []engine.Column{{Key: "pid", Title: "pid", Align: "right"}}}},
		Tabs: []engine.Tab{{ID: "activity", Title: "activity", Body: engine.Node{Kind: "row", Children: []engine.Node{
			{Kind: "table", Panel: "sessions", Weight: 2},
			{Kind: "detail", Panel: "sessions", From: "sessions", Title: "query"},
			{Kind: "actions", Actions: []string{"cancel"}},
		}}}},
		Extend: []engine.Extension{{After: "vitals", Node: engine.Node{Kind: "table", Panel: "databases"}}},
	}
	out := consoleSpecFromProto(consoleSpecToProto(in))
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip changed the spec:\n in  %+v\n out %+v", in, out)
	}
}

// A module's Admin methods get the toolchain on the instance they are handed,
// so a postgres module can run the psql it converged with.
func TestTheToolchainTravelsOnTheInstance(t *testing.T) {
	in := engine.Instance{Name: "db", Type: "postgres",
		Toolchain: engine.Toolchain{Engine: "postgres", Full: "17.2.0", BinDir: "/cache/postgres/17/bin", Tools: map[string]string{"psql": "/cache/psql"}}}
	p, err := instanceToProto(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := instanceFromProto(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in.Toolchain, out.Toolchain) {
		t.Errorf("toolchain = %+v, want %+v", out.Toolchain, in.Toolchain)
	}
	if got := out.Toolchain.Path("psql"); got != "/cache/psql" {
		t.Errorf("psql = %q", got)
	}
}
