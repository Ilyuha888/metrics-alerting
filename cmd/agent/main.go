package main

import (
	"log"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/agent"
)

const (
	serverEndpoint = "http://localhost:8080"
	pollInterval   = 2 * time.Second
	reportInterval = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	c := agent.NewCollector()
	s := agent.NewSender(serverEndpoint)
	agent.New(c, s, pollInterval, reportInterval).Run()
	return nil
}
