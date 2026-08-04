package minecraft

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gorcon/rcon"
)

type Client struct {
	address  string
	password string
	timeout  time.Duration
}

func New(address, password string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{address: address, password: password, timeout: timeout}
}

func (c *Client) Configured() bool {
	return c.address != "" && c.password != ""
}

type result struct {
	out string
	err error
}

func (c *Client) Execute(ctx context.Context, command string) (string, error) {
	if !c.Configured() {
		return "", fmt.Errorf("rcon is not configured")
	}

	ch := make(chan result, 1)

	go func() {
		conn, err := rcon.Dial(
			c.address, c.password,
			rcon.SetDialTimeout(c.timeout),
			rcon.SetDeadline(c.timeout),
		)
		if err != nil {
			ch <- result{err: fmt.Errorf("connect to rcon: %w", err)}
			return
		}
		defer conn.Close()

		out, err := conn.Execute(command)
		if err != nil {
			ch <- result{err: fmt.Errorf("execute %q: %w", command, err)}
			return
		}
		ch <- result{out: out}
	}()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return "", r.err
		}
		return Clean(r.out), nil
	}
}

func Clean(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '§' && i+1 < len(runes) {
			i++ // skip the code character that follows
			continue
		}
		b.WriteRune(runes[i])
	}

	return strings.TrimSpace(b.String())
}