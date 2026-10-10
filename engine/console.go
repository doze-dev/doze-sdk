package engine

import "context"

// Console is implemented by a module that describes how its running instance
// should be looked at. The module returns data; the host draws it, so every
// console looks and behaves like the rest of the dash and the dash holds no
// knowledge of any one module.
//
// It builds on Admin: a table panel is the rows of Resources of one kind, and
// the actions on a row are Admin's Actions. What Console adds is how to lay
// those out, a way to stream, and where a web console lives.
//
// Optional capability interface, discovered by a type assertion like the
// others. A module without it keeps working: the host shows the overview.
type Console interface {
	// DescribeConsole says what panels the console has. It is static per module
	// version, and the host caches it.
	DescribeConsole() ConsoleSpec

	// Tail streams rows for a stream panel until ctx ends. selector is the
	// name of the row selected in the table panel the stream follows (the
	// topic whose messages to tail), or "" for a stream with no table.
	Tail(ctx context.Context, inst Instance, ep Endpoint, panel, selector string, out chan<- Row) error
}

// ConsoleSpec is a module's description of its console.
type ConsoleSpec struct {
	// Front is the tab the console opens on: "console", "web", or "" for the
	// host's default for the kind.
	Front string
	// WebURL is where the module serves a web console, as a template over the
	// instance's address: "http://{address}/_console".
	WebURL string
	Panels []Panel
}

// Panel is one block of the console.
type Panel struct {
	ID, Title string
	// Kind is "table", "counters" or "stream".
	Kind string
	// Source, for a table or counters, is the Resource.Kind it lists.
	Source  string
	Columns []Column
	// Select, for a stream, is the ID of the table panel whose selected row
	// picks what to tail.
	Select string
}

// Column is one column of a table, one field of a stream row.
type Column struct {
	// Key is "name", "status", or a key of Resource.Info (or of Row.Cells).
	Key, Title string
	// Align is "left" or "right".
	Align string
	// Role lets the host format and colour without knowing the module: "",
	// "count", "rate", "lag", "state", "time" or "code". A "rate" column's Key
	// names an Info key that holds a running total; the host shows how fast it
	// is growing.
	Role string
}

// Row is one line of a stream.
type Row struct {
	Cells map[string]string
	// Tone colours the row: "", "ok", "warn" or "bad".
	Tone string
}
