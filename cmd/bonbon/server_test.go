package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"bonbon/internal/client"
	"bonbon/internal/instance"
)

func testCommand(args ...string) *exec.Cmd {
	// Runtime fixtures do not need a desktop or a native menu for each instance.
	if os.Getenv("BONBON_TEST_MENU_HELPER") == "" {
		for i, arg := range args {
			if arg == "server" && i+1 < len(args) && (args[i+1] == "start" || args[i+1] == "restart") {
				args = append(append([]string(nil), args...), "--no-menubar")
				break
			}
		}
	}
	executable, _ := os.Executable()
	cmd := exec.Command(executable, args...)
	cmd.Env = append(os.Environ(), "BONBON_TEST_CLI=1")
	return cmd
}

func startTestServer(t *testing.T, directory string) string {
	t.Helper()
	t.Setenv("BONBON_DIR", directory)
	t.Cleanup(func() {
		output, err := testCommand("--dir", directory, "server", "stop").CombinedOutput()
		if err != nil {
			t.Errorf("test server cleanup: %v: %s", err, output)
		}
	})
	output, err := testCommand("--dir", directory, "server", "start").CombinedOutput()
	if err != nil {
		log, _ := os.ReadFile(filepath.Join(directory, "server.log"))
		t.Fatalf("server startup: %v: %s\n%s", err, output, log)
	}
	info, err := (client.Target{Dir: directory}).Health()
	if err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(info.Port)
}

func TestServerLifecycleAndRestartStopsActiveRun(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "new-archive")
	startTestServer(t, directory)
	target := client.Target{Dir: directory}
	before, err := target.Health()
	if err != nil {
		t.Fatal(err)
	}
	// The launcher has already exited. The daemon must still answer requests.
	if output, err := testCommand("--dir", directory, "server", "start").CombinedOutput(); err != nil || !strings.Contains(string(output), "already running") {
		t.Fatalf("duplicate start: %s %v", output, err)
	}
	same, _ := target.Health()
	if same.Instance != before.Instance {
		t.Fatal("start replaced the running server")
	}
	executable, _ := os.Executable()
	c := launchAgent(t, t.TempDir(), "wait")
	waitFor(t, func() bool { return strings.Contains(c.text(), "READY") })
	store := archive(t, directory)
	sessions, _ := store.RecentSessions(1000)
	pid := sessions[0].Run.PID
	output, err := testCommand("--dir", directory, "server", "restart").CombinedOutput()
	if err != nil {
		t.Fatalf("restart: %v: %s", err, output)
	}
	c.wait(t, 137)
	after, err := target.Health()
	if err != nil || after.Instance == before.Instance || after.PID == before.PID {
		t.Fatalf("server not replaced: %+v %v", after, err)
	}
	if after.Executable != executable {
		t.Fatalf("restart used %s, expected %s", after.Executable, executable)
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("agent survived restart: %v", err)
	}
	latest, _ := store.LatestRun(sessions[0].ID)
	if latest.Status != "stopped" {
		t.Fatalf("run not finalized: %+v", latest)
	}
	if output, err = testCommand("--dir", directory, "server", "stop").CombinedOutput(); err != nil {
		t.Fatalf("stop: %s %v", output, err)
	}
	if _, err = target.Health(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("server still listening: %v", err)
	}
	if output, err = testCommand("--dir", directory, "server", "stop").CombinedOutput(); err != nil {
		t.Fatalf("repeated stop: %s %v", output, err)
	}
}

func TestServerRefusesTamperedMetadata(t *testing.T) {
	directory, other := t.TempDir(), t.TempDir()
	startTestServer(t, directory)
	startTestServer(t, other)
	original, err := instance.Read(directory)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := instance.Read(other)
	if err != nil {
		t.Fatal(err)
	}
	// Copy another live server's endpoint, even claiming the requested directory.
	foreign.DataDir = original.DataDir
	if err = instance.Publish(foreign); err != nil {
		t.Fatal(err)
	}
	defer instance.Publish(original)
	for _, action := range []string{"start", "stop", "restart"} {
		output, err := testCommand("--dir", directory, "server", action).CombinedOutput()
		if err == nil {
			t.Fatalf("%s accepted foreign server: %s", action, output)
		}
	}
	for _, contents := range []string{"{", ""} {
		if contents == "" {
			instance.Remove(directory)
		} else {
			if err = os.WriteFile(filepath.Join(directory, "server.json"), []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, action := range []string{"start", "stop", "restart"} {
			if output, err := testCommand("--dir", directory, "server", action).CombinedOutput(); err == nil {
				t.Fatalf("%s accepted missing/broken metadata: %s", action, output)
			}
		}
	}
	if _, err = (client.Target{Dir: other}).Health(); err != nil {
		t.Fatalf("foreign server affected: %v", err)
	}
}

func TestStaleMetadataAndSymlinkDirectory(t *testing.T) {
	foreignDir := t.TempDir()
	startTestServer(t, foreignDir)
	foreign, err := instance.Read(foreignDir)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	foreign.DataDir, err = filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Publish(foreign); err != nil {
		t.Fatal(err)
	}
	// A free archive lock makes stale metadata disposable, even if its port is
	// now used by another live server. Stop never contacts that server.
	if output, err := testCommand("--dir", directory, "server", "stop").CombinedOutput(); err != nil {
		t.Fatalf("stale stop: %s %v", output, err)
	}
	if err = instance.Publish(foreign); err != nil {
		t.Fatal(err)
	}
	startTestServer(t, directory)
	info, err := (client.Target{Dir: directory}).Health()
	if err != nil || info.Instance == foreign.Instance || info.Port == foreign.Port {
		t.Fatalf("stale start: %+v %v", info, err)
	}
	stat, err := os.Stat(filepath.Join(directory, "server.json"))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("descriptor permissions: %v %v", stat, err)
	}
	link := filepath.Join(t.TempDir(), "instance-link")
	if err = os.Symlink(directory, link); err != nil {
		t.Fatal(err)
	}
	viaLink, err := (client.Target{Dir: link}).Health()
	if err != nil || viaLink.Instance != info.Instance {
		t.Fatalf("symlink lookup: %+v %v", viaLink, err)
	}
	if output, err := testCommand("--dir", link, "server", "start").CombinedOutput(); err != nil || !strings.Contains(string(output), "already running") {
		t.Fatalf("symlink start: %s %v", output, err)
	}
	// Simulate a crash without active agents. Startup must replace its stale file.
	if err = syscall.Kill(info.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err = waitServerRelease(client.Target{Dir: directory}); err != nil {
		t.Fatal(err)
	}
	if output, err := testCommand("--dir", directory, "server", "start").CombinedOutput(); err != nil {
		t.Fatalf("crash recovery: %s %v", output, err)
	}
	after, err := (client.Target{Dir: directory}).Health()
	if err != nil || after.Instance == info.Instance {
		t.Fatalf("crashed instance reused: %+v %v", after, err)
	}
	if _, err = (client.Target{Dir: foreignDir}).Health(); err != nil {
		t.Fatalf("foreign server affected: %v", err)
	}
}

func TestViewDisconnectLeavesSessionAndServerRunning(t *testing.T) {
	directory := t.TempDir()
	startTestServer(t, directory)
	c := launchAgent(t, t.TempDir(), "wait")
	waitFor(t, func() bool { return strings.Contains(c.text(), "READY") })
	store := archive(t, directory)
	sessions, _ := store.RecentSessions(1000)
	pid := sessions[0].Run.PID
	c.Close()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("agent died with client: %v", err)
	}
	waitFor(t, func() bool { return sessionInfos(t, 10)[0].Viewers == 0 })
	resumed := resumeView(t, sessions[0].ID)
	waitFor(t, func() bool { return strings.Contains(resumed.text(), "READY") })
	latest, _ := store.LatestRun(sessions[0].ID)
	if latest.PID != pid || latest.Status != "running" {
		t.Fatalf("resume replaced run: %+v", latest)
	}
	stopTestSession(t, sessions[0].ID)
	resumed.wait(t, 137)
	if _, err := (client.Target{Dir: directory}).Health(); err != nil {
		t.Fatalf("server exited with client: %v", err)
	}

}

func TestServerStopWaitsForDetachedSession(t *testing.T) {
	directory := t.TempDir()
	startTestServer(t, directory)
	c := launchAgent(t, t.TempDir(), "wait")
	waitFor(t, func() bool { return strings.Contains(c.text(), "READY") })
	store := archive(t, directory)
	sessions, _ := store.RecentSessions(1000)
	c.Close()
	if output, err := testCommand("--dir", directory, "server", "stop").CombinedOutput(); err != nil {
		t.Fatalf("stop: %s %v", output, err)
	}
	run, err := store.LatestRun(sessions[0].ID)
	if err != nil || run.Status != "stopped" {
		t.Fatalf("detached session not finalized: %+v %v", run, err)
	}
	if err := syscall.Kill(run.PID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("detached child survived server stop: %v", err)
	}
}
