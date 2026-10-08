package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"bonbon/internal/client"
)

// The same executable runs the CLI, server, and menu in separate processes. Normal EOF
// follows archive cleanup. A server crash also closes the pipe, but cleanup is not
// guaranteed. The menu lifetime does not depend on a saved PID or status polling.
type menuBarProcess struct {
	control *os.File
	cmd     *exec.Cmd
	done    chan error
	closing chan struct{}
}

func launchMenuBar(target client.Target) (*menuBarProcess, error) {
	if !menuBarSupported {
		return nil, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	cmd := exec.Command(executable, "--dir", target.Dir, "server", "start")
	cmd.Env = append(os.Environ(), "BONBON_MENUBAR_CHILD=1")
	cmd.Dir = target.Dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.ExtraFiles = []*os.File{reader}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		writer.Close()
		return nil, err
	}
	menu := &menuBarProcess{control: writer, cmd: cmd, done: make(chan error, 1), closing: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		select {
		case <-menu.closing:
		default:
			fmt.Fprintf(os.Stderr, "BonBon menu bar exited before server shutdown (%v); sessions continue running.\n", err)
		}
		menu.done <- err
	}()
	return menu, nil
}

func (m *menuBarProcess) close() {
	if m == nil {
		return
	}
	close(m.closing)
	m.control.Close()
	select {
	case <-m.done:
	case <-time.After(3 * time.Second):
		// The handle is to our own child, never a PID read from disk.
		m.cmd.Process.Kill()
		<-m.done
	}
}

func runMenuBarChild(target client.Target) error {
	control := os.NewFile(3, "menu-server-lifetime")
	defer control.Close()
	stat, err := control.Stat()
	if err != nil || stat.Mode()&os.ModeNamedPipe == 0 {
		return errors.New("menu bar requires its server's lifetime pipe")
	}
	ended := make(chan struct{})
	go func() {
		io.Copy(io.Discard, control)
		close(ended)
	}()
	// The server launches us just before it publishes its connection information. Use that
	// parent's identity to prevent stale metadata from selecting another server.
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	parent := os.Getppid()
	for {
		select {
		case <-ended:
			return nil
		case <-deadline.C:
			return errors.New("menu bar could not verify its server")
		case <-ticker.C:
			info, err := target.Health()
			if err != nil || info.PID != parent {
				continue
			}
			return runNativeMenu(ended, func() error {
				return ui(target, nil)
			}, func() error {
				// This confirms receipt only. The lifetime pipe closes after server cleanup.
				if err := client.StopInstance(info); err != nil {
					return fmt.Errorf("cannot quit BonBon: %w", err)
				}
				return nil
			})
		}
	}
}
