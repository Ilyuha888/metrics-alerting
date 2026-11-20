package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Ilyuha888/metrics-alerting/internal/agent"
	"github.com/Ilyuha888/metrics-alerting/internal/logger"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {

	p, err := parseParams(os.Args[1:])

	switch {
	case errors.Is(err, flag.ErrHelp):
		return nil
	case err != nil:
		return err
	}

	serverEndpoint := p.address.URL("http")
	pollInterval := p.pollInterval
	reportInterval := p.reportInterval
	lg := logger.New(p.logLevel)
	lg.Info("agent started", "server", serverEndpoint, "poll", pollInterval, "report", reportInterval, "rate_limit", p.rateLimit)

	cfg := agent.Config{PollInterval: pollInterval, ReportInterval: reportInterval, RateLimit: p.rateLimit}
	s := agent.NewSender(serverEndpoint, []byte(p.key))
	a, err := agent.New(agent.NewCollector(), agent.NewSystemCollector(), s, cfg, lg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.Run(ctx)
	return nil
}
