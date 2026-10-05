package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"bonbon/internal/client"
	"bonbon/internal/history"
	"bonbon/internal/instance"
)

// Stand in for AppKit so lifecycle tests can run without a graphical login.
// Use the real inherited pipe, child process, server, and archive shutdown.
func menuBarFixture() {
	log, err := os.OpenFile(os.Getenv("BONBON_TEST_MENU_HELPER"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		os.Exit(1)
	}
	defer log.Close()
	fmt.Fprintf(log, "start %d\n", os.Getpid())
	control := os.NewFile(3, "lifetime")
	io.Copy(io.Discard, control)
	control.Close()
	fmt.Fprintf(log, "exit %d\n", os.Getpid())
}

func menuEvents(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestMenuFollowsServerLifecycle(t *testing.T) {
	if !menuBarSupported {
		t.Skip("automatic menu is macOS only")
	}
	path := filepath.Join(t.TempDir(), "menu-events")
	t.Setenv("BONBON_TEST_MENU_HELPER", path)
	directory := t.TempDir()
	startTestServer(t, directory)
	waitFor(t, func() bool { return strings.HasPrefix(menuEvents(t, path), "start ") })
	first := menuEvents(t, path)
	if output, err := testCommand("server", "start").CombinedOutput(); err != nil {
		t.Fatalf("repeat start: %v %s", err, output)
	}
	if current := menuEvents(t, path); current != first {
		t.Fatalf("repeat start duplicated the menu: %q", current)
	}
	if output, err := testCommand("server", "restart").CombinedOutput(); err != nil {
		t.Fatalf("restart: %v %s", err, output)
	}
	waitFor(t, func() bool { return strings.Count(menuEvents(t, path), "start ") == 2 })
	if output, err := testCommand("server", "stop").CombinedOutput(); err != nil {
		t.Fatalf("stop: %v %s", err, output)
	}
	lines := strings.Fields(menuEvents(t, path))
	if len(lines) != 8 || lines[0] != "start" || lines[2] != "exit" || lines[4] != "start" || lines[6] != "exit" || lines[1] != lines[3] || lines[5] != lines[7] || lines[1] == lines[5] {
		t.Fatalf("menus did not follow server generations: %v", lines)
	}
}

func TestMenuQuitFinalizesActiveSession(t *testing.T) {
	if !menuBarSupported {
		t.Skip("automatic menu is macOS only")
	}
	path := filepath.Join(t.TempDir(), "menu-events")
	t.Setenv("BONBON_TEST_MENU_HELPER", path)
	directory := t.TempDir()
	startTestServer(t, directory)
	c := launchAgent(t, t.TempDir(), "wait")
	waitFor(t, func() bool {
		return strings.Contains(c.text(), "READY") && strings.HasPrefix(menuEvents(t, path), "start ")
	})
	store := archive(t, directory)
	sessions, _ := store.ListSessions(history.SessionFilter{Limit: 1})
	target := client.Target{Dir: directory}
	info, err := target.Health()
	if err != nil {
		t.Fatal(err)
	}
	// Quit must still reach its parent after disposable connection data is lost.
	if err = instance.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err = client.StopInstance(info); err != nil {
		t.Fatal(err)
	}
	c.wait(t, 137)
	if err = waitServerRelease(target); err != nil {
		t.Fatal(err)
	}
	run, err := store.LatestRun(sessions[0].ID)
	if err != nil || run.Status != "stopped" {
		t.Fatalf("quit did not finalize history: %+v %v", run, err)
	}
	if strings.Count(menuEvents(t, path), "exit ") != 1 {
		t.Fatal("server released the archive before its menu exited")
	}
}

func TestMenuPipeClosesAfterServerCrash(t *testing.T) {
	if !menuBarSupported {
		t.Skip("automatic menu is macOS only")
	}
	path := filepath.Join(t.TempDir(), "menu-events")
	t.Setenv("BONBON_TEST_MENU_HELPER", path)
	directory := t.TempDir()
	startTestServer(t, directory)
	waitFor(t, func() bool { return strings.HasPrefix(menuEvents(t, path), "start ") })
	info, err := (client.Target{Dir: directory}).Health()
	if err != nil {
		t.Fatal(err)
	}
	// This PID belongs to the fixture server we just launched and verified.
	if err = syscall.Kill(info.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return strings.Count(menuEvents(t, path), "exit ") == 1 })
}

func TestMenuQuitLeavesReplacementConnectionFile(t *testing.T) {
	directory := t.TempDir()
	startTestServer(t, directory)
	target := client.Target{Dir: directory}
	original, err := target.Health()
	if err != nil {
		t.Fatal(err)
	}
	// Ensure even a failing test stops only the fixture server we launched.
	t.Cleanup(func() {
		client.StopInstance(original)
		waitServerRelease(target)
	})
	replacement := original
	replacement.Instance = "replacement"
	if err = instance.Publish(replacement); err != nil {
		t.Fatal(err)
	}
	if err = client.StopInstance(original); err != nil {
		t.Fatal(err)
	}
	if err = waitServerRelease(target); err != nil {
		t.Fatal(err)
	}
	saved, err := instance.Read(directory)
	if err != nil || saved != replacement {
		t.Fatalf("old server removed replacement connection information: %+v, %v", saved, err)
	}
}

func TestServerCanStartWithoutMenu(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menu-events")
	t.Setenv("BONBON_TEST_MENU_HELPER", path)
	directory := t.TempDir()
	t.Cleanup(func() { testCommand("--dir", directory, "server", "stop").Run() })
	if output, err := testCommand("--dir", directory, "server", "start", "--no-menubar").CombinedOutput(); err != nil {
		t.Fatalf("headless start: %v %s", err, output)
	}
	info, err := (client.Target{Dir: directory}).Health()
	if err != nil || info.PID == 0 {
		t.Fatalf("headless server unavailable: PID %d, %v", info.PID, err)
	}
	if output, err := testCommand("--dir", directory, "server", "stop").CombinedOutput(); err != nil {
		t.Fatalf("headless stop: %v %s", err, output)
	}
	if events := menuEvents(t, path); events != "" {
		t.Fatalf("headless server launched a menu: %q", events)
	}
}
