package main

import (
	"errors"
	"flag"
	"fmt"
	"runtime"

	"bonbon/internal/install"
)

func update(args []string) error {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Println("Usage: bonbon update\n\nInstall the latest BonBon release. Running sessions continue.\nRestart the server when ready to use the new version; restarting ends active sessions.")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("update takes no arguments")
	}
	if runtime.GOOS != "darwin" {
		return errors.New("updates are available for macOS only")
	}
	if version == "dev" {
		return errors.New("development builds cannot update; install a release first")
	}
	return install.Update()
}
