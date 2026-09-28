package main

import (
	"flag"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    flags
		wantErr string
	}{
		{
			name: "defaults",
			args: nil,
			want: flags{address: config.DefaultAddress(), reportInterval: 10 * time.Second, pollInterval: 2 * time.Second},
		},
		{
			name: "number address",
			args: []string{"-a", "127.0.0.1:1234"},
			want: flags{address: config.NetAddress{Host: "127.0.0.1", Port: 1234}, reportInterval: 10 * time.Second, pollInterval: 2 * time.Second},
		},
		{
			name: "text address",
			args: []string{"-a", "example.com:1234"},
			want: flags{address: config.NetAddress{Host: "example.com", Port: 1234}, reportInterval: 10 * time.Second, pollInterval: 2 * time.Second},
		},
		{
			name: "report time + text address",
			args: []string{"-r", "40", "-a", "example.com:1234"},
			want: flags{address: config.NetAddress{Host: "example.com", Port: 1234}, reportInterval: 40 * time.Second, pollInterval: 2 * time.Second},
		},
		{
			name: "poll interval",
			args: []string{"-p", "5"},
			want: flags{address: config.DefaultAddress(), reportInterval: 10 * time.Second, pollInterval: 5 * time.Second},
		},
		{
			name: "every flag at once",
			args: []string{"-a", "example.com:9000", "-r", "20", "-p", "4"},
			want: flags{address: config.NetAddress{Host: "example.com", Port: 9000}, reportInterval: 20 * time.Second, pollInterval: 4 * time.Second},
		},
		{name: "zero report interval", args: []string{"-r", "0"}, wantErr: "for flag -r: must be greater than 0"},
		{name: "negative report interval", args: []string{"-r", "-1"}, wantErr: "for flag -r: must be greater than 0"},
		{name: "zero poll interval", args: []string{"-p", "0"}, wantErr: "for flag -p: must be greater than 0"},
		{name: "negative poll interval", args: []string{"-p", "-1"}, wantErr: "for flag -p: must be greater than 0"},
		{name: "interval with a unit is not seconds", args: []string{"-r", "10s"}, wantErr: `invalid value "10s" for flag -r`},
		{name: "address without port", args: []string{"-a", "example.com"}, wantErr: "missing port in address"},
		{name: "unknown flag", args: []string{"-x"}, wantErr: "flag provided but not defined: -x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFlags(tt.args)

			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

// run tells -h apart with ==, so the help error must come back as flag.ErrHelp itself.
func TestParseFlags_Help_ReturnsErrHelp(t *testing.T) {
	_, err := parseFlags([]string{"-h"})

	assert.Equal(t, flag.ErrHelp, err)
}
