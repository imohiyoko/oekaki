// Command oekaki turns Terraform output into a diagram, and into the graph
// behind it.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/imohiyoko/oekaki/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(cli.Run(ctx, cli.Env{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}, os.Args[1:]))
}
