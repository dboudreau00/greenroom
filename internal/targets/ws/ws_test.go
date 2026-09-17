package ws

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// echoServer upgrades the connection, picks the first offered subprotocol,
// sends a ping before the first echo and then echoes every message.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Sec-WebSocket-Key")
		proto := strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",")[0]
		proto = strings.TrimSpace(proto)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijacker")
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: %s\r\n\r\n", ServerAccept(key), proto)
		sc := ServerConn(conn, rw.Reader)
		first := true
		for {
			op, msg, err := sc.ReadMessage()
			if err != nil {
				return
			}
			if first {
				sc.WriteServerMessage(OpPing, []byte("hi"))
				first = false
			}
			if err := sc.WriteServerMessage(op, msg); err != nil {
				return
			}
		}
	}))
}

func TestEchoRoundTrip(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/exec?x=1", nil, nil, []string{"v5.channel.k8s.io", "v4.channel.k8s.io"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Subprotocol != "v5.channel.k8s.io" {
		t.Fatalf("subprotocol %q", c.Subprotocol)
	}
	for _, size := range []int{0, 5, 200, 70000} {
		payload := bytes.Repeat([]byte("ab"), size/2)
		if err := c.WriteMessage(OpBinary, payload); err != nil {
			t.Fatal(err)
		}
		op, got, err := c.ReadMessage()
		if err != nil || op != OpBinary || !bytes.Equal(got, payload) {
			t.Fatalf("size %d: op %d len %d err %v", size, op, len(got), err)
		}
	}
	if err := c.WriteMessage(OpText, []byte("text")); err != nil {
		t.Fatal(err)
	}
	if op, got, err := c.ReadMessage(); err != nil || op != OpText || string(got) != "text" {
		t.Fatalf("text: %d %q %v", op, got, err)
	}
}

func TestHandshakeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()
	_, resp, err := Dial(context.Background(), srv.URL, nil, nil, nil)
	if err == nil || resp == nil || resp.StatusCode != 403 || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("expected 403 error, got %v %v", err, resp)
	}
}

func TestServerClose(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj := w.(http.Hijacker)
		conn, rw, _ := hj.Hijack()
		defer conn.Close()
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", ServerAccept(r.Header.Get("Sec-WebSocket-Key")))
		sc := ServerConn(conn, rw.Reader)
		sc.WriteServerMessage(OpBinary, []byte{1, 'x'})
		sc.WriteServerMessage(OpClose, []byte{0x03, 0xE8})
	}))
	defer srv.Close()
	c, _, err := Dial(context.Background(), srv.URL, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, msg, err := c.ReadMessage(); err != nil || string(msg) != "\x01x" {
		t.Fatalf("first %q %v", msg, err)
	}
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("expected EOF after close frame")
	}
}
