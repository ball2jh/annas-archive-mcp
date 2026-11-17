//go:build windows

package browser

import (
	"context"
	"errors"
)

// launch is not implemented on Windows: Chrome's debugging pipe is handed
// over through inherited file descriptors, which os/exec cannot do there.
func launch(context.Context, string, []string) (*cdpConn, error) {
	return nil, errors.New("browser: passing the DDoS-Guard check automatically is not supported on Windows yet")
}
