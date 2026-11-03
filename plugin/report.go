package plugin

import (
	"sync/atomic"
)

// A Reporter hears about a plugin call that failed where the engine interface
// has no error to return (BackendSocket, ConnString, Provisioned, Attributes,
// actions). Those calls fall back to an empty answer; without a reporter the
// failure would leave no trace.
type Reporter func(engineType, call string, err error)

var reporter atomic.Pointer[Reporter]

// SetReporter installs the host's Reporter. The host should log what it hears:
// an empty connection string or socket path is otherwise a mystery.
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
