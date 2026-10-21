package main

import (
	"errors"
	"flag"
	"log"
	"os"

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

	serverEndpoint := "http://" + p.address.String()
	pollInterval := p.pollInterval
	reportInterval := p.reportInterval
	lg := logger.New(p.logLevel)
	lg.Info("agent started", "server", serverEndpoint, "poll", pollInterval, "report", reportInterval)

	c := agent.NewCollector()
	s := agent.NewSender(serverEndpoint)
	agent.New(c, s, pollInterval, reportInterval, lg).Run()
	return nil
}
