package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"bonbon/internal/client"
	"bonbon/internal/history"
	"bonbon/internal/instance"
	"bonbon/internal/server"
)

func serverCommand(target client.Target, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: bonbon [--dir DIR] server start|stop|restart")
		return nil
	}
	action := args[0]
	if action != "start" && action != "stop" && action != "restart" {
		return errors.New("use bonbon server start|stop|restart")
	}
	flags := flag.NewFlagSet("server "+action, flag.ContinueOnError)
	var noMenu bool
	if action != "stop" {
		flags.BoolVar(&noMenu, "no-menubar", false, "start without the macOS menu bar")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("server command takes no positional arguments")
	}
	if os.Getenv("BONBON_MENUBAR_CHILD") == "1" {
		os.Unsetenv("BONBON_MENUBAR_CHILD")
		return runMenuBarChild(target)
	}
	if os.Getenv("BONBON_SERVER_CHILD") == "1" {
		os.Unsetenv("BONBON_SERVER_CHILD")
		return serve(target, noMenu)
	}
	if err := os.MkdirAll(target.Dir, 0700); err != nil {
		return err
	}
	// Resolve again after creation: a new directory can have symlinked parents
	// (for example /tmp on macOS). Parent and daemon must name the same archive.
	path, err := filepath.EvalSymlinks(target.Dir)
	if err != nil {
		return err
	}
	target.Dir = path
	unlock, err := lockServerFile(target.Dir, "server-control.lock")
	if err != nil {
		return fmt.Errorf("another server lifecycle command is in progress: %w", err)
	}
	defer unlock()
	if action == "stop" || action == "restart" {
		if err = stopServer(target); err != nil {
			return err
		}
	}
	if action == "start" || action == "restart" {
		return startServer(target, noMenu)
	}
	return nil
}

func lockServerFile(directory, name string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return func() { file.Close() }, nil
}

func serve(target client.Target, noMenu bool) error {
	unlock, err := lockServerFile(target.Dir, "server.lock")
	if err != nil {
		return fmt.Errorf("a server already owns %s: %w", target.Dir, err)
	}
	defer unlock()
	// Close the companion after SQLite closes, but before releasing the archive
	// lock. Quit waits for this pipe to close instead of waiting on that lock.
	var menu *menuBarProcess
	defer func() { menu.close() }()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	store, err := history.Open(filepath.Join(target.Dir, "history.sqlite"))
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.MarkInterrupted(); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer cancel()
	if !noMenu {
		menu, err = launchMenuBar(target)
		if err != nil {
			fmt.Fprintln(os.Stderr, "BonBon menu bar unavailable:", err)
		}
	}
	return server.Serve(ctx, listener, store)
}

func startServer(target client.Target, noMenu bool) error {
	check, err := lockServerFile(target.Dir, "server.lock")
	if errors.Is(err, syscall.EWOULDBLOCK) {
		info, err := target.Health()
		if err != nil {
			return fmt.Errorf("a server owns %s but cannot be verified: %w", target.Dir, err)
		}
		fmt.Printf("BonBon is already running at http://127.0.0.1:%d/ (PID %d).\n", info.Port, info.PID)
		return nil
	}
	if err != nil {
		return err
	}
	err = instance.Remove(target.Dir)
	check()
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(target.Dir, "server.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(executable, "--dir", target.Dir, "server", "start")
	if noMenu {
		cmd.Args = append(cmd.Args, "--no-menubar")
	}
	cmd.Env = append(os.Environ(), "BONBON_SERVER_CHILD=1")
	cmd.Dir = target.Dir
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		select {
		case err := <-done:
			return fmt.Errorf("server failed to start (%v); see %s", err, logPath)
		default:
		}
		if info, err := target.Health(); err == nil && info.PID == cmd.Process.Pid && info.DataDir == target.Dir {
			fmt.Printf("BonBon is running at http://127.0.0.1:%d/ (PID %d).\nHistory: %s\nLogs: %s\n", info.Port, info.PID, target.Dir, logPath)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// This handle belongs to the child we just launched; never kill a saved PID.
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		cmd.Process.Kill()
		<-done
	}
	return fmt.Errorf("server did not become ready; see %s", logPath)
}

func stopServer(target client.Target) error {
	unlock, err := lockServerFile(target.Dir, "server.lock")
	if err == nil {
		defer unlock()
		if err = instance.Remove(target.Dir); err != nil {
			return err
		}
		fmt.Printf("No BonBon server is running in %s.\n", target.Dir)
		return nil
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		return err
	}
	if err = target.Stop(); err != nil {
		return fmt.Errorf("stop server in %s: %w", target.Dir, err)
	}
	if err = waitServerRelease(target); err != nil {
		return err
	}
	fmt.Printf("Stopped BonBon in %s.\n", target.Dir)
	return nil
}

func waitServerRelease(target client.Target) error {
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		unlock, err := lockServerFile(target.Dir, "server.lock")
		if err == nil {
			unlock()
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("server has not released its archive; no replacement was started")
}
