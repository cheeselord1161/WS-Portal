package transfer

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// DefaultDiscoveryPort is the UDP port used to announce LAN transfers.
const DefaultDiscoveryPort = 41819

// announcePrefix identifies a WSPortal announcement datagram.
const announcePrefix = "WSPORTAL/1"

// maxPayload caps an incoming workspace payload (16 MiB).
const maxPayload = 16 << 20

// LANSender sends a workspace directly to a receiver on the local network.
//
// It opens a TCP listener, announces itself over UDP, and waits for a receiver
// to connect and present the one-time nonce from the announcement. The
// workspace travels in the clear; by policy a workspace contains no secrets.
type LANSender struct {
	// DiscoveryPort is the UDP port announcements are sent to.
	DiscoveryPort int
	// BroadcastAddr is the address announcements are sent to. It defaults to
	// the broadcast address, but can be a unicast address (used by tests).
	BroadcastAddr string
	// Timeout is how long to wait for a receiver. Defaults to 5 minutes.
	Timeout time.Duration
}

// Send implements Sender. It blocks until a receiver completes the transfer or
// the timeout elapses.
func (s LANSender) Send(ws *workspace.Workspace) (*Receipt, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is nil")
	}

	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	discoveryPort := s.DiscoveryPort
	if discoveryPort == 0 {
		discoveryPort = DefaultDiscoveryPort
	}
	broadcast := strings.TrimSpace(s.BroadcastAddr)
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}

	payload, err := workspace.Encode(ws)
	if err != nil {
		return nil, err
	}
	nonce := newNonce()

	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("listen for receiver: %w", err)
	}
	defer ln.Close()
	tcpPort := ln.Addr().(*net.TCPAddr).Port

	done := make(chan struct{})
	defer close(done)
	go announce(broadcast, discoveryPort, tcpPort, nonce, ws.Name(), done)

	deadline := time.Now().Add(timeout)
	if tcpLn, ok := ln.(*net.TCPListener); ok {
		if err := tcpLn.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set deadline: %w", err)
		}
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil, fmt.Errorf("waiting for receiver: %w", err)
		}
		if sendWorkspace(conn, nonce, payload) {
			conn.Close()
			return &Receipt{Mode: ModeLAN, Code: nonce, ExpiresAt: deadline}, nil
		}
		conn.Close()
	}
}

// announce repeatedly sends the discovery datagram until done is closed.
func announce(broadcast string, port, tcpPort int, nonce, name string, done <-chan struct{}) {
	conn, err := net.Dial("udp4", net.JoinHostPort(broadcast, strconv.Itoa(port)))
	if err != nil {
		return
	}
	defer conn.Close()

	msg := []byte(fmt.Sprintf("%s %d %s %s", announcePrefix, tcpPort, nonce, sanitizeName(name)))
	_, _ = conn.Write(msg)

	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_, _ = conn.Write(msg)
		}
	}
}

// sendWorkspace performs the handshake and writes the payload. It reports
// whether the transfer completed.
func sendWorkspace(conn net.Conn, wantNonce string, payload []byte) bool {
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return false
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return false
	}
	if strings.TrimSpace(line) != wantNonce {
		return false
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := conn.Write(header[:]); err != nil {
		return false
	}
	if _, err := conn.Write(payload); err != nil {
		return false
	}
	return true
}

// LANReceiver receives a workspace sent on the local network. It listens for a
// UDP announcement, connects to the sender, and reads the workspace.
type LANReceiver struct {
	// DiscoveryPort is the UDP port announcements arrive on.
	DiscoveryPort int
	// Timeout is how long to wait for an announcement. Defaults to 2 minutes.
	Timeout time.Duration
}

// Receive implements Receiver. A non-empty code belongs to a remote transfer,
// so LAN receiving rejects it.
func (r LANReceiver) Receive(code string) (*workspace.Workspace, error) {
	if strings.TrimSpace(code) != "" {
		return nil, fmt.Errorf("LAN transfer does not use a code")
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	discoveryPort := r.DiscoveryPort
	if discoveryPort == 0 {
		discoveryPort = DefaultDiscoveryPort
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: discoveryPort})
	if err != nil {
		return nil, fmt.Errorf("listen for announcements: %w", err)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	buf := make([]byte, 2048)
	var senderIP net.IP
	var tcpPort int
	var nonce string
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return nil, fmt.Errorf("waiting for a sender: %w", err)
		}
		port, gotNonce, ok := parseAnnouncement(string(buf[:n]))
		if !ok {
			continue
		}
		senderIP, tcpPort, nonce = addr.IP, port, gotNonce
		break
	}

	dialer := net.Dialer{Timeout: 10 * time.Second}
	tcp, err := dialer.Dial("tcp", net.JoinHostPort(senderIP.String(), strconv.Itoa(tcpPort)))
	if err != nil {
		return nil, fmt.Errorf("connect to sender: %w", err)
	}
	defer tcp.Close()

	if err := tcp.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(tcp, "%s\n", nonce); err != nil {
		return nil, fmt.Errorf("send handshake: %w", err)
	}

	var header [4]byte
	if _, err := io.ReadFull(tcp, header[:]); err != nil {
		return nil, fmt.Errorf("read payload header: %w", err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > maxPayload {
		return nil, fmt.Errorf("invalid payload size %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(tcp, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	return workspace.Decode(bytes.NewReader(payload))
}

// parseAnnouncement extracts the TCP port and nonce from a datagram.
func parseAnnouncement(s string) (port int, nonce string, ok bool) {
	fields := strings.Fields(s)
	if len(fields) < 3 || fields[0] != announcePrefix {
		return 0, "", false
	}
	p, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", false
	}
	return p, fields[2], true
}

// newNonce returns a short random handshake token.
func newNonce() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return strings.ToUpper(hex.EncodeToString(b))
}

// sanitizeName makes a workspace name safe for the space-delimited announcement.
func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "workspace"
	}
	return strings.ReplaceAll(name, " ", "_")
}
