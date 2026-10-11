package console_test

import (
	"reflect"
	"testing"

	"github.com/doze-dev/doze-sdk/console"
	"github.com/doze-dev/doze-sdk/engine"
)

func TestBlocksComposeTheLayoutTheBoardsShow(t *testing.T) {
	tab := console.Tab("activity", "activity", console.Col(
		console.Stat("activity"),
		console.Row(
			console.Table("sessions").Weight(2),
			console.Col(console.Detail("sessions").From("sessions").Title("query"), console.Actions("cancel")),
		)))
	want := engine.Tab{ID: "activity", Title: "activity", Body: engine.Node{Kind: "col", Children: []engine.Node{
		{Kind: "stat", Panel: "activity"},
		{Kind: "row", Children: []engine.Node{
			{Kind: "table", Panel: "sessions", Weight: 2},
			{Kind: "col", Children: []engine.Node{
				{Kind: "detail", Panel: "sessions", From: "sessions", Title: "query"},
				{Kind: "actions", Actions: []string{"cancel"}},
			}},
		}},
	}}}
	if !reflect.DeepEqual(tab, want) {
		t.Errorf("tab = %+v\nwant  %+v", tab, want)
	}
	ext := console.After("vitals", console.Table("databases"))
	if ext.After != "vitals" || ext.Node.Kind != "table" || ext.Node.Panel != "databases" {
		t.Errorf("extension = %+v", ext)
	}
}
