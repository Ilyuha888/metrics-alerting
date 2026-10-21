package main

import (
	"flag"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseParams(t *testing.T) {
	tests := []struct {
		name string
		args []string
		// A variable missing from env is unset for the case, so the developer's own ADDRESS, REPORT_INTERVAL and POLL_INTERVAL cannot leak in.
		env     map[string]string
		want    params
		wantErr string
	}{
		{
			name: "defaults",
			args: nil,
			want: params{address: config.DefaultAddress(), reportInterval: 10 * time.Second, pollInterval: 2 * time.Second, logLevel: slog.LevelInfo},
		},
		{
			name: "debug log level",
			args: []string{"-l", "debug"},
			want: params{address: config.DefaultAddress(), reportInterval: 10 * time.Second, pollInterval: 2 * time.Second, logLevel: slog.LevelDebug},
		},
		{name: "unknown log level", args: []string{"-l", "loud"}, wantErr: `invalid value "loud" for flag -l`},
		{
			name: "number address",
			args: []string{"-a", "127.0.0.1:1234"},
			want: params{address: config.NetAddress{Host: "127.0.0.1", Port: 1234}, reportInterval: 10 * time.Second, pollInterval: 2 * time.Second},
		},
		{
			name: "text address",
			args: []string{"-a", "example.com:1234"},
			want: params{address: config.NetAddress{Host: "example.com", Port: 1234}, reportInterval: 10 * time.Second, pollInterval: 2 * time.Second},
		},
		{
			name: "report time + text address",
			args: []string{"-r", "40", "-a", "example.com:1234"},
			want: params{address: config.NetAddress{Host: "example.com", Port: 1234}, reportInterval: 40 * time.Second, pollInterval: 2 * time.Second},
		},
		{
			name: "poll interval",
			args: []string{"-p", "5"},
			want: params{address: config.DefaultAddress(), reportInterval: 10 * time.Second, pollInterval: 5 * time.Second},
		},
		{
			name: "every flag at once",
			args: []string{"-a", "example.com:9000", "-r", "20", "-p", "4"},
			want: params{address: config.NetAddress{Host: "example.com", Port: 9000}, reportInterval: 20 * time.Second, pollInterval: 4 * time.Second},
		},
		{name: "zero report interval", args: []string{"-r", "0"}, wantErr: "for flag -r: must be greater than 0"},
		{name: "negative report interval", args: []string{"-r", "-1"}, wantErr: "for flag -r: must not be negative"},
		{name: "zero poll interval", args: []string{"-p", "0"}, wantErr: "for flag -p: must be greater than 0"},
		{name: "negative poll interval", args: []string{"-p", "-1"}, wantErr: "for flag -p: must not be negative"},
		{name: "interval with a unit is not seconds", args: []string{"-r", "10s"}, wantErr: `invalid value "10s" for flag -r`},
		{name: "address without port", args: []string{"-a", "example.com"}, wantErr: "missing port in address"},
		{name: "unknown flag", args: []string{"-x"}, wantErr: "flag provided but not defined: -x"},
		{
			name: "every env var beats default",
			env:  map[string]string{"ADDRESS": "example.com:5678", "REPORT_INTERVAL": "30", "POLL_INTERVAL": "3"},
			want: params{address: config.NetAddress{Host: "example.com", Port: 5678}, reportInterval: 30 * time.Second, pollInterval: 3 * time.Second},
		},
		{
			name: "every env var beats its flag",
			args: []string{"-a", "example.com:9000", "-r", "20", "-p", "4"},
			env:  map[string]string{"ADDRESS": "env.example.com:5678", "REPORT_INTERVAL": "30", "POLL_INTERVAL": "3"},
			want: params{address: config.NetAddress{Host: "env.example.com", Port: 5678}, reportInterval: 30 * time.Second, pollInterval: 3 * time.Second},
		},
		{
			name: "env poll interval beats flag, other flags still apply",
			args: []string{"-a", "example.com:9000", "-r", "20", "-p", "4"},
			env:  map[string]string{"POLL_INTERVAL": "7"},
			want: params{address: config.NetAddress{Host: "example.com", Port: 9000}, reportInterval: 20 * time.Second, pollInterval: 7 * time.Second},
		},
		{
			name: "empty env vars behave as unset",
			args: []string{"-a", "example.com:9000", "-r", "20", "-p", "4"},
			env:  map[string]string{"ADDRESS": "", "REPORT_INTERVAL": "", "POLL_INTERVAL": ""},
			want: params{address: config.NetAddress{Host: "example.com", Port: 9000}, reportInterval: 20 * time.Second, pollInterval: 4 * time.Second},
		},
		{
			name:    "invalid env address names the variable",
			env:     map[string]string{"ADDRESS": "bad"},
			wantErr: "ADDRESS: ",
		},
		{
			name:    "zero env poll interval names the variable",
			env:     map[string]string{"POLL_INTERVAL": "0"},
			wantErr: "POLL_INTERVAL: must be greater than 0",
		},
		{
			name:    "non-numeric env report interval names the variable",
			env:     map[string]string{"REPORT_INTERVAL": "abc"},
			wantErr: `REPORT_INTERVAL: strconv.Atoi: parsing "abc"`,
		},
		{
			name:    "env interval with a unit is not seconds",
			env:     map[string]string{"REPORT_INTERVAL": "10s"},
			wantErr: `REPORT_INTERVAL: strconv.Atoi: parsing "10s"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"ADDRESS", "REPORT_INTERVAL", "POLL_INTERVAL"} {
				v, ok := tt.env[k]
				// Setenv first so the test restores the developer's value; then unset if the case omits it.
				t.Setenv(k, v)
				if !ok {
					require.NoError(t, os.Unsetenv(k))
				}
			}

			got, err := parseParams(tt.args)

			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

// run tells -h apart with errors.Is, so the help error must be flag.ErrHelp or wrap it.
func TestParseParams_Help_ReturnsErrHelp(t *testing.T) {
	_, err := parseParams([]string{"-h"})

	assert.ErrorIs(t, err, flag.ErrHelp)
}
