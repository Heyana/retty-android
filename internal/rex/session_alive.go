package rex

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// ErrServerUnavailable reports that nothing answered at the socket, as
// distinct from a server that answered but could not list its sessions.
var ErrServerUnavailable = errors.New("rex: session server unavailable")

// SessionAlive asks the server at socket, over a private control connection,
// whether sid is a running session. Remote uploads and image pastes check
// this before touching files or the clipboard, so a closed pane fails early.
func SessionAlive(ctx context.Context, socket, sid string) (bool, error) {
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrServerUnavailable, err)
	}
	client := NewClient(conn, nil)
	defer client.Close()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	sessions, err := client.List()
	if err != nil {
		return false, err
	}
	for _, s := range sessions {
		if s.ID == sid && !s.Exited {
			return true, nil
		}
	}
	return false, nil
}
