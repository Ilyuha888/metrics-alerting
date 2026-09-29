package main

import (
	"errors"
	"flag"
	"strconv"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
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

type flags struct {
	address        config.NetAddress
	reportInterval time.Duration
	pollInterval   time.Duration
}

func parseFlags(args []string) (flags, error) {

	var f flags
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)

	f.address = config.DefaultAddress()
	f.pollInterval = 2 * time.Second
	f.reportInterval = 10 * time.Second

	fs.Var(&f.address, "a", "Net address host:port")
	fs.Var((*seconds)(&f.reportInterval), "r", "Reporting interval in seconds")
	fs.Var((*seconds)(&f.pollInterval), "p", "Polling interval in seconds")

	err := fs.Parse(args)

	if err != nil {
		return f, err
	}

	return f, nil
}
