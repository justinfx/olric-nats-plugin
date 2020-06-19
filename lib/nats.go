/*
A service discovery library for use with Olric distributed cache.
Provides Nats.io service discovery for use with a standalone, embedded, or clustered v2 nats-server.
*/
package lib

import (
	"fmt"
	"log"
	"net"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/nats-io/nats.go"
)

const (
	DefaultProvider = `nats`

	// The default nats subject to use when communicating
	// with other available nodes
	DefaultDiscoverySubject = `olric-service-discovery`

	// How long we should wait to collect as many results as possible
	DefaultDiscoveryTimeout = 50 * time.Millisecond
)

// MapConfig is a map-based configuration that can be
// passed to SetConfig
type MapConfig = map[string]interface{}

// Configuration options to use when creating a client
// connection to a nats server, and sending service
// discovery payloads
type Config struct {
	// Optional provider name: default "nats"
	Provider string

	// The nats://host:port of a nats server
	Url string

	// Optionally specify a list of nats://host:port
	// endpoints in a cluster for redundancy
	Servers []string

	// Override the default nats subject channel to use
	// for all service discovery communication in this olric cluster.
	// Customizing allows for multiple olric clusters to communicate
	// over a single nats server cluster
	Subject string

	// Override the max time to wait for a response from nodes,
	// when discovering peers.
	// If your nodes have a large amount of latency between each
	// other, you may want to adjust this higher than DefaultDiscoveryTimeout
	// to wait longer for replies
	DiscoveryTimeout time.Duration

	// The payload to use when delivering messages to peers
	Payload Payload
}

// Payload contains the user-configurable information about
// the client that should be made available to the rest of
// the peers during service discovery
type Payload struct {
	// The host name representing this client node
	Host string
	// The port number of the client
	Port int
	// An optional client name for logging
	ClientName string
}

// Address returns the host:port string
func (p *Payload) Address() string {
	return net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
}

// payloadType describes the service discovery message type
// to determine proper handling
type payloadType int

const (
	registerType payloadType = iota
	unregisterType
	discoverReqType
	discoverRepType
)

// discoveryMessage is the message containing all types
// of service discovery event types
type discoveryMessage struct {
	Type    payloadType
	Payload Payload
}

// NatsDiscovery is the ServiceDiscovery client interface
type NatsDiscovery struct {
	Config *Config

	conn *nats.EncodedConn
	subs []*nats.Subscription
	log  *log.Logger
}

func (n *NatsDiscovery) checkErrors() error {
	if n.Config == nil {
		return fmt.Errorf("Config cannot be nil")
	}
	if n.log == nil {
		return fmt.Errorf("logger cannot be nil")
	}
	return nil
}

// Initialize the plugin: registers some
// internal data structures, clients etc.
func (n *NatsDiscovery) Initialize() error {
	if err := n.Close(); err != nil {
		return err
	}

	if n.Config.Provider == "" {
		n.Config.Provider = DefaultProvider
	}

	if n.Config.Subject == "" {
		n.Config.Subject = DefaultDiscoverySubject
	}

	if n.Config.DiscoveryTimeout <= 0 {
		n.Config.DiscoveryTimeout = DefaultDiscoveryTimeout
	}

	if n.log == nil {
		n.log = log.New(os.Stderr, " [olric-nats-plugin] ",
			log.LstdFlags|log.Lmsgprefix)
	}

	if err := n.checkErrors(); err != nil {
		return err
	}
	n.log.Printf("[INFO] Service discovery plugin is enabled, provider: %s",
		n.Config.Provider)

	opts := nats.GetDefaultOptions()
	opts.Url = n.Config.Url
	opts.Servers = n.Config.Servers
	opts.Name = n.Config.Payload.ClientName
	opts.ReconnectWait = 2 * time.Second
	opts.MaxReconnect = -1 // Keep trying forever

	var (
		conn *nats.Conn
		err error
	)
	const N = 3
	for i := 1;;i++ {
		conn, err = opts.Connect()
		if err != nil {
			if i <= N {
				n.log.Printf("[WARN] nats server connection failure %d/%d; " +
					"retrying in %s", i, N, opts.ReconnectWait)
				time.Sleep(opts.ReconnectWait)
				continue
			}
			return fmt.Errorf("nats server connection failure: %w", err)
		}
		break

	}

	enc, err := nats.NewEncodedConn(conn, nats.JSON_ENCODER)
	if err != nil {
		return fmt.Errorf("nats server connection failure: %w", err)
	}

	n.conn = enc
	return nil
}

// SetConfig registers plugin configuration
func (n *NatsDiscovery) SetConfig(c MapConfig) error {
	if n.Config == nil {
		n.Config = &Config{}
	}

	if len(c) == 0 {
		return nil
	}

	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: func(src, tgt reflect.Type, val interface{}) (interface{}, error) {
			if !tgt.AssignableTo(reflect.TypeOf(time.Duration(0))) {
				return val, nil
			}
			if src.Kind() != reflect.String {
				return val, nil
			}
			return time.ParseDuration(val.(string))
		},
		Result: n.Config,
	})
	if err != nil {
		return err
	}
	return dec.Decode(c)
}

// SetLogger sets a custom logger instance
func (n *NatsDiscovery) SetLogger(l *log.Logger) {
	n.log = l
}

// Register this node to a service discovery directory.
func (n *NatsDiscovery) Register() error {
	if err := n.checkErrors(); err != nil {
		return err
	}

	payload := n.Config.Payload

	handler := func(subj, reply string, msg *discoveryMessage) {
		switch msg.Type {

		case discoverReqType:
			// A peer is asking us to identify
			err := n.conn.Publish(reply, &discoveryMessage{
				Type:    discoverRepType,
				Payload: payload,
			})
			if err != nil {
				n.log.Printf("[INFO] %s service discovery request "+
					"failed during replying: %v", n.Config.Provider, err)
			}

		case registerType:
			n.log.Printf("[INFO] %s peer registered: %+v",
				n.Config.Provider, msg.Payload)

		case unregisterType:
			n.log.Printf("[INFO] %s peer unregistered: %+v",
				n.Config.Provider, msg.Payload)
		}
	}

	// Start listening to incoming messages
	sub, err := n.conn.Subscribe(n.Config.Subject, handler)
	if err != nil {
		return fmt.Errorf("%s subject subscription failure: %w",
			n.Config.Provider, err)
	}

	n.subs = append(n.subs, sub)
	return nil
}

// Deregister this node from a service discovery directory.
func (n *NatsDiscovery) Deregister() error {
	if err := n.checkErrors(); err != nil {
		return err
	}

	// Stop listening to incoming messages
	var err error
	for _, sub := range n.subs {
		err = sub.Unsubscribe()
		if err != nil {
			n.log.Printf("[ERROR] %s subject unsubscription failure: %v",
				n.Config.Provider, err)
		}
	}
	// return the last error
	if err != nil {
		return err
	}

	// Send out the notification that we are leaving
	err = n.conn.Publish(n.Config.Subject, &discoveryMessage{
		Type:    unregisterType,
		Payload: n.Config.Payload,
	})

	return err
}

// DiscoverPeers returns a list of known Olric nodes.
func (n *NatsDiscovery) DiscoverPeers() ([]string, error) {
	n.log.Println("DiscoverPeers()")
	if err := n.checkErrors(); err != nil {
		return nil, err
	}

	e := n.Config.Provider + " service discovery failed to make discovery request"

	// Set up a reply channel, then broadcast for all peers to
	// report their presence.
	// Collect as many responses as possible in the given timeout.
	reply := nats.NewInbox()
	recv := make(chan *discoveryMessage)

	sub, err := n.conn.BindRecvChan(reply, recv)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", e, err)
	}

	err = n.conn.PublishRequest(n.Config.Subject, reply, &discoveryMessage{
		Type:    discoverReqType,
		Payload: n.Config.Payload,
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %v", e, err)
	}

	var peers []string

	timeout := time.After(n.Config.DiscoveryTimeout)
	me := n.Config.Payload.Address()

	// Loop until we hit our timeout
	for {
		select {
		case m, ok := <-recv:
			if !ok {
				// Subscription is closed
				return peers, nil
			}
			if m.Payload.Address() == me {
				// Ignore a reply from outself
				continue
			}
			peers = append(peers, m.Payload.Address())

		case <-timeout:
			sub.Unsubscribe()
			close(recv)
			// Next loop will return
		}
	}
}

// Close stops underlying goroutines, if there is any.
// It should be a blocking call.
func (n *NatsDiscovery) Close() error {
	if n.conn != nil {
		defer n.conn.Close()

		for _, sub := range n.subs {
			sub.Unsubscribe()
		}

		if err := n.conn.Flush(); err != nil {
			return fmt.Errorf("%s service discovery connection failed to "+
				"flush data before closing: %w", n.Config.Provider, err)
		}
		n.conn = nil
	}
	return nil
}
