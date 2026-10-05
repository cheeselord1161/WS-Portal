// Package transfer moves a workspace between machines.
//
// Three modes are planned, all behind the same interfaces so the CLI does not
// need to change as they land:
//
//  1. File    — export/import a .ws file manually. No account, no network.
//  2. LAN     — send/receive directly between machines on the same network.
//  3. Remote  — send/receive across networks using a temporary transfer code,
//     preferring a direct connection, then NAT traversal, then a short-lived
//     encrypted relay.
//
// Only the interfaces and placeholders exist today. No networking is
// implemented.
package transfer

import (
	"fmt"
	"time"

	"github.com/cheeselord1161/WS_Portal/internal/workspace"
)

// Sender sends a workspace to a receiver.
type Sender interface {
	// Send transmits the workspace. It returns a receipt describing how the
	// transfer completed (for example a transfer code).
	Send(ws *workspace.Workspace) (*Receipt, error)
}

// Receiver waits for or fetches an incoming workspace.
type Receiver interface {
	// Receive blocks until a workspace arrives, or until the code/context is
	// exhausted. An empty code means "wait for a LAN transfer".
	Receive(code string) (*workspace.Workspace, error)
}

// Receipt describes the outcome of a send operation.
type Receipt struct {
	// Mode is the transfer mode that was used.
	Mode Mode
	// Code is a short, human-typed transfer code, when the mode uses one.
	Code string
	// ExpiresAt is when a temporary code stops being valid.
	ExpiresAt time.Time
}

// Mode identifies a transfer mechanism.
type Mode string

const (
	// ModeFile is manual export/import of a .ws file.
	ModeFile Mode = "file"
	// ModeLAN is a direct transfer over the local network.
	ModeLAN Mode = "lan"
	// ModeRemote is a transfer across networks via a code.
	ModeRemote Mode = "remote"
)

// ErrNotImplemented is returned by the placeholder implementations.
var ErrNotImplemented = fmt.Errorf("transfer is not implemented yet")

// PlaceholderSender is a Sender that reports transfer as unimplemented.
type PlaceholderSender struct {
	// Mode is the mode this sender would use.
	Mode Mode
}

// Send implements Sender.
func (s PlaceholderSender) Send(*workspace.Workspace) (*Receipt, error) {
	// TODO(transfer): implement LAN discovery and the remote relay client.
	return nil, fmt.Errorf("%s transfer: %w", s.Mode, ErrNotImplemented)
}

// PlaceholderReceiver is a Receiver that reports transfer as unimplemented.
type PlaceholderReceiver struct {
	// Mode is the mode this receiver would use.
	Mode Mode
}

// Receive implements Receiver.
func (r PlaceholderReceiver) Receive(string) (*workspace.Workspace, error) {
	// TODO(transfer): implement LAN discovery and the remote relay client.
	return nil, fmt.Errorf("%s transfer: %w", r.Mode, ErrNotImplemented)
}
