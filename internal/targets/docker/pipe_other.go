//go:build !windows

package docker

import (
	"context"
	"errors"
	"net"
)

func isWindows() bool { return false }

func dialPipe(ctx context.Context, p string) (net.Conn, error) {
	return nil, errors.New("npipe docker hosts are only available on Windows")
}
