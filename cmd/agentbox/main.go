package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nklisch/agentbox/internal/cli"
	"github.com/nklisch/agentbox/internal/exitcode"
)

func main() {
	// SIGINT/SIGTERM → exit 130 immediately. Subcommands that need cleanup
	// can register their own handlers in a later phase; P1 has nothing to
	// clean up.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		os.Exit(exitcode.Interrupted)
	}()

	err := cli.Execute(os.Args[1:])
	if err == nil {
		return
	}
	var ee *exitcode.Err
	if errors.As(err, &ee) {
		if ee.Wrap != nil {
			fmt.Fprintln(os.Stderr, "agentbox:", ee.Wrap.Error())
		}
		os.Exit(ee.Code)
	}
	// Unwrapped error from cobra (e.g. unknown flag): treat as InvalidArgs.
	fmt.Fprintln(os.Stderr, "agentbox:", err.Error())
	os.Exit(exitcode.InvalidArgs)
}
