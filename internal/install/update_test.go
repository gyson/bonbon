package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateInvokedExecutable(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "symlink"}[alias], func(t *testing.T) {
			f := newFixture(t)
			f.existing("0.9.0")
			f.release("0.10.0", "arm64", "0.10.0")
			executable := f.destination
			if alias {
				executable = filepath.Join(f.root, "alias")
				if err := os.Symlink(f.destination, executable); err != nil {
					t.Fatal(err)
				}
			}
			command, err := updateCommand(executable)
			if err != nil {
				t.Fatal(err)
			}
			other := filepath.Join(f.root, "do-not-install-here")
			command.Env = f.environment("TEST_TAG=v0.10.0", "BONBON_INSTALL_DIR="+other)
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "Installed BonBon 0.10.0") {
				t.Fatalf("update: %s %v", output, err)
			}
			if !strings.Contains(string(output), "Restart ends active sessions") {
				t.Fatal("missing restart guidance", string(output))
			}
			if _, err := os.Stat(other); !os.IsNotExist(err) {
				t.Fatal("updated the environment's directory", err)
			}
			if alias {
				if target, err := os.Readlink(executable); err != nil || target != f.destination {
					t.Fatal("replaced invocation symlink", target, err)
				}
			}
		})
	}
}

func TestUpdateSkipsCurrentAndNewerVersions(t *testing.T) {
	for _, version := range []string{"0.0.1", "0.10.0"} {
		t.Run(version, func(t *testing.T) {
			f := newFixture(t)
			f.existing(version)
			before, _ := os.Stat(f.destination)
			command, err := updateCommand(f.destination)
			if err != nil {
				t.Fatal(err)
			}
			command.Env = f.environment()
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v", output, err)
			}
			after, err := os.Stat(f.destination)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("replaced an up-to-date executable", err)
			}
			requests, _ := os.ReadFile(filepath.Join(f.root, "requests"))
			if strings.Contains(string(requests), "releases/assets/") {
				t.Fatal("downloaded assets for a current or newer installation")
			}
		})
	}
}

func TestUpdateRefusesRenamedExecutable(t *testing.T) {
	f := newFixture(t)
	f.existing("0.0.0")
	renamed := f.destination + "-custom"
	if err := os.Rename(f.destination, renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := updateCommand(renamed); err == nil || !strings.Contains(err.Error(), "renamed executable") {
		t.Fatal("accepted renamed executable", err)
	}
}
