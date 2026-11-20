package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
)

type NetAddress struct {
	Host string
	Port int
}

func (adr *NetAddress) String() string {
	return net.JoinHostPort(adr.Host, strconv.Itoa(adr.Port))
}

func (adr *NetAddress) URL(scheme string) string {
	return (&url.URL{Scheme: scheme, Host: adr.String()}).String()
}

func (adr *NetAddress) Set(flagValue string) error {
	host, portStr, err := net.SplitHostPort(flagValue)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("port %q: %w", portStr, err)
	}
	if port < 0 || port > 65535 {
		return fmt.Errorf("port %d is outside 0-65535", port)
	}

	adr.Host = host
	adr.Port = port
	return nil
}

func DefaultAddress() NetAddress {
	return NetAddress{Host: "localhost", Port: 8080}
}
