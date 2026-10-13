package main

import (
	"flag"
	"fmt"
	"log/slog"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
	"github.com/caarlos0/env/v11"
)

type params struct {
	address  config.NetAddress
	logLevel slog.Level
}

// Strings, so an env value goes through the same Set as the flag.
type envParams struct {
	Address string `env:"ADDRESS"`
}

func parseParams(args []string) (params, error) {

	var p params
	fs := flag.NewFlagSet("server", flag.ContinueOnError)

	p.address = config.DefaultAddress()
	fs.Var(&p.address, "a", "Net address host:port")
	fs.TextVar(&p.logLevel, "l", slog.LevelInfo, "log level")
	if err := fs.Parse(args); err != nil {
		return p, err
	}

	var e envParams
	if err := env.Parse(&e); err != nil {
		return p, fmt.Errorf("read env: %w", err)
	}
	if e.Address != "" {
		if err := p.address.Set(e.Address); err != nil {
			return p, fmt.Errorf("ADDRESS: %w", err)
		}
	}

	return p, nil
}
