package enginetest

import (
	"strings"
	"testing"

	"github.com/doze-dev/doze-sdk/engine"
)

type netDriver struct {
	engine.Driver // nil: only Network is called
	net           func(engine.Instance) engine.Network
}

func (d netDriver) Network(inst engine.Instance) engine.Network { return d.net(inst) }

func TestNetworkProblems(t *testing.T) {
	inst := engine.Instance{Name: "events", Port: 9092}
	good := func(i engine.Instance) engine.Network {
		return engine.Network{Listeners: []engine.Listener{
			{Name: engine.MainListener, Protocol: engine.ProtocolTCP},
			{Name: "console", Port: 9093, Protocol: engine.ProtocolHTTP, Routes: []string{"@"}},
		}}
	}
	if err := networkProblems(netDriver{net: good}, inst); err != nil {
		t.Errorf("a valid, pure network: %v", err)
	}
	if err := networkProblems(struct{ engine.Driver }{}, inst); err != nil {
		t.Errorf("a module without Networked has the default network: %v", err)
	}
	bad := func(engine.Instance) engine.Network {
		return engine.Network{Listeners: []engine.Listener{{Name: engine.MainListener, Port: 1, Protocol: engine.ProtocolTCP, Routes: []string{"@"}}}}
	}
	if err := networkProblems(netDriver{net: bad}, inst); err == nil || !strings.Contains(err.Error(), "only an http listener has routes") {
		t.Errorf("an invalid network: %v", err)
	}
	n := 0
	impure := func(i engine.Instance) engine.Network {
		n++
		return engine.Network{Listeners: []engine.Listener{{Name: engine.MainListener, Protocol: engine.ProtocolTCP}, {Name: "ui", Port: 8000 + n, Protocol: engine.ProtocolHTTP}}}
	}
	if err := networkProblems(netDriver{net: impure}, inst); err == nil || !strings.Contains(err.Error(), "not the same twice") {
		t.Errorf("a network that changes between calls: %v", err)
	}
}
