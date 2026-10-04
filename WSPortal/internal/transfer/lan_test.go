package transfer

import (
	"net"
	"testing"
	"time"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// freeUDPPort reserves an ephemeral UDP port and returns it.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func TestLANTransferRoundTrip(t *testing.T) {
	port := freeUDPPort(t)

	ws := workspace.New("demo", "a captured workspace")
	ws.Environment.Tools = []string{"git"}
	ws.Terminals = []workspace.Terminal{{Name: "server", WorkingDirectory: "${HOME}/p", Command: "npm run dev"}}

	type result struct {
		receipt *Receipt
		err     error
	}
	sent := make(chan result, 1)
	go func() {
		receipt, err := LANSender{
			DiscoveryPort: port,
			BroadcastAddr: "127.0.0.1",
			Timeout:       10 * time.Second,
		}.Send(ws)
		sent <- result{receipt, err}
	}()

	got, err := LANReceiver{DiscoveryPort: port, Timeout: 10 * time.Second}.Receive("")
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	if got.Name() != "demo" {
		t.Errorf("received name = %q, want demo", got.Name())
	}
	if len(got.Terminals) != 1 || got.Terminals[0].Command != "npm run dev" {
		t.Errorf("received terminals = %+v", got.Terminals)
	}

	select {
	case res := <-sent:
		if res.err != nil {
			t.Fatalf("Send: %v", res.err)
		}
		if res.receipt == nil || res.receipt.Mode != ModeLAN {
			t.Errorf("receipt = %+v, want LAN mode", res.receipt)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Send did not return")
	}
}

func TestLANReceiverRejectsCode(t *testing.T) {
	if _, err := (LANReceiver{}).Receive("7K4X-92QP"); err == nil {
		t.Fatal("LAN receive with a code should fail")
	}
}
