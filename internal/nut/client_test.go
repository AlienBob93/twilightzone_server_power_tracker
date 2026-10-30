package nut

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeServer struct {
	listener net.Listener
	mu       sync.Mutex
	commands []string
}

func newFakeServer(t *testing.T, handler func(command string, conn net.Conn)) (*fakeServer, *Client) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	server := &fakeServer{listener: listener}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					command := strings.TrimRight(line, "\r\n")
					server.mu.Lock()
					server.commands = append(server.commands, command)
					server.mu.Unlock()
					handler(command, conn)
				}
			}(conn)
		}
	}()

	host, portString, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split address: %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	return server, New(host, port, "", "", 2*time.Second)
}

func (s *fakeServer) commandSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func TestListUPS(t *testing.T) {
	_, client := newFakeServer(t, func(command string, conn net.Conn) {
		if command != "LIST UPS" {
			fmt.Fprint(conn, "ERR UNKNOWN-COMMAND\n")
			return
		}
		fmt.Fprint(conn, "UPS beta \"Second UPS\"\n")
		fmt.Fprint(conn, "UPS alpha \"First \\\"UPS\\\"\"\n")
		fmt.Fprint(conn, "END LIST UPS\n")
	})

	upses, err := client.ListUPS(context.Background())
	if err != nil {
		t.Fatalf("ListUPS: %v", err)
	}
	if len(upses) != 2 {
		t.Fatalf("got %d UPSes", len(upses))
	}
	if upses[0].Name != "alpha" || upses[0].Description != `First "UPS"` {
		t.Errorf("first = %+v", upses[0])
	}
	if upses[1].Name != "beta" || upses[1].Description != "Second UPS" {
		t.Errorf("second = %+v", upses[1])
	}
}

func TestListVars(t *testing.T) {
	_, client := newFakeServer(t, func(command string, conn net.Conn) {
		if command != "LIST VAR test" {
			fmt.Fprint(conn, "ERR UNKNOWN-COMMAND\n")
			return
		}
		fmt.Fprint(conn, "VAR test input.voltage \"230.1\"\n")
		fmt.Fprint(conn, "VAR test ups.status \"OL CHRG\"\n")
		fmt.Fprint(conn, "VAR test device.model \"EC850LCD \\\"Pro\\\"\"\n")
		fmt.Fprint(conn, "END LIST VAR test\n")
	})

	vars, err := client.ListVars(context.Background(), "test")
	if err != nil {
		t.Fatalf("ListVars: %v", err)
	}
	if vars["input.voltage"] != "230.1" {
		t.Errorf("input.voltage = %q", vars["input.voltage"])
	}
	if vars["ups.status"] != "OL CHRG" {
		t.Errorf("ups.status = %q", vars["ups.status"])
	}
	if vars["device.model"] != `EC850LCD "Pro"` {
		t.Errorf("device.model = %q", vars["device.model"])
	}
}

func TestGetVarNotSupported(t *testing.T) {
	_, client := newFakeServer(t, func(command string, conn net.Conn) {
		fmt.Fprint(conn, "ERR VAR-NOT-SUPPORTED\n")
	})

	_, err := client.GetVar(context.Background(), "test", "ups.temperature")
	if !errors.Is(err, ErrVarNotSupported) {
		t.Fatalf("err = %v, want ErrVarNotSupported", err)
	}
}

func TestAuthentication(t *testing.T) {
	server, client := newFakeServer(t, func(command string, conn net.Conn) {
		switch command {
		case "USERNAME monitor", "PASSWORD secret", "LOGIN test":
			fmt.Fprint(conn, "OK\n")
		case "LIST VAR test":
			fmt.Fprint(conn, "VAR test ups.status \"OL\"\n")
			fmt.Fprint(conn, "END LIST VAR test\n")
		default:
			fmt.Fprint(conn, "ERR ACCESS-DENIED\n")
		}
	})
	client.Username = "monitor"
	client.Password = "secret"

	vars, err := client.ListVars(context.Background(), "test")
	if err != nil {
		t.Fatalf("ListVars: %v", err)
	}
	if vars["ups.status"] != "OL" {
		t.Errorf("ups.status = %q", vars["ups.status"])
	}

	commands := server.commandSnapshot()
	want := []string{"USERNAME monitor", "PASSWORD secret", "LOGIN test", "LIST VAR test"}
	if len(commands) < len(want) {
		t.Fatalf("commands = %v", commands)
	}
	for i, command := range want {
		if commands[i] != command {
			t.Errorf("command %d = %q, want %q", i, commands[i], command)
		}
	}
}

func TestServerError(t *testing.T) {
	_, client := newFakeServer(t, func(command string, conn net.Conn) {
		fmt.Fprint(conn, "ERR ACCESS-DENIED\n")
	})

	_, err := client.ListVars(context.Background(), "test")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "ACCESS-DENIED") {
		t.Errorf("err = %v", err)
	}
}

func TestContextDeadline(t *testing.T) {
	_, client := newFakeServer(t, func(command string, conn net.Conn) {
		time.Sleep(500 * time.Millisecond)
		fmt.Fprint(conn, "END LIST UPS\n")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := client.ListUPS(ctx); err == nil {
		t.Fatal("expected a deadline error")
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{`"plain"`, "plain", true},
		{`"with \"quotes\""`, `with "quotes"`, true},
		{`"back\\slash"`, `back\slash`, true},
		{`"line\nbreak"`, "line\nbreak", true},
		{`unquoted`, "", false},
		{`"unterminated`, "", false},
	}

	for _, test := range tests {
		got, ok := unquote(test.input)
		if ok != test.ok || got != test.want {
			t.Errorf("unquote(%q) = %q, %v; want %q, %v", test.input, got, ok, test.want, test.ok)
		}
	}
}
