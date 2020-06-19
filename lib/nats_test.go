package lib

import (
	"bytes"
	"io/ioutil"
	"log"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
)

func TestPayload(t *testing.T) {
	table := []struct {
		Name   string
		Input  Payload
		Expect string
	}{
		{Name: "empty", Input: Payload{}, Expect: ":0"},
		{Name: "host", Input: Payload{Host: "localhost"}, Expect: "localhost:0"},
		{Name: "port", Input: Payload{Port: 12345}, Expect: ":12345"},
		{Name: "full", Input: Payload{Host: "myhost", Port: 12345}, Expect: "myhost:12345"},
	}

	for _, tt := range table {
		t.Run(tt.Name, func(t *testing.T) {
			actual := tt.Input.Address()
			if actual != tt.Expect {
				t.Fatalf("expected %q, got %q", tt.Expect, actual)
			}
		})
	}
}

func TestCheckErrors(t *testing.T) {
	table := []struct {
		Name    string
		Input   NatsDiscovery
		WantErr bool
	}{
		{
			Name:    "zero",
			Input:   NatsDiscovery{},
			WantErr: true,
		},
		{
			Name:    "missing config",
			Input:   NatsDiscovery{log: log.New(&bytes.Buffer{}, "", 0)},
			WantErr: true,
		},
		{
			Name:    "missing logger",
			Input:   NatsDiscovery{Config: &Config{}},
			WantErr: true,
		},
		{
			Name: "ok",
			Input: NatsDiscovery{
				Config: &Config{},
				log:    log.New(&bytes.Buffer{}, "", 0),
			},
			WantErr: false,
		},
	}

	for _, tt := range table {
		t.Run(tt.Name, func(t *testing.T) {
			err := tt.Input.checkErrors()
			if err == nil && tt.WantErr {
				t.Fatal("unexpected nil error")
			}
			if err != nil && !tt.WantErr {
				t.Fatalf("unxpected error: %v", err)
			}
		})
	}
}

func startNatsServer(t *testing.T) (addr string) {
	serv, err := server.NewServer(&server.Options{
		Host: "127.0.0.1",
		Port: -1,
	})
	checkFatal(t, err)

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("Embedded nats-io server failed to start")
	}
	t.Cleanup(serv.Shutdown)
	return serv.Addr().String()
}

func TestInitialize(t *testing.T) {
	addr := startNatsServer(t)

	doTest := func(t *testing.T, sd *NatsDiscovery) {
		t.Helper()

		sd.SetLogger(log.New(ioutil.Discard, "", 0))

		checkFatal(t, sd.Initialize())
		checkFatal(t, sd.conn.Conn.Publish("foo", []byte(`bar`)))

		if sd.conn.Conn.Opts.Name != sd.Config.Payload.ClientName {
			t.Fatalf("expected ClientName %q, got %q",
				sd.Config.Payload.ClientName, sd.conn.Conn.Opts.Name)
		}

		checkFatal(t, sd.Close())
	}

	t.Run("config struct", func(t *testing.T) {
		sd := NatsDiscovery{
			Config: &Config{
				Url: addr,
				Payload: Payload{
					Host:       "localhost",
					Port:       12345,
					ClientName: "test-client-1",
				},
			},
		}
		doTest(t, &sd)

		if sd.Config.Provider != DefaultProvider {
			t.Fatalf("expected provider %q, got %q",
				DefaultProvider, sd.Config.Provider)
		}
		if sd.Config.Subject != DefaultDiscoverySubject {
			t.Fatalf("expected subject %q, got %q",
				DefaultDiscoverySubject, sd.Config.Subject)
		}
		if sd.Config.DiscoveryTimeout != DefaultDiscoveryTimeout {
			t.Fatalf("expected timeout %q, got %q",
				DefaultDiscoveryTimeout, sd.Config.DiscoveryTimeout)
		}
	})

	t.Run("config map", func(t *testing.T) {
		var sd NatsDiscovery

		expectTimeout := 10 * time.Millisecond
		expectProvider := "custom"
		expectSubject := "foo-bar-baz"

		err := sd.SetConfig(MapConfig{
			"provider":         expectProvider,
			"servers":          []string{"bad_host:12334", addr},
			"subject":          expectSubject,
			"discoveryTimeout": expectTimeout.String(),
			"payload": MapConfig{
				"host":       "localhost",
				"port":       12346,
				"clientName": "test-client-2",
			},
		})
		checkFatal(t, err)
		doTest(t, &sd)

		if sd.Config.Provider != expectProvider {
			t.Fatalf("expected provider %q, got %q",
				expectProvider, sd.Config.Provider)
		}
		if sd.Config.Subject != expectSubject {
			t.Fatalf("expected subject %q, got %q",
				expectSubject, sd.Config.Subject)
		}
		if sd.Config.DiscoveryTimeout != expectTimeout {
			t.Fatalf("expected timeout %q, got %q",
				expectTimeout, sd.Config.DiscoveryTimeout)
		}
	})

	t.Run("config reset", func(t *testing.T) {
		expect := "addr:1234"
		sd := NatsDiscovery{Config: &Config{Url: expect}}
		if err := sd.SetConfig(MapConfig{}); err != nil {
			t.Fatal(err)
		}
		if sd.Config.Url != expect {
			t.Fatalf("expected %q, got %q", expect, sd.Config.Url)
		}

		if err := sd.SetConfig(MapConfig{"subject": "foo"}); err != nil {
			t.Fatal(err)
		}
		if sd.Config.Url != expect {
			t.Fatalf("expected %q, got %q", expect, sd.Config.Url)
		}
		expect = "foo"
		if sd.Config.Subject != expect {
			t.Fatalf("expectec %q, got %q", expect, sd.Config.Subject)
		}
	})
}

func TestDiscover(t *testing.T) {
	addr := startNatsServer(t)

	createClient := func(t *testing.T, port int) *NatsDiscovery {
		t.Helper()

		sd := &NatsDiscovery{
			Config: &Config{
				Url:              addr,
				DiscoveryTimeout: 5 * time.Millisecond,
				Payload: Payload{
					Host: "localhost",
					Port: port,
				},
			},
		}
		sd.SetLogger(log.New(ioutil.Discard, "", 0))

		if err := sd.Initialize(); err != nil {
			t.Fatalf("failed to start client on port %d: %v", port, err)
		}

		t.Cleanup(func() { sd.Close() })
		return sd
	}

	// Start two clients and check that nothing is initially discoverable
	client1 := createClient(t, 12345)
	client2 := createClient(t, 12346)

	peers, err := client1.DiscoverPeers()
	checkFatal(t, err)
	if len(peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(peers))
	}
	peers, err = client2.DiscoverPeers()
	checkFatal(t, err)
	if len(peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(peers))
	}

	// if other client isn't registered, we still can't see it
	checkFatal(t, client2.Register())
	peers, err = client2.DiscoverPeers()
	checkFatal(t, err)
	if len(peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(peers))
	}

	checkFatal(t, client1.Register())

	// Now that we have both clients registered, we should
	// be able to discover each one
	peers, err = client1.DiscoverPeers()
	checkFatal(t, err)
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0] != client2.Config.Payload.Address() {
		t.Fatalf("expected peer %q, got %q",
			client2.Config.Payload.Address(), peers[0])
	}

	peers, err = client2.DiscoverPeers()
	checkFatal(t, err)
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0] != client1.Config.Payload.Address() {
		t.Fatalf("expected peer %q, got %q",
			client1.Config.Payload.Address(), peers[0])
	}

	// One of the peers should deregister, but can still
	// discover the other peers
	checkFatal(t, client2.Deregister())
	peers, err = client2.DiscoverPeers()
	checkFatal(t, err)
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0] != client1.Config.Payload.Address() {
		t.Fatalf("expected peer %q, got %q",
			client1.Config.Payload.Address(), peers[0])
	}

	// But the other peer should no longer see the deregistered peer
	peers, err = client1.DiscoverPeers()
	checkFatal(t, err)
	if len(peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(peers))
	}

	// other deregister
	checkFatal(t, client1.Deregister())
	peers, err = client2.DiscoverPeers()
	checkFatal(t, err)
	if len(peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(peers))
	}
}

func checkFatal(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
