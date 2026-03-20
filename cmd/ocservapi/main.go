package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/example/ocservapi/internal/app"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "/etc/ocservapi/config.yaml", "path to config file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, *configPath, version); err != nil {
		log.Fatal(err)
	}
}
