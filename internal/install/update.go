// Package install shares the release installer with the update command.
package install

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed install.sh
var script string

// Update replaces the invoked executable using the bundled release installer.
func Update() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command, err := updateCommand(executable)
	if err != nil {
		return err
	}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func updateCommand(executable string) (*exec.Cmd, error) {
	// Update the actual executable even when it was invoked through a symlink.
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	if filepath.Base(executable) != "bonbon" {
		return nil, fmt.Errorf("cannot update a renamed executable: %s; reinstall BonBon instead", executable)
	}
	command := exec.Command("/bin/sh", "-s", "--", "--dir", filepath.Dir(executable))
	command.Stdin = strings.NewReader(script)
	return command, nil
}
