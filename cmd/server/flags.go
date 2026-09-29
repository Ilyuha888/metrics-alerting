package main

import (
	"flag"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
)

type flags struct {
	address config.NetAddress
}

func parseFlags(args []string) (flags, error) {

	var f flags
	fs := flag.NewFlagSet("server", flag.ContinueOnError)

	f.address = config.DefaultAddress()
	fs.Var(&f.address, "a", "Net address host:port")
	err := fs.Parse(args)

	return f, err
}
