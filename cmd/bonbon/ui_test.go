package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"bonbon/internal/instance"
)

func browserFixture(t *testing.T) string {
	t.Helper()
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux":
		name = "xdg-open"
	default:
		t.Skip("browser launcher is unsupported on this OS")
	}
	directory := t.TempDir()
	args := filepath.Join(directory, "browser-args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$BONBON_TEST_BROWSER_ARGS\"\nexit \"${BONBON_TEST_BROWSER_EXIT:-0}\"\n"
	if err := os.WriteFile(filepath.Join(directory, name), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BONBON_TEST_BROWSER_ARGS", args)
	t.Setenv("BONBON_TEST_BROWSER_EXIT", "0")
	return args
}

func TestUIOpensSelectedInstanceHome(t *testing.T) {
	argsPath := browserFixture(t)
	directory := t.TempDir()
	port := startTestServer(t, directory)
	url := "http://127.0.0.1:" + port + "/"
	// The explicit instance wins over an unrelated environment selection.
	t.Setenv("BONBON_DIR", filepath.Join(t.TempDir(), "missing"))
	if output, err := testCommand("--dir", directory, "ui").CombinedOutput(); err != nil || !strings.Contains(string(output), url) {
		t.Fatalf("ui: %s %v", output, err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil || string(args) != url+"\n" {
		t.Fatalf("browser must receive only the home URL: %q %v", args, err)
	}
	// Environment selection works too, and launcher failures include the URL.
	t.Setenv("BONBON_DIR", directory)
	t.Setenv("BONBON_TEST_BROWSER_EXIT", "1")
	if output, err := testCommand("ui").CombinedOutput(); err == nil || !strings.Contains(string(output), "open "+url+" manually") {
		t.Fatalf("launcher failure: %s %v", output, err)
	}
}

func TestUIDoesNotOpenUnverifiedServer(t *testing.T) {
	argsPath := browserFixture(t)
	directory := t.TempDir()
	if output, err := testCommand("--dir", directory, "ui").CombinedOutput(); err == nil || !strings.Contains(string(output), "server start") {
		t.Fatalf("missing server: %s %v", output, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "server.json")); !os.IsNotExist(err) {
		t.Fatal("ui started a server")
	}
	startTestServer(t, directory)
	info, err := instance.Read(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Publish(info) // Restore discovery before server cleanup.
	tampered := info
	tampered.Instance = "wrong-instance"
	if err := instance.Publish(tampered); err != nil {
		t.Fatal(err)
	}
	if output, err := testCommand("--dir", directory, "ui").CombinedOutput(); err == nil || !strings.Contains(string(output), "identity does not match") {
		t.Fatalf("unverified server: %s %v", output, err)
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatal("opened browser without verifying the server")
	}
}

func TestUIHelpAndArguments(t *testing.T) {
	argsPath := browserFixture(t)
	t.Setenv("BONBON_DIR", t.TempDir())
	if output, err := testCommand("ui", "--help").CombinedOutput(); err != nil || !strings.Contains(string(output), "Usage: bonbon [--dir DIR] ui") {
		t.Fatalf("help: %s %v", output, err)
	}
	for _, args := range [][]string{{"ui", "~/project"}, {"ui", "session-id"}, {"ui", "--unknown"}} {
		if output, err := testCommand(args...).CombinedOutput(); err == nil {
			t.Fatalf("accepted invalid arguments: %s", output)
		}
	}
	if _, err := os.Stat(argsPath); !os.IsNotExist(err) {
		t.Fatal("help or invalid arguments opened a browser")
	}
}
