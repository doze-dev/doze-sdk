package plugin

import (
	"sync/atomic"
)

// A Reporter hears about a plugin call that failed where the engine interface
// has no error to return (BackendSocket, ConnString, Provisioned, Attributes,
// actions, objects, the console and the lifecycle answers). Those calls fall back to an empty answer; without a reporter the
// failure would leave no trace.
type Reporter func(engineType, call string, err error)

var reporter atomic.Pointer[Reporter]

// SetReporter installs the host's Reporter. The host should log what it hears:
// an empty connection string or socket path is otherwise a mystery. There is
// one per process, and it is called from whatever goroutine made the call, so
// it must be safe to call concurrently. Calls whose capability the module did
// not advertise never reach it.
func SetReporter(r Reporter) {
	if r == nil {
		reporter.Store(nil)
		return
	}
	reporter.Store(&r)
}

// report tells the host's Reporter that call failed for this driver.
func (d *pluginDriver) report(call string, err error) {
	if r := reporter.Load(); r != nil && err != nil {
		(*r)(d.engineType, call, err)
	}
}
