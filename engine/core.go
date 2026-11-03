package engine

// CoreArguments are the arguments of a module block that doze itself reads, so
// a module's own decode never sees them: the meta-arguments (count, for_each,
// depends_on, enabled, after), the common fields (version, listen, port) and
// sleep. The plugin server and enginetest strip these before a driver decodes.
var CoreArguments = []string{
	"count", "for_each", "depends_on", "enabled", "after",
	"version", "listen", "port", "sleep",
}
