package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"bonbon/internal/client"
)

const usage = `Usage:
  bonbon --version
  bonbon update
  bonbon [--dir DIR] server start|stop|restart
  bonbon [--dir DIR] ui
  bonbon [--dir DIR] query SQL

--dir selects a BonBon instance, not the agent's working directory.
Start the server with bonbon server start, then open bonbon ui.
Create and manage interactive shell sessions in the web UI.
Closing a browser view leaves its session running.`

// Local builds use development storage. The release target sets this at link time.
var instanceDirName = ".bonbon-dev"

// Release builds set the version from their tag.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "bonbon:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("bonbon", flag.ContinueOnError)
	flags.Usage = func() { fmt.Println(usage) }
	directory := flags.String("dir", defaultInstanceDir(), "BonBon instance directory")
	showVersion := flags.Bool("version", false, "Print the executable version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	args = flags.Args()
	if *showVersion {
		if len(args) != 0 {
			return errors.New("--version takes no command or arguments")
		}
		fmt.Println("bonbon", version)
		return nil
	}
	if len(args) == 0 || args[0] == "help" {
		fmt.Println(usage)
		return nil
	}
	if args[0] == "update" {
		return update(args[1:])
	}
	path, err := filepath.Abs(*directory)
	if err != nil {
		return err
	}
	target := client.Target{Dir: path}
	switch args[0] {
	case "server":
		return serverCommand(target, args[1:])
	case "ui":
		return ui(target, args[1:])
	case "query":
		return query(target, args[1:])
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

func defaultInstanceDir() string {
	if directory := os.Getenv("BONBON_DIR"); directory != "" {
		return directory
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, instanceDirName)
}
