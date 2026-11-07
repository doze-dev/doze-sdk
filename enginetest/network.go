package enginetest

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/doze-dev/doze-sdk/engine"
)

// CheckNetwork fails t unless the module's network for inst is one the host will
// take: valid (engine.ValidateNetwork) and pure (the same on a second call, since
// the host asks without starting anything and expects the answer it will serve).
// A module without engine.Networked has engine.DefaultNetwork, which passes.
// It needs no backend, so it runs in plain `go test`.
func CheckNetwork(t testing.TB, drv engine.Driver, inst engine.Instance) {
	t.Helper()
	if err := networkProblems(drv, inst); err != nil {
		t.Error(err)
	}
}

func networkProblems(drv engine.Driver, inst engine.Instance) error {
	nw, ok := drv.(engine.Networked)
	if !ok {
		return engine.ValidateNetwork(engine.DefaultNetwork())
	}
	first := nw.Network(inst)
	if err := engine.ValidateNetwork(first); err != nil {
		return fmt.Errorf("the network of %q is not one the host will take: %w", inst.Name, err)
	}
	if second := nw.Network(inst); !reflect.DeepEqual(first, second) {
		return fmt.Errorf("the network of %q is not the same twice (%+v, then %+v): it must be a pure function of the instance", inst.Name, first, second)
	}
	return nil
}
