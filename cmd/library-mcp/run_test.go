package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The MCP handshake a stdio client sends before any tools/call: an initialize
// request, then the notifications/initialized acknowledgement. Newline-delimited
// per the stdio wire framing.
const (
	initializeMsg  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test-client","version":"1"}}}`
	initializedMsg = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
)

// TestMain lets a test re-exec this binary as the real server: when the gate
// env var is set it runs main() (flags come from os.Args) and exits, otherwise
// it runs the normal test suite. This is how TestServer_CleanEOFExitsZero
// observes the actual process exit code.
func TestMain(m *testing.M) {
	if os.Getenv("LIBRARY_MCP_RUN_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestServer_CleanEOFExitsZero is the regression for the stdin-EOF shutdown: an
// MCP client stops a stdio server by closing its stdin, and that clean EOF must
// exit 0, not be treated as fatal. It drives the real binary through the
// initialize handshake, then closes stdin, and asserts the process exits 0.
func TestServer_CleanEOFExitsZero(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	cmd := exec.Command(exe, "-db", filepath.Join(t.TempDir(), "eof.db"))
	cmd.Env = append(os.Environ(), "LIBRARY_MCP_RUN_MAIN=1")
	cmd.Stdout = io.Discard // drain responses so the server's writes never block
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Complete the mandatory handshake, then close stdin to signal a clean
	// client-side shutdown (EOF).
	if _, err := fmt.Fprintf(stdin, "%s\n%s\n", initializeMsg, initializedMsg); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clean EOF should exit 0, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("server did not exit within 10s of stdin close")
	}
}

// TestRun_CleanEOFReturnsNil drives run() in-process over an IOTransport: it
// feeds the handshake, then closes the reader to deliver EOF, and asserts run
// returns nil (which is what makes main exit 0). This covers run's serve loop
// without spawning a process.
func TestRun_CleanEOFReturnsNil(t *testing.T) {
	clientToServerR, clientToServerW := io.Pipe()
	serverToClientR, serverToClientW := io.Pipe()
	go func() { _, _ = io.Copy(io.Discard, serverToClientR) }() // drain server output

	transport := &mcp.IOTransport{Reader: clientToServerR, Writer: serverToClientW}
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), []string{"-db", filepath.Join(t.TempDir(), "run.db")}, transport)
	}()

	// io.Pipe writes block until the server consumes them, so once these return
	// the handshake has been read; closing then delivers a clean EOF.
	if _, err := fmt.Fprintf(clientToServerW, "%s\n%s\n", initializeMsg, initializedMsg); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	if err := clientToServerW.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run should return nil on clean EOF, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return within 10s of EOF")
	}
}

// TestRun_OpenError checks that a database that cannot be opened surfaces as a
// non-nil error (which main turns into a non-zero exit), not a swallowed clean
// shutdown. The db path sits under a regular file, so the parent is not a
// directory and the open fails.
func TestRun_OpenError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	err := run(context.Background(), []string{"-db", filepath.Join(blocker, "sub.db")}, &mcp.StdioTransport{})
	if err == nil {
		t.Fatal("expected an open error, got nil")
	}
}

// TestRun_FlagParseError checks that an unknown flag is reported, not swallowed.
func TestRun_FlagParseError(t *testing.T) {
	if err := run(context.Background(), []string{"-nope"}, &mcp.StdioTransport{}); err == nil {
		t.Fatal("expected a flag parse error, got nil")
	}
}

// TestIsCleanShutdown classifies the shutdown signals: nil and the SDK's
// "server is closing" (code -32004) and bare io.EOF are clean; a genuine
// transport error is not.
func TestIsCleanShutdown(t *testing.T) {
	closing := fmt.Errorf("wrapped: %w", &jsonrpc.Error{Code: codeServerClosing, Message: "server is closing"})
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"server closing wrapped", closing, true},
		{"bare EOF", io.EOF, true},
		{"eof chain", fmt.Errorf("read: %w", io.EOF), true},
		{"genuine transport error", errors.New("connection reset"), false},
		{"other wire error", &jsonrpc.Error{Code: -32600, Message: "invalid request"}, false},
	}
	for _, c := range cases {
		if got := isCleanShutdown(c.err); got != c.want {
			t.Errorf("%s: isCleanShutdown(%v) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}
