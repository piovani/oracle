// Command oracle is the single binary of the project: the first argument picks what to run.
package main

import (
	"fmt"
	"os"

	"oracle/internal/command"
)

var commands = map[string]func(args []string){
	"api":       command.API,
	"backtest":  command.Backtest,
	"recommend": command.Recommend,
	"sync":      command.Sync,
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	run, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
	}
	run(os.Args[2:])
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: oracle <command> [flags]

commands:
  sync       download B3 quotes into the database
  recommend  print the trading plan for the next session
  backtest   replay the strategy on past data
  api        start the HTTP server

Run "oracle <command> -h" to see the flags of a command.
`)
	os.Exit(2)
}
