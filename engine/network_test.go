package engine

import (
	"strings"
	"testing"
)

func TestTheDefaultNetworkIsOneTCPListener(t *testing.T) {
	n := DefaultNetwork()
	if len(n.Listeners) != 1 {
		t.Fatalf("listeners = %+v", n.Listeners)
	}
	l := n.Listeners[0]
	if l.Name != MainListener || l.Port != 0 || l.Protocol != ProtocolTCP || len(l.Routes) != 0 {
		t.Errorf("default listener = %+v", l)
	}
	if err := ValidateNetwork(n); err != nil {
		t.Errorf("the default is not valid: %v", err)
	}
}

func TestValidateNetwork(t *testing.T) {
	main := Listener{Name: MainListener, Protocol: ProtocolTCP}
	http := func(name string, port int, routes ...string) Listener {
		return Listener{Name: name, Port: port, Protocol: ProtocolHTTP, Routes: routes}
	}
	for _, c := range []struct {
		name string
		n    Network
		want string // "" = valid; otherwise a fragment of the error
	}{
		{"kafka", Network{Listeners: []Listener{main, http("console", 9093, "@"), http("registry", 8081, "registry")}}, ""},
		{"aws", Network{Listeners: []Listener{{Name: MainListener, Protocol: ProtocolHTTP, Routes: []string{"@", "sync-@"}}}}, ""},
		{"an http listener reached on its port only", Network{Listeners: []Listener{main, http("ui", 8025)}}, ""},
		{"empty", Network{}, `no listener named "main"`},
		{"no main", Network{Listeners: []Listener{http("ui", 80, "@")}}, `no listener named "main"`},
		{"two with one name", Network{Listeners: []Listener{main, {Name: MainListener, Protocol: ProtocolTCP}}}, `two listeners are named "main"`},
		{"two on one port", Network{Listeners: []Listener{main, http("ui", 9093), http("api", 9093)}}, "both use port 9093"},
		{"main with no port: the declared one", Network{Listeners: []Listener{{Name: MainListener, Protocol: ProtocolTCP}}}, ""},
		{"a port out of range", Network{Listeners: []Listener{main, http("ui", 70000)}}, "port 70000"},
		{"another listener with no port", Network{Listeners: []Listener{main, http("ui", 0)}}, "port 0"},
		{"an unknown protocol", Network{Listeners: []Listener{{Name: MainListener, Protocol: "udp"}}}, `protocol "udp"`},
		{"routes on tcp", Network{Listeners: []Listener{{Name: MainListener, Protocol: ProtocolTCP, Routes: []string{"@"}}}}, "only an http listener has routes"},
		{"a bad listener name", Network{Listeners: []Listener{main, http("Web UI", 8080)}}, `listener name "Web UI"`},
		{"a route that is a full name", Network{Listeners: []Listener{main, http("ui", 80, "ui.shop.doze")}}, `route "ui.shop.doze"`},
		{"a wildcard route", Network{Listeners: []Listener{main, http("ui", 80, "*")}}, `route "*"`},
		{"an empty prefix", Network{Listeners: []Listener{main, http("ui", 80, "@@")}}, `route "@@"`},
		{"a suffix after the name", Network{Listeners: []Listener{main, http("ui", 80, "@-x")}}, `route "@-x"`},
		{"one route twice", Network{Listeners: []Listener{main, http("ui", 80, "@"), http("api", 81, "@")}}, `route "@" is given twice`},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateNetwork(c.n)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case c.want != "" && err == nil:
				t.Errorf("accepted, want an error with %q", c.want)
			case c.want != "" && !strings.Contains(err.Error(), c.want):
				t.Errorf("error %q, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestRouteHost(t *testing.T) {
	for _, c := range []struct{ route, host, want string }{
		{"@", "aws.shop.doze", "aws.shop.doze"},
		{"registry", "events.shop.doze", "registry.events.shop.doze"},
		{"sync-@", "aws.shop.doze", "sync-aws.shop.doze"},
	} {
		if got, err := RouteHost(c.route, c.host); err != nil || got != c.want {
			t.Errorf("RouteHost(%q, %q) = %q, %v; want %q", c.route, c.host, got, err, c.want)
		}
	}
	if _, err := RouteHost("*", "aws.shop.doze"); err == nil {
		t.Error("an invalid route gave a host")
	}
}

func TestAGrantSaysWhatAListenerGot(t *testing.T) {
	g := Grant{Host: "events.shop.doze", Listeners: map[string]GrantedListener{
		MainListener: {Addr: "events.shop.doze:9092", Backend: "/s/kafka.sock"},
		"console":    {Addr: "events.shop.doze:9093", URL: "http://events.shop.doze", Hosts: []string{"events.shop.doze"}, Backend: "/s/console.sock"},
	}}
	if a, ok := g.Addr(MainListener); !ok || a != "events.shop.doze:9092" {
		t.Errorf("Addr(main) = %q, %v", a, ok)
	}
	if _, ok := g.Addr("registry"); ok {
		t.Error("a listener not granted has an address")
	}
	if b, ok := g.Backend("console"); !ok || b != "/s/console.sock" {
		t.Errorf("Backend(console) = %q, %v", b, ok)
	}
	if !g.Named() || (Grant{}).Named() {
		t.Error("Named should say whether the instance has a name")
	}
	// Without names a listener still has an address and a backend.
	off := Grant{Listeners: map[string]GrantedListener{MainListener: {Addr: "127.0.0.1:9092", Backend: "/s/kafka.sock"}}}
	if off.Named() {
		t.Error("a grant with no host is not named")
	}
	if a, ok := off.Addr(MainListener); !ok || a != "127.0.0.1:9092" {
		t.Errorf("without names, Addr(main) = %q, %v", a, ok)
	}
}
