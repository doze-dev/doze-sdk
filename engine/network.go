package engine

import (
	"errors"
	"fmt"
	"strings"
)

// Networked is implemented by a module that serves more than one port, or that
// wants HTTP names without a port. Without it an instance has one tcp listener,
// "main", on its declared port, and nothing else: DefaultNetwork.
//
// A module says WHAT it serves; the host decides WHERE. The host gives every
// instance one name, <instance>.<stack>.doze, on an address of its own, and puts
// every listener's port on that address. A tcp listener is reached at
// name:port, always (plain TCP carries no host to route on). An http listener
// is reached at name:port too, and, for each of its Routes, without a port
// through the host's front door.
//
// The host owns every public port: it listens on each, wakes the instance on a
// connection, and forwards to the listener's backend, a socket the module
// listens on (Grant's Backend). The main listener's backend is the one
// BackendSocket names, as for a module without Networked.
//
// Network must be a pure function of inst (its Name and Spec): the host calls it
// without starting anything, to print endpoints and to check a configuration
// before it runs. What the host actually set up comes back on Instance.Grant.
//
// Optional capability interface, discovered by a type assertion like the
// others.
type Networked interface {
	Network(inst Instance) Network
}

// Network is what an instance serves.
type Network struct {
	// Listeners are the instance's ports. One must be named MainListener: it is
	// the backend the host's proxy splices to, as for a module without
	// Networked.
	Listeners []Listener
}

// MainListener is the name of the listener every instance has.
const MainListener = "main"

// Protocol is what a listener speaks.
type Protocol string

const (
	// ProtocolTCP is any byte stream: a database, a broker. Reached at its port.
	ProtocolTCP Protocol = "tcp"
	// ProtocolHTTP is HTTP: reached at its port, and by name through the front
	// door for each of its routes.
	ProtocolHTTP Protocol = "http"
)

// Listener is one port an instance serves.
type Listener struct {
	// Name identifies the listener within the instance: "main", "console",
	// "registry". A DNS label.
	Name string
	// Port is the port clients use on the instance's address. The main
	// listener's is the instance's declared port (the person's `port`, or the
	// module's default), which the host sets: leave it 0. Every other listener
	// names its own.
	Port int
	// Protocol is what it speaks.
	Protocol Protocol
	// Routes are the names, relative to the instance's own, that reach this
	// listener without a port. HTTP only. Three shapes:
	//
	//	"@"        the instance's name and every name under it:
	//	           aws.shop.doze, bucket.s3.eu-west-1.aws.shop.doze
	//	"registry" one child, and every name under it:
	//	           registry.events.shop.doze
	//	"sync-@"   a sibling: the instance's name with a prefix:
	//	           sync-aws.shop.doze
	//
	// Nothing else: a module cannot ask for a name outside its own.
	Routes []string
}

// DefaultNetwork is the network of an instance whose module does not implement
// Networked: one tcp listener, "main", on the declared port.
func DefaultNetwork() Network {
	return Network{Listeners: []Listener{{Name: MainListener, Protocol: ProtocolTCP}}}
}

// Listener returns the listener with name.
func (n Network) Listener(name string) (Listener, bool) {
	for _, l := range n.Listeners {
		if l.Name == name {
			return l, true
		}
	}
	return Listener{}, false
}

// RouteSelf is the route for the instance's own name.
const RouteSelf = "@"

// RouteHost is the host a route names, given the instance's host
// (aws.shop.doze): "@" is the host, "registry" is registry.<host>, "sync-@" is
// sync-<host>.
func RouteHost(route, host string) (string, error) {
	kind, part, err := parseRoute(route)
	if err != nil {
		return "", err
	}
	switch kind {
	case routeChild:
		return part + "." + host, nil
	case routeSibling:
		return part + host, nil
	}
	return host, nil
}

type routeKind int

const (
	routeSelf routeKind = iota
	routeChild
	routeSibling
)

// parseRoute returns a route's shape and its label (a child) or prefix (a sibling).
func parseRoute(route string) (routeKind, string, error) {
	bad := func(why string) (routeKind, string, error) {
		return 0, "", fmt.Errorf("route %q: %s; a route is %q (the instance's name), a label such as %q (a child), or a prefix followed by @ such as %q (a sibling)",
			route, why, RouteSelf, "registry", "sync-@")
	}
	switch {
	case route == RouteSelf:
		return routeSelf, "", nil
	case strings.HasSuffix(route, "@"):
		prefix := strings.TrimSuffix(route, "@")
		if prefix == "" || !labelChars(prefix) {
			return bad("the prefix must be letters, digits and hyphens")
		}
		return routeSibling, prefix, nil
	case strings.Contains(route, "@"):
		return bad("@ may only end it")
	case !isLabel(route):
		return bad("a child is one DNS label")
	}
	return routeChild, route, nil
}

// labelChars says whether s is made of lower-case letters, digits and hyphens.
func labelChars(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return s != ""
}

// isLabel says whether s is one DNS label: letters, digits and hyphens, not
// starting or ending with a hyphen, at most 63 long.
func isLabel(s string) bool {
	return labelChars(s) && len(s) <= 63 && s[0] != '-' && s[len(s)-1] != '-'
}

// ValidateNetwork checks a network as the host will: a "main" listener, names
// that are DNS labels, ports in range and not shared (the main listener's may
// be 0: the declared port), a known protocol, routes only on http listeners
// and only in the three shapes, and no route given twice. Every problem is
// reported. The host also refuses a listener whose port is the declared one.
func ValidateNetwork(n Network) error {
	var errs []error
	names := map[string]bool{}
	ports := map[int]string{}
	routes := map[string]bool{}
	for _, l := range n.Listeners {
		if !isLabel(l.Name) {
			errs = append(errs, fmt.Errorf("listener name %q: a listener's name is one DNS label, such as %q", l.Name, "console"))
		}
		if names[l.Name] {
			errs = append(errs, fmt.Errorf("two listeners are named %q", l.Name))
		}
		names[l.Name] = true
		if l.Name == MainListener && l.Port == 0 {
			// The declared port, which the host puts here.
		} else if l.Port < 1 || l.Port > 65535 {
			errs = append(errs, fmt.Errorf("listener %q: port %d is not a port (1 to 65535)", l.Name, l.Port))
		} else if other, ok := ports[l.Port]; ok {
			errs = append(errs, fmt.Errorf("listeners %q and %q both use port %d", other, l.Name, l.Port))
		} else {
			ports[l.Port] = l.Name
		}
		switch l.Protocol {
		case ProtocolTCP:
			if len(l.Routes) > 0 {
				errs = append(errs, fmt.Errorf("listener %q is tcp: only an http listener has routes (plain TCP carries no host to route on)", l.Name))
			}
		case ProtocolHTTP:
		default:
			errs = append(errs, fmt.Errorf("listener %q: protocol %q is neither %q nor %q", l.Name, l.Protocol, ProtocolTCP, ProtocolHTTP))
		}
		for _, r := range l.Routes {
			if _, _, err := parseRoute(r); err != nil {
				errs = append(errs, fmt.Errorf("listener %q: %w", l.Name, err))
				continue
			}
			if routes[r] {
				errs = append(errs, fmt.Errorf("route %q is given twice", r))
			}
			routes[r] = true
		}
	}
	if !names[MainListener] {
		errs = append(errs, fmt.Errorf("no listener named %q: it is the one the host's proxy serves", MainListener))
	}
	return errors.Join(errs...)
}

// Grant is what the host set up for an instance, carried on Instance. A host
// that knows Networked fills it for every instance, with names or without; it
// is the zero value only from a host that predates Networked, and then a module
// builds its URLs from Endpoint as before.
type Grant struct {
	// Host is the instance's name, aws.shop.doze, or "" when the host gave no
	// names (names are off, or the machine is not set up for them). Without a
	// name, listeners are reached at addresses and routes are not served.
	Host string
	// Listeners are what each listener got, by listener name.
	Listeners map[string]GrantedListener
}

// GrantedListener is what one listener got.
type GrantedListener struct {
	// Addr is the host:port clients dial: aws.shop.doze:4566, or 127.0.0.1:41233
	// without names.
	Addr string
	// URL is how a person or a client reaches an http listener: without a port
	// when it has a route (http://aws.shop.doze), with one when it does not.
	// Empty for tcp.
	URL string
	// Hosts are the names its routes reach, each covering the names under it:
	// aws.shop.doze, sync-aws.shop.doze. Empty without names.
	Hosts []string
	// Backend is where the module listens for this listener: a unix socket
	// path. The host forwards the listener's connections to it.
	Backend string
}

// Named says whether the host gave the instance a name.
func (g Grant) Named() bool { return g.Host != "" }

// Backend is the socket a listener's backend listens on.
func (g Grant) Backend(listener string) (string, bool) {
	l, ok := g.Listeners[listener]
	if !ok || l.Backend == "" {
		return "", false
	}
	return l.Backend, true
}

// Addr is the address a listener got.
func (g Grant) Addr(listener string) (string, bool) {
	l, ok := g.Listeners[listener]
	if !ok || l.Addr == "" {
		return "", false
	}
	return l.Addr, true
}
