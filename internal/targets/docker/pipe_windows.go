//go:build windows

package docker

import (
	"context"
	"net"
	"os"
	"time"
)

func isWindows() bool { return true }

// pipeConn adapts a named pipe opened as a file to net.Conn. It is a
// best-effort path for Docker Desktop on Windows; TCP or running Greenroom
// inside WSL is the supported route.
type pipeConn struct {
	f *os.File
}

func dialPipe(ctx context.Context, p string) (net.Conn, error) {
	var f *os.File
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err = os.OpenFile(p, os.O_RDWR, 0)
		if err == nil {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return &pipeConn{f: f}, nil
}

func (c *pipeConn) Read(b []byte) (int, error)         { return c.f.Read(b) }
func (c *pipeConn) Write(b []byte) (int, error)        { return c.f.Write(b) }
func (c *pipeConn) Close() error                       { return c.f.Close() }
func (c *pipeConn) LocalAddr() net.Addr                { return pipeAddr(c.f.Name()) }
func (c *pipeConn) RemoteAddr() net.Addr               { return pipeAddr(c.f.Name()) }
func (c *pipeConn) SetDeadline(t time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(t time.Time) error { return nil }

type pipeAddr string

func (a pipeAddr) Network() string { return "npipe" }
func (a pipeAddr) String() string  { return string(a) }
