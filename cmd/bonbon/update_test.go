package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpdateCommand(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "absent")
	unsupported := "development builds cannot update"
	if runtime.GOOS != "darwin" {
		unsupported = "macOS only"
	}
	for _, tc := range []struct {
		args []string
		want string
		ok   bool
	}{
		{[]string{"--help"}, "Usage: bonbon update", true},
		{nil, unsupported, false},
		{[]string{"extra"}, "takes no arguments", false},
		{[]string{"--version", "0.0.1"}, "flag provided but not defined", false},
	} {
		command := testCommand(append([]string{"--dir", directory, "update"}, tc.args...)...)
		output, err := command.CombinedOutput()
		if (err == nil) != tc.ok || !strings.Contains(string(output), tc.want) {
			t.Fatalf("%v: %s %v", tc.args, output, err)
		}
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("update accessed the instance", err)
	}
}
