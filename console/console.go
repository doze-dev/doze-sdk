// Package console is how a module describes its console: rows, columns and a
// handful of blocks, written once and published with the module.
//
// There is deliberately little here. A module chooses what to show and the
// host decides how it looks, so every console is like the rest and nobody
// configures one. The blocks are Table, Stat, Detail, Stream, Actions, Note
// and Chart; Row, Col and Tab arrange them; Extend adds to the generic screen
// of an instance.
//
//	spec := engine.ConsoleSpec{
//		Panels: []engine.Panel{ ... },          // the data
//		Tabs: []engine.Tab{console.Tab("activity", "activity", console.Col(
//			console.Stat("activity"),
//			console.Row(
//				console.Table("sessions").Weight(2),
//				console.Col(console.Detail("sessions").From("sessions"), console.Actions()),
//			))),
//		},
//		Extend: []engine.Extension{console.After("vitals", console.Table("databases"))},
//	}
package console

import "github.com/doze-dev/doze-sdk/engine"

// Block is a node under construction. Its methods return it, so a block is
// one expression.
type Block struct{ n engine.Node }

// Node is the finished node.
func (b Block) Node() engine.Node { return b.n }

// Weight sets the share of a row or column this block takes. The default is 1.
func (b Block) Weight(w int) Block { b.n.Weight = w; return b }

// Title overrides the block's title, which is its panel's.
func (b Block) Title(t string) Block { b.n.Title = t; return b }

// ID names the block, for another block to follow. It defaults to the kind and
// the panel.
func (b Block) ID(id string) Block { b.n.ID = id; return b }

// From makes a detail or a stream follow the selected row of the table block
// named by id or by its panel.
func (b Block) From(table string) Block { b.n.From = table; return b }

func block(kind, panel string) Block { return Block{engine.Node{Kind: kind, Panel: panel}} }

// Table draws the rows of a panel, one selectable.
func Table(panel string) Block { return block("table", panel) }

// Stat draws one row of a panel as labelled numbers.
func Stat(panel string) Block { return block("stat", panel) }

// Detail draws one row of a panel as key and value; follow a table with From.
func Detail(panel string) Block { return block("detail", panel) }

// Stream draws the lines of a stream panel as they arrive; follow a table's
// selection with From.
func Stream(panel string) Block { return block("stream", panel) }

// Chart draws a panel's numbers as a series.
func Chart(panel string) Block { return block("chart", panel) }

// Actions draws the module's actions, or only the ones named.
func Actions(ids ...string) Block {
	b := block("actions", "")
	b.n.Actions = ids
	return b
}

// Note is a sentence of explanation.
func Note(text string) Block {
	b := block("note", "")
	b.n.Text = text
	return b
}

// Row lays its children side by side, by weight.
func Row(children ...Block) Block { return container("row", children) }

// Col stacks its children.
func Col(children ...Block) Block { return container("col", children) }

func container(kind string, children []Block) Block {
	b := Block{engine.Node{Kind: kind}}
	for _, c := range children {
		b.n.Children = append(b.n.Children, c.n)
	}
	return b
}

// Tab is a tab of the console with body as its layout.
func Tab(id, title string, body Block) engine.Tab {
	return engine.Tab{ID: id, Title: title, Body: body.n}
}

// After adds block to the generic overview after the block of it named: one of
// "connect", "awake", "vitals" or "recent".
func After(slot string, block Block) engine.Extension {
	return engine.Extension{After: slot, Node: block.n}
}
