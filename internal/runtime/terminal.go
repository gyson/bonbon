package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Control associates an input receipt with the view that submitted it.
type Control struct {
	protocol.Message
	Ack func(error)
}

// Launch adds server-owned shell settings to a session request.
type Launch struct {
	protocol.Run
	Shell string
	Env   []string
}

// Run owns an agent PTY on the server. Terminal controls arrive from the client;
// the server commits original output before emulation and input before delivery.
func Run(ctx context.Context, store *history.Store, session history.Session, request Launch, controls <-chan Control, publish func(history.Event) ([]byte, error)) (int, error) {
	command := []string{request.Shell, "-i"}
	size := &pty.Winsize{Rows: request.Size.Rows, Cols: request.Size.Cols}
	run, err := store.NewRun(session.ID)
	if err != nil {
		return 1, err
	}
	// Commit and publish in the same order, including concurrent output and resize.
	// The server emulator must see the same ordering as durable reconstruction.
	var eventMu sync.Mutex
	var writeMu sync.Mutex
	var tty *os.File
	write := func(data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		tty.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err := tty.Write(data)
		return err
	}
	appendEvent := func(kind string, data []byte, text string) error {
		eventMu.Lock()
		defer eventMu.Unlock()
		event, err := store.Append(session.ID, run.ID, kind, data, text)
		if err == nil && (kind == "start" || kind == "resize" || kind == "output") {
			responses, renderErr := publish(event)
			if renderErr != nil {
				return renderErr
			}
			if len(responses) > 0 {
				if _, err = store.Append(session.ID, run.ID, "terminal-response", responses, ""); err == nil {
					err = write(responses)
				}
			}
		}
		return err
	}
	status := func() error {
		if err := store.SaveRun(run); err != nil {
			return err
		}
		data, _ := json.Marshal(run)
		return appendEvent("run", data, "")
	}
	metadata, _ := json.Marshal(map[string]any{"command": command, "workspace": session.Workspace, "cols": size.Cols, "rows": size.Rows})
	if err = appendEvent("start", metadata, ""); err != nil {
		run.Status, run.Ended, run.Detail = "failed", history.Now(), err.Error()
		return 1, errors.Join(err, status())
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = session.Workspace
	// Inherit the server's environment and normal interactive shell startup.
	cmd.Env = append(request.Env, "BONBON_SESSION="+session.ID, "BONBON_DIR="+filepath.Dir(store.Path))
	tty, err = pty.StartWithSize(cmd, size)
	if err != nil {
		run.Status, run.Ended, run.Detail = "failed", history.Now(), err.Error()
		return 1, errors.Join(err, status())
	}
	// Register the PTY with Go's poller so writes can have deadlines and closing
	// it can interrupt a read, including when a descendant retains the slave.
	fd, err := unix.FcntlInt(tty.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	tty.Close()
	if err == nil {
		err = syscall.SetNonblock(fd, true)
	}
	if err != nil {
		if fd >= 0 {
			syscall.Close(fd)
		}
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
		run.Status, run.Ended, run.Detail = "failed", history.Now(), err.Error()
		return 1, errors.Join(err, status())
	}
	tty = os.NewFile(uintptr(fd), "bonbon-pty")
	defer tty.Close()
	run.PID, run.Status = cmd.Process.Pid, "running"
	failures := make(chan error, 4)
	report := func(err error) {
		if err != nil {
			select {
			case failures <- err:
			default:
			}
		}
	}
	report(status())
	outputDone := make(chan error, 1)
	go func() {
		parser := &history.Text{}
		buf := make([]byte, 16384)
		var result error
		defer func() { outputDone <- result }()
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				text := parser.Push(buf[:n])
				if result = appendEvent("output", buf[:n], text); result != nil {
					report(fmt.Errorf("record terminal output: %w", result))
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, syscall.EIO) && !errors.Is(err, os.ErrClosed) {
					result = err
					report(err)
				}
				return
			}
		}
	}()
	childDone := make(chan error, 1)
	go func() { childDone <- cmd.Wait() }()
	var waitErr, captureErr error
	var deadline <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	stopping := false
	// An interactive shell gives its foreground job a separate process group.
	// Remember that group through shutdown even if the shell exits first.
	groups := map[int]bool{cmd.Process.Pid: true}
	signalOwned := func(sig syscall.Signal) {
		if group := foregroundGroup(tty); group > 0 {
			groups[group] = true
		}
		for group := range groups {
			syscall.Kill(-group, sig)
		}
	}
	stop := func(sig syscall.Signal) {
		signalOwned(sig)
		if !stopping {
			stopping = true
			timer = time.NewTimer(2 * time.Second)
			deadline = timer.C
		}
	}
wait:
	for {
		select {
		case waitErr = <-childDone:
			break wait
		case err := <-failures:
			captureErr = errors.Join(captureErr, err)
			stop(syscall.SIGKILL)
		case <-deadline:
			signalOwned(syscall.SIGKILL)
			deadline = nil
		case <-ctx.Done():
			stop(syscall.SIGHUP)
			ctx = context.Background()
		case control, ok := <-controls:
			if !ok {
				stop(syscall.SIGHUP)
				controls = nil
				continue
			}
			switch control.Type {
			case "input":
				if err := appendEvent("input", control.Data, ""); err != nil {
					if control.Ack != nil {
						control.Ack(err)
					}
					report(err)
					continue
				}
				err := write(control.Data)
				if control.Ack != nil {
					control.Ack(err)
				}
				report(err)
			case "resize":
				if size.Rows == control.Size.Rows && size.Cols == control.Size.Cols {
					// A new view gets the server's current screen. Reattaching at
					// the same size must not provoke fresh application output.
					continue
				}
				size = &pty.Winsize{Rows: control.Size.Rows, Cols: control.Size.Cols}
				data, _ := json.Marshal(control.Size)
				report(appendEvent("resize", data, ""))
				// The PTY notifies its foreground process when dimensions change.
				report(resize(tty, size))
			case "signal":
				if syscall.Signal(control.Signal) == syscall.SIGINT {
					group := foregroundGroup(tty)
					if group <= 0 {
						group = cmd.Process.Pid
					}
					syscall.Kill(-group, syscall.SIGINT)
				}
			}
		}
	}
	// End the launched command group and any foreground shell job. Programs
	// that deliberately detach into other sessions are outside this PTY lifetime.
	signalOwned(syscall.SIGKILL)
	select {
	case err := <-outputDone:
		captureErr = errors.Join(captureErr, err)
	case <-time.After(3 * time.Second):
		tty.Close()
		captureErr = errors.Join(captureErr, errors.New("timed out draining terminal output"), <-outputDone)
	}
	for len(failures) > 0 {
		captureErr = errors.Join(captureErr, <-failures)
	}
	run.Status, run.Ended = "exited", history.Now()
	if stopping {
		run.Status = "stopped"
	}
	if captureErr != nil {
		run.Status, run.Detail = "failed", captureErr.Error()
	} else if waitErr != nil {
		run.Detail = waitErr.Error()
	}
	code := cmd.ProcessState.ExitCode()
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		code = 128 + int(ws.Signal())
	}
	return code, errors.Join(captureErr, status())
}

func resize(tty *os.File, size *pty.Winsize) error {
	// File.Fd switches pollable descriptors back to blocking mode. Use the raw
	// connection to resize without losing interruptible reads and write deadlines.
	connection, err := tty.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	err = connection.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Row: size.Rows, Col: size.Cols, Xpixel: size.X, Ypixel: size.Y})
	})
	return errors.Join(err, ioctlErr)
}

// Query through SyscallConn so the PTY stays registered with the poller.
func foregroundGroup(tty *os.File) int {
	connection, err := tty.SyscallConn()
	if err != nil {
		return 0
	}
	group := 0
	connection.Control(func(fd uintptr) { group, _ = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP) })
	return group
}
