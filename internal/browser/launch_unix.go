//go:build !windows

package browser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// closeGrace is how long Chrome gets to exit cleanly before it is killed.
const closeGrace = 5 * time.Second

// launch starts Chrome with the given arguments and returns a connection to
// its debugging pipe. Chrome reads commands on file descriptor 3 and writes
// replies on 4.
func launch(ctx context.Context, chrome string, args []string) (*cdpConn, error) {
	toChromeR, toChromeW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("browser: create pipe: %w", err)
	}
	fromChromeR, fromChromeW, err := os.Pipe()
	if err != nil {
		_ = toChromeR.Close()
		_ = toChromeW.Close()
		return nil, fmt.Errorf("browser: create pipe: %w", err)
	}

	// Not CommandContext: Chrome is stopped through the connection's close,
	// which lets it exit cleanly (and save its profile) before any kill.
	cmd := exec.Command(chrome, args...)
	cmd.ExtraFiles = []*os.File{toChromeR, fromChromeW} // fds 3 and 4 in the child
	// Own process group, so a kill reaches Chrome's helper processes and a
	// Ctrl-C aimed at the server does not also hit the browser mid-run.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err = cmd.Start()
	// The child holds its own copies of its pipe ends now.
	_ = toChromeR.Close()
	_ = fromChromeW.Close()
	if err != nil {
		_ = toChromeW.Close()
		_ = fromChromeR.Close()
		return nil, fmt.Errorf("browser: start Chrome: %w", err)
	}

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	var conn *cdpConn
	stop := func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeGrace)
		defer cancel()
		_ = conn.call(closeCtx, "", "Browser.close", nil, nil)
		select {
		case <-exited:
		case <-closeCtx.Done():
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-exited
		}
		_ = fromChromeR.Close()
	}
	conn = newCDPConn(fromChromeR, toChromeW, stop)
	return conn, nil
}
