package main

import (
	"flag"
	"fmt"
	"log/slog"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
	"github.com/caarlos0/env/v11"
)

type params struct {
	address        config.NetAddress
	reportInterval time.Duration
	pollInterval   time.Duration
	logLevel       slog.Level
}

// Env intervals are bare seconds ("5"), and env sees only the declared field type, so a
// time.Duration field would demand a unit and fail. Strings routed through the flags' Set
// keep validation in one place.
type envParams struct {
	Address        string `env:"ADDRESS"`
	ReportInterval string `env:"REPORT_INTERVAL"`
	PollInterval   string `env:"POLL_INTERVAL"`
}

func parseParams(args []string) (params, error) {

	var p params
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)

	p.address = config.DefaultAddress()
	p.pollInterval = 2 * time.Second
	p.reportInterval = 10 * time.Second

	fs.Var(&p.address, "a", "Net address host:port")
	fs.Var((*config.Seconds)(&p.reportInterval), "r", "Reporting interval in seconds")
	fs.Var((*config.Seconds)(&p.pollInterval), "p", "Polling interval in seconds")
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
	if e.ReportInterval != "" {
		if err := (*config.Seconds)(&p.reportInterval).Set(e.ReportInterval); err != nil {
			return p, fmt.Errorf("REPORT_INTERVAL: %w", err)
		}
	}
	if e.PollInterval != "" {
		if err := (*config.Seconds)(&p.pollInterval).Set(e.PollInterval); err != nil {
			return p, fmt.Errorf("POLL_INTERVAL: %w", err)
		}
	}

	if err := checkPositive(p.reportInterval, "r", "REPORT_INTERVAL", e.ReportInterval); err != nil {
		return p, err
	}
	if err := checkPositive(p.pollInterval, "p", "POLL_INTERVAL", e.PollInterval); err != nil {
		return p, err
	}

	return p, nil
}

// checkPositive keeps the error text the agent gave when its own seconds type rejected 0,
// naming the env variable when it set the value and the flag otherwise.
func checkPositive(d time.Duration, flagName, envName, envValue string) error {
	if d > 0 {
		return nil
	}
	if envValue != "" {
		return fmt.Errorf("%s: must be greater than 0", envName)
	}
	return fmt.Errorf("invalid value %q for flag -%s: must be greater than 0", (*config.Seconds)(&d).String(), flagName)
}
