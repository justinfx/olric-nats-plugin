package main

import (
	"github.com/justinfx/olric-nats-plugin/lib"
)

// ServiceDiscovery defines a service discovery plugin
// for Olric, backed by Nats.io.
var ServiceDiscovery lib.NatsDiscovery

func main() {
	_ = ServiceDiscovery
}
