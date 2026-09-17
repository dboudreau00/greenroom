// Package ws is a minimal RFC 6455 WebSocket client: enough to open a
// Kubernetes pod exec stream, write masked binary frames, read frames with
// fragmentation, answer pings and close cleanly.
package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Opcodes.
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

// Conn is an open WebSocket.
type Conn struct {
	conn        net.Conn
	br          *bufio.Reader
	wmu         sync.Mutex
	Subprotocol string
	closed      bool
}

// Dial opens a WebSocket to u (ws://, wss://, http:// or https://).
func Dial(ctx context.Context, u string, header http.Header, tlsCfg *tls.Config, subprotocols []string) (*Conn, *http.Response, error) {
	pu, err := url.Parse(u)
	if err != nil {
		return nil, nil, err
	}
	secure := pu.Scheme == "wss" || pu.Scheme == "https"
	host := pu.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		if secure {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, nil, err
	}
	var conn net.Conn = raw
	if secure {
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if tlsCfg != nil {
			cfg = tlsCfg.Clone()
		}
		if cfg.ServerName == "" {
			cfg.ServerName = pu.Hostname()
		}
		tc := tls.Client(raw, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, nil, err
		}
		conn = tc
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	var keyBytes [16]byte
	rand.Read(keyBytes[:])
	key := base64.StdEncoding.EncodeToString(keyBytes[:])
	path := pu.RequestURI()
	var sb strings.Builder
	fmt.Fprintf(&sb, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&sb, "Host: %s\r\n", pu.Host)
	sb.WriteString("Upgrade: websocket\r\nConnection: Upgrade\r\n")
	fmt.Fprintf(&sb, "Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", key)
	if len(subprotocols) > 0 {
		fmt.Fprintf(&sb, "Sec-WebSocket-Protocol: %s\r\n", strings.Join(subprotocols, ", "))
	}
	for k, vs := range header {
		for _, v := range vs {
			fmt.Fprintf(&sb, "%s: %s\r\n", k, v)
		}
	}
	sb.WriteString("\r\n")
	if _, err := io.WriteString(conn, sb.String()); err != nil {
		conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		conn.Close()
		return nil, resp, fmt.Errorf("websocket handshake: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	want := base64.StdEncoding.EncodeToString(acceptHash(key))
	if resp.Header.Get("Sec-WebSocket-Accept") != want {
		conn.Close()
		return nil, resp, errors.New("websocket handshake: bad Sec-WebSocket-Accept")
	}
	conn.SetDeadline(time.Time{})
	return &Conn{conn: conn, br: br, Subprotocol: resp.Header.Get("Sec-WebSocket-Protocol")}, resp, nil
}

func acceptHash(key string) []byte {
	h := sha1.New()
	io.WriteString(h, key+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
	return h.Sum(nil)
}

// SetDeadline sets the read and write deadline.
func (c *Conn) SetDeadline(t time.Time) error { return c.conn.SetDeadline(t) }

// WriteMessage sends one masked frame.
func (c *Conn) WriteMessage(opcode int, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed && opcode != OpClose {
		return errors.New("websocket closed")
	}
	var hdr [14]byte
	n := 0
	hdr[0] = 0x80 | byte(opcode&0x0f)
	n++
	l := len(data)
	switch {
	case l < 126:
		hdr[1] = 0x80 | byte(l)
		n++
	case l < 65536:
		hdr[1] = 0x80 | 126
		binary.BigEndian.PutUint16(hdr[2:], uint16(l))
		n += 3
	default:
		hdr[1] = 0x80 | 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(l))
		n += 9
	}
	var mask [4]byte
	rand.Read(mask[:])
	copy(hdr[n:], mask[:])
	n += 4
	masked := make([]byte, l)
	for i := range data {
		masked[i] = data[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(hdr[:n]); err != nil {
		return err
	}
	_, err := c.conn.Write(masked)
	return err
}

// ReadMessage returns the next complete data message. Control frames are
// handled internally; a close frame yields io.EOF.
func (c *Conn) ReadMessage() (int, []byte, error) {
	var msg []byte
	msgOp := -1
	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case OpPing:
			c.WriteMessage(OpPong, payload)
			continue
		case OpPong:
			continue
		case OpClose:
			c.wmu.Lock()
			if !c.closed {
				c.closed = true
				c.wmu.Unlock()
				c.WriteMessage(OpClose, payload)
			} else {
				c.wmu.Unlock()
			}
			return OpClose, payload, io.EOF
		case OpContinuation:
			if msgOp < 0 {
				return 0, nil, errors.New("websocket: unexpected continuation frame")
			}
			msg = append(msg, payload...)
		default:
			msgOp = op
			msg = append(msg, payload...)
		}
		if fin {
			return msgOp, msg, nil
		}
	}
}

func (c *Conn) readFrame() (fin bool, op int, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	op = int(h[0] & 0x0f)
	masked := h[1]&0x80 != 0
	l := uint64(h[1] & 0x7f)
	switch l {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		l = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		l = binary.BigEndian.Uint64(b[:])
	}
	if l > 64<<20 {
		err = fmt.Errorf("websocket frame too large: %d", l)
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, l)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

// Close sends a close frame and closes the socket.
func (c *Conn) Close() error {
	c.wmu.Lock()
	already := c.closed
	c.closed = true
	c.wmu.Unlock()
	if !already {
		c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		var code [2]byte
		binary.BigEndian.PutUint16(code[:], 1000)
		c.WriteMessage(OpClose, code[:])
	}
	return c.conn.Close()
}

// ServerAccept computes the accept header for a key; exported for tests
// that stand up a fake server.
func ServerAccept(key string) string {
	return base64.StdEncoding.EncodeToString(acceptHash(key))
}

// ServerConn wraps an already-upgraded server side connection for tests.
func ServerConn(conn net.Conn, br *bufio.Reader) *Conn {
	if br == nil {
		br = bufio.NewReader(conn)
	}
	return &Conn{conn: conn, br: br}
}

// WriteServerMessage writes an unmasked frame (server side, tests only).
func (c *Conn) WriteServerMessage(opcode int, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	var hdr [10]byte
	n := 0
	hdr[0] = 0x80 | byte(opcode&0x0f)
	n++
	l := len(data)
	switch {
	case l < 126:
		hdr[1] = byte(l)
		n++
	case l < 65536:
		hdr[1] = 126
		binary.BigEndian.PutUint16(hdr[2:], uint16(l))
		n += 3
	default:
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:], uint64(l))
		n += 9
	}
	if _, err := c.conn.Write(hdr[:n]); err != nil {
		return err
	}
	_, err := c.conn.Write(data)
	return err
}
