package main

import (
	"errors"
	"flag"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
)

type flags struct {
	address        config.NetAddress
	tls            bool
	reportInterval time.Duration
	pollInterval   time.Duration
}

func parseFlags(args []string) (flags, error) {

	var f flags
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)

	f.address = config.DefaultAddress
	fs.Var(&f.address, "a", "Net address host:port")
	reportInterval := fs.Int("r", 10, "Reporting interval in seconds")
	pollInterval := fs.Int("p", 2, "Polling interval in seconds")
	fs.BoolVar(&f.tls, "tls", false, "Enable TLS")

	err := fs.Parse(args)

	if err != nil {
		return f, err
	}
	if *reportInterval <= 0 {
		return f, errors.New("-r must be greater than 0")
	}
	if *pollInterval <= 0 {
		return f, errors.New("-p must be greater than 0")
	}

	f.reportInterval = time.Duration(*reportInterval) * time.Second
	f.pollInterval = time.Duration(*pollInterval) * time.Second

	return f, nil
}
