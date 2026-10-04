package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"bonbon/internal/client"
	"bonbon/internal/protocol"
)

func query(target client.Target, args []string) error {
	flags := flag.NewFlagSet("query", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("provide one quoted SQL query, for example: bonbon query \"SELECT * FROM sessions\"")
	}
	result, err := target.Call(protocol.Request{Operation: "query", SQL: flags.Arg(0)})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(result))
	return err
}
