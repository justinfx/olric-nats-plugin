# olric-nats-plugin

A plugin / service discovery library for use with [Olric](https://github.com/buraksezer/olric/)
distributed cache.

Provides [Nats.io](https://github.com/nats-io/nats-server/) service discovery
for use with a standalone, embedded, or clustered v2 nats-server.

## Build

Get the code:

```
go get -u github.com/justinfx/olric-nats-plugin
```

With a properly configured Go environment:

```
go build -buildmode=plugin -o olric_nats.so 
```

In order to strip debug symbols:

```
go build -ldflags="-s -w" -buildmode=plugin -o olric_nats.so 
```

## Configuration

If you prefer to deploy Olric in client-server scenario, add a `serviceDiscovery` block to 
your `olricd.yaml`. A sample:

```yaml

serviceDiscovery:
  provider: "nats"
  path: "YOUR_PLUGIN_PATH/olric_nats.so"
  url: "nats://127.0.0.1:4222"
  subject: "optional-nats-channel-name"
  discoveryTimeout: "50ms"
  payload:
    host: "localhost"
    port: MEMBERLIST_PORT
    clientName: "olric-node-1"
```

In embedded member deployment scenario:

```go
// import config package
"github.com/buraksezer/olric/config"

// Get a new Olric config for local environment
c := config.New("local")

// Set service discovery definition
c.ServiceDiscovery = map[string]interface{}{
    "provider": "nats",
    "path": "YOUR_PLUGIN_PATH/olric_nats.so",
    "url": "nats://127.0.0.1:4222",
    // or a list of nats peers
    //"servers": []string{"nats://127.0.0.1:4222"},
    "subject": "optional-nats-channel-name",
    "discoveryTimeout": "50ms",
    "payload": map[string]interface{}{
        "host": "localhost",
        "port": MEMBERLIST_PORT,
        "clientName": "olric-node-1",
    }
}
```

Or you can load the plugin directly as a library:

```go
import (
    olricnats "github.com/justinfx/olric-nats-plugin/lib"
)

//...
sd := make(map[string]interface{})
sd["plugin"] = &NatsDiscovery{
	Config: &Config{
		Provider: "nats",
		Url:      "nats://127.0.0.1:4222",
		Payload: Payload{
			Host: "localhost",
			Port: MEMBERLIST_PORT,
		},
	},
}
//...
```

## License

MIT - see [LICENSE](LICENSE) for more details.
