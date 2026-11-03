package plugin

import (
	"errors"
	"testing"
)

// A failure that has no error to return still reaches the host's Reporter,
// with the module and the call that failed.
func TestReporterHearsSwallowedFailures(t *testing.T) {
	var got []string
	SetReporter(func(engineType, call string, err error) {
		got = append(got, engineType+" "+call+": "+err.Error())
	})
	t.Cleanup(func() { SetReporter(nil) })
	d := &pluginDriver{engineType: "postgres"}
	d.report("ConnString", errors.New("rpc error: connection refused"))
	d.report("BackendSocket", nil) // not a failure: nothing to say
	if len(got) != 1 || got[0] != "postgres ConnString: rpc error: connection refused" {
		t.Fatalf("reported %q", got)
	}
}
