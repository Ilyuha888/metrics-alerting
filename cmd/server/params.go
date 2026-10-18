package main

import (
	"flag"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
	"github.com/caarlos0/env/v11"
)

type params struct {
	address       config.NetAddress
	logLevel      slog.Level
	storeInterval time.Duration
	filePath      string
	restore       bool
}

// Strings, so an env value goes through the same Set as the flag.
type envParams struct {
	Address         string `env:"ADDRESS"`
	StoreInterval   string `env:"STORE_INTERVAL"`
	FileStoragePath string `env:"FILE_STORAGE_PATH"`
	Restore         string `env:"RESTORE"`
}

func parseParams(args []string) (params, error) {

	var p params
	fs := flag.NewFlagSet("server", flag.ContinueOnError)

	p.address = config.DefaultAddress()
	p.storeInterval = 300 * time.Second
	fs.Var(&p.address, "a", "Net address host:port")
	fs.TextVar(&p.logLevel, "l", slog.LevelInfo, "log level")
	fs.Var((*config.Seconds)(&p.storeInterval), "i", "Store interval in seconds, 0 saves on every update")
	fs.StringVar(&p.filePath, "f", "dumps/metrics.json", "File to store metrics in")
	// A bool flag takes its value only as -r=false; "-r false" ends flag parsing.
	fs.BoolVar(&p.restore, "r", true, "Restore metrics from the file at start")
	if err := fs.Parse(args); err != nil {
		return p, err
	}
	if fs.NArg() > 0 {
		return p, fmt.Errorf("unexpected arguments: %v", fs.Args())
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
	if e.StoreInterval != "" {
		if err := (*config.Seconds)(&p.storeInterval).Set(e.StoreInterval); err != nil {
			return p, fmt.Errorf("STORE_INTERVAL: %w", err)
		}
	}
	if e.FileStoragePath != "" {
		p.filePath = e.FileStoragePath
	}
	if e.Restore != "" {
		restore, err := strconv.ParseBool(e.Restore)
		if err != nil {
			return p, fmt.Errorf("RESTORE: %w", err)
		}
		p.restore = restore
	}

	return p, nil
}
