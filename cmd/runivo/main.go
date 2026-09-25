package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/Layerrail/runivo-cli/internal/cli"
)

var version = "dev"
var commit = "unknown"

func main() {
	// go install sets module build information but does not use release ldflags.
	if info, ok := debug.ReadBuildInfo(); ok {
		if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		if commit == "unknown" {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					commit = setting.Value
				}
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, version, commit))
}
