package main

import (
	"flag"
	"log/slog"
	"os"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseParams(t *testing.T) {
	tests := []struct {
		name string
		args []string
		// A variable missing from env is unset for the case, so the developer's own ADDRESS cannot leak in.
		env     map[string]string
		want    params
		wantErr string
	}{
		{
			name: "defaults",
			args: nil,
			want: params{address: config.DefaultAddress(), logLevel: slog.LevelInfo},
		},
		{
			name: "debug log level",
			args: []string{"-l", "debug"},
			want: params{address: config.DefaultAddress(), logLevel: slog.LevelDebug},
		},
		{name: "unknown log level", args: []string{"-l", "loud"}, wantErr: `invalid value "loud" for flag -l`},
		{
			name: "number address",
			args: []string{"-a", "127.0.0.1:1234"},
			want: params{address: config.NetAddress{Host: "127.0.0.1", Port: 1234}},
		},
		{
			name: "text address",
			args: []string{"-a", "example.com:1234"},
			want: params{address: config.NetAddress{Host: "example.com", Port: 1234}},
		},
		{
			name:    "agent-only flag -r is unknown",
			args:    []string{"-r", "40", "-a", "example.com:1234"},
			wantErr: "flag provided but not defined",
		},
		{name: "address without port", args: []string{"-a", "example.com"}, wantErr: "missing port in address"},
		{name: "port out of range", args: []string{"-a", ":70000"}, wantErr: "outside 0-65535"},
		{
			name: "env address beats default",
			env:  map[string]string{"ADDRESS": "example.com:5678"},
			want: params{address: config.NetAddress{Host: "example.com", Port: 5678}},
		},
		{
			name: "env address beats flag",
			args: []string{"-a", "example.com:1234"},
			env:  map[string]string{"ADDRESS": "env.example.com:5678"},
			want: params{address: config.NetAddress{Host: "env.example.com", Port: 5678}},
		},
		{
			name: "empty env address behaves as unset",
			args: []string{"-a", "example.com:1234"},
			env:  map[string]string{"ADDRESS": ""},
			want: params{address: config.NetAddress{Host: "example.com", Port: 1234}},
		},
		{
			name:    "invalid env address names the variable",
			args:    []string{"-a", "example.com:1234"},
			env:     map[string]string{"ADDRESS": "bad"},
			wantErr: "ADDRESS: ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"ADDRESS"} {
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

// run tells -h apart with ==, so the help error must come back as flag.ErrHelp itself.
func TestParseParams_Help_ReturnsErrHelp(t *testing.T) {
	_, err := parseParams([]string{"-h"})

	assert.Equal(t, flag.ErrHelp, err)
}
