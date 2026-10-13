package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
	"github.com/caarlos0/env/v11"
)

type seconds time.Duration

func (s *seconds) String() string {
	sec := strconv.Itoa(int(time.Duration(*s) / time.Second))
	return sec
}

func (s *seconds) Set(s2 string) error {
	sec, err := strconv.Atoi(s2)
	if err != nil {
		return err
	}
	if sec <= 0 {
		return errors.New("must be greater than 0")
	}

	*s = seconds(time.Duration(sec) * time.Second)
	return nil
}

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
	fs.Var((*seconds)(&p.reportInterval), "r", "Reporting interval in seconds")
	fs.Var((*seconds)(&p.pollInterval), "p", "Polling interval in seconds")
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
		if err := (*seconds)(&p.reportInterval).Set(e.ReportInterval); err != nil {
			return p, fmt.Errorf("REPORT_INTERVAL: %w", err)
		}
	}
	if e.PollInterval != "" {
		if err := (*seconds)(&p.pollInterval).Set(e.PollInterval); err != nil {
			return p, fmt.Errorf("POLL_INTERVAL: %w", err)
		}
	}

	return p, nil
}
