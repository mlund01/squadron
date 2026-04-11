package mcp

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestIsTransportError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"eof", io.EOF, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"context canceled", context.Canceled, true},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"broken pipe message", errors.New("write: broken pipe"), true},
		{"connection refused", errors.New("dial tcp: connection refused"), true},
		{"connection reset", errors.New("read: connection reset by peer"), true},
		{"transport closed", errors.New("transport error: closed"), true},
		{"closed pipe", errors.New("read closed pipe"), true},
		{"plain app error", errors.New("tool failed to find file"), false},
		{"invalid args", errors.New("invalid arguments"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransportError(tc.err); got != tc.want {
				t.Errorf("isTransportError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestEnsureAlive_RestartsAfterDeadInner verifies that a client whose inner
// transport has been torn down will respawn it on ensureAlive. We stage this
// with a bare command that exits immediately — the first Load call fails
// (because the server never implements initialize), but that's fine: we just
// need a Client with a populated spec to drive ensureAlive against.
//
// Since we can't easily stand up a real MCP stdio server in-process, this
// test focuses on the error surface: a respawn of an unusable command
// returns a wrapped "respawn failed" error rather than panicking, which is
// the safety guarantee we care about.
func TestEnsureAlive_RespawnFailureSurface(t *testing.T) {
	c := &Client{
		name: "broken",
		spec: Spec{Command: "/nonexistent/path/to/mcp/server"},
		// inner is nil — ensureAlive should go straight to bringUpTransport,
		// which will fail because the command doesn't exist.
	}

	err := c.ensureAlive()
	if err == nil {
		t.Fatal("expected error from ensureAlive with bogus command")
	}
	msg := err.Error()
	for _, needle := range []string{"mcp", "broken", "respawn failed"} {
		if !strings.Contains(msg, needle) {
			t.Errorf("error %q should contain %q", msg, needle)
		}
	}
}

// TestAlive_NilInner confirms that alive() on a freshly-constructed Client
// (inner not yet set) returns false instead of panicking. This matters
// because ensureAlive is allowed to leave inner nil if a respawn fails, and
// a subsequent ensureAlive call must be able to handle that state.
func TestAlive_NilInner(t *testing.T) {
	c := &Client{name: "x"}
	if c.alive() {
		t.Error("alive() on client with nil inner should return false")
	}
}

