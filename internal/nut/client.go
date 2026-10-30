// Package nut implements the small subset of the Network UPS Tools (NUT)
// upsd text protocol needed to discover UPSes and read their variables.
//
// The upsd protocol is line-oriented and runs over TCP (3493 by default).
// Read-only commands such as LIST UPS, LIST VAR and GET VAR do not require
// authentication on a default upsd installation. Optional USERNAME/PASSWORD/
// LOGIN credentials are supported for installations that restrict access.
package nut

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrVarNotSupported is returned by GetVar when upsd reports that the variable
// is not exposed by the driver.
var ErrVarNotSupported = errors.New("nut: variable not supported")

// UPS describes a UPS announced by upsd.
type UPS struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Client talks to a single upsd instance. It is safe for concurrent use
// because every operation uses its own connection.
type Client struct {
	Addr     string
	Username string
	Password string
	Timeout  time.Duration
}

// New returns a client for a upsd instance reachable at host:port.
func New(host string, port int, username, password string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		Addr:     net.JoinHostPort(host, strconv.Itoa(port)),
		Username: username,
		Password: password,
		Timeout:  timeout,
	}
}

// ListUPS returns the UPSes exposed by upsd, sorted by name.
func (c *Client) ListUPS(ctx context.Context) ([]UPS, error) {
	conn, reader, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := sendCommand(conn, "LIST UPS"); err != nil {
		return nil, err
	}

	result := make([]UPS, 0, 1)
	for {
		line, err := readLine(ctx, reader)
		if err != nil {
			return nil, err
		}
		if line == "END LIST UPS" {
			break
		}
		if err := lineError(line); err != nil {
			return nil, err
		}

		fields := strings.SplitN(line, " ", 3)
		if len(fields) < 2 || fields[0] != "UPS" {
			return nil, fmt.Errorf("nut: unexpected reply %q", line)
		}
		ups := UPS{Name: fields[1]}
		if len(fields) == 3 {
			if desc, ok := unquote(fields[2]); ok {
				ups.Description = desc
			}
		}
		result = append(result, ups)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// ListVars returns every variable exposed for the named UPS.
func (c *Client) ListVars(ctx context.Context, ups string) (map[string]string, error) {
	conn, reader, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := c.authenticate(ctx, conn, reader, ups); err != nil {
		return nil, err
	}
	if err := sendCommand(conn, "LIST VAR "+ups); err != nil {
		return nil, err
	}

	vars := make(map[string]string)
	for {
		line, err := readLine(ctx, reader)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "END LIST VAR") {
			break
		}
		if err := lineError(line); err != nil {
			return nil, err
		}

		_, name, value, ok := parseVarLine(line)
		if !ok {
			return nil, fmt.Errorf("nut: unexpected reply %q", line)
		}
		vars[name] = value
	}

	return vars, nil
}

// GetVar reads a single variable from a UPS.
func (c *Client) GetVar(ctx context.Context, ups, variable string) (string, error) {
	conn, reader, err := c.connect(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if err := c.authenticate(ctx, conn, reader, ups); err != nil {
		return "", err
	}
	if err := sendCommand(conn, "GET VAR "+ups+" "+variable); err != nil {
		return "", err
	}

	line, err := readLine(ctx, reader)
	if err != nil {
		return "", err
	}
	if err := lineError(line); err != nil {
		if strings.Contains(err.Error(), "VAR-NOT-SUPPORTED") {
			return "", fmt.Errorf("%w: %s", ErrVarNotSupported, variable)
		}
		return "", err
	}

	_, _, value, ok := parseVarLine(line)
	if !ok {
		return "", fmt.Errorf("nut: unexpected reply %q", line)
	}
	return value, nil
}

func (c *Client) connect(ctx context.Context) (net.Conn, *bufio.Reader, error) {
	dialer := net.Dialer{Timeout: c.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("nut: dial %s: %w", c.Addr, err)
	}

	deadline := time.Now().Add(c.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("nut: set deadline: %w", err)
	}

	return conn, bufio.NewReaderSize(conn, 64*1024), nil
}

func (c *Client) authenticate(ctx context.Context, conn net.Conn, reader *bufio.Reader, ups string) error {
	if c.Username == "" {
		return nil
	}

	for _, command := range []string{"USERNAME " + c.Username, "PASSWORD " + c.Password} {
		if err := sendCommand(conn, command); err != nil {
			return err
		}
		if err := expectOK(ctx, reader); err != nil {
			return err
		}
	}

	if ups != "" {
		if err := sendCommand(conn, "LOGIN "+ups); err != nil {
			return err
		}
		if err := expectOK(ctx, reader); err != nil {
			return err
		}
	}

	return nil
}

func sendCommand(conn net.Conn, command string) error {
	if _, err := fmt.Fprintf(conn, "%s\n", command); err != nil {
		return fmt.Errorf("nut: send %q: %w", command, err)
	}
	return nil
}

func readLine(ctx context.Context, reader *bufio.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return strings.TrimRight(line, "\r\n"), nil
		}
		return "", fmt.Errorf("nut: read: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func expectOK(ctx context.Context, reader *bufio.Reader) error {
	line, err := readLine(ctx, reader)
	if err != nil {
		return err
	}
	if line == "OK" {
		return nil
	}
	if err := lineError(line); err != nil {
		return err
	}
	return fmt.Errorf("nut: unexpected reply %q", line)
}

func lineError(line string) error {
	if line == "ERR" {
		return errors.New("nut: unknown error")
	}
	if strings.HasPrefix(line, "ERR ") {
		return fmt.Errorf("nut: %s", strings.TrimSpace(strings.TrimPrefix(line, "ERR ")))
	}
	return nil
}

func parseVarLine(line string) (upsName, variable, value string, ok bool) {
	fields := strings.SplitN(line, " ", 4)
	if len(fields) != 4 || fields[0] != "VAR" {
		return "", "", "", false
	}

	value, ok = unquote(fields[3])
	if !ok {
		return "", "", "", false
	}
	return fields[1], fields[2], value, true
}

// unquote decodes the quoted, backslash-escaped strings used by the NUT
// protocol. See "Network protocol information" in the NUT documentation.
func unquote(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", false
	}

	s = s[1 : len(s)-1]
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			b.WriteByte('\\')
			break
		}
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), true
}
