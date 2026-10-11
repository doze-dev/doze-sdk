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
