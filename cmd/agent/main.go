package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/Ilyuha888/metrics-alerting/internal/agent"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {

	f, err := parseFlags(os.Args[1:])

	switch {
	case err == flag.ErrHelp:
		return nil
	case err != nil:
		return err
	}

	serverEndpoint := "http://" + f.address.String()
	pollInterval := f.pollInterval
	reportInterval := f.reportInterval
	fmt.Printf("Sending to %s\nPolling every %s\nReporting every %s\n", serverEndpoint, pollInterval, reportInterval)

	c := agent.NewCollector()
	s := agent.NewSender(serverEndpoint)
	agent.New(c, s, pollInterval, reportInterval).Run()
	return nil
}
