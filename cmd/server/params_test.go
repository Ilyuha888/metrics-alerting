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
		// A variable missing from env is unset for the case, so the developer's own ADDRESS, STORE_INTERVAL, FILE_STORAGE_PATH and RESTORE cannot leak in.
		env     map[string]string
		want    params
		wantErr string
	}{
		{
			name: "defaults",
			args: nil,
			want: params{address: config.DefaultAddress(), logLevel: slog.LevelInfo, storeInterval: 300 * time.Second, filePath: "dumps/metrics.json", restore: true},
		},
		{
			name: "debug log level",
			args: []string{"-l", "debug"},
			want: params{address: config.DefaultAddress(), logLevel: slog.LevelDebug, storeInterval: 300 * time.Second, filePath: "dumps/metrics.json", restore: true},
		},
		{name: "unknown log level", args: []string{"-l", "loud"}, wantErr: `invalid value "loud" for flag -l`},
		{
			name: "number address",
			args: []string{"-a", "127.0.0.1:1234"},
			want: params{address: config.NetAddress{Host: "127.0.0.1", Port: 1234}, storeInterval: 300 * time.Second, filePath: "dumps/metrics.json", restore: true},
		},
		{
			name: "text address",
			args: []string{"-a", "example.com:1234"},
			want: params{address: config.NetAddress{Host: "example.com", Port: 1234}, storeInterval: 300 * time.Second, filePath: "dumps/metrics.json", restore: true},
		},
		{
			name:    "agent-only flag -p is unknown",
			args:    []string{"-p", "5", "-a", "example.com:1234"},
			wantErr: "flag provided but not defined",
		},
		{name: "address without port", args: []string{"-a", "example.com"}, wantErr: "missing port in address"},
		{name: "port out of range", args: []string{"-a", ":70000"}, wantErr: "outside 0-65535"},
		{
			name: "env address beats default",
			env:  map[string]string{"ADDRESS": "example.com:5678"},
			want: params{address: config.NetAddress{Host: "example.com", Port: 5678}, storeInterval: 300 * time.Second, filePath: "dumps/metrics.json", restore: true},
		},
		{
			name: "env address beats flag",
			args: []string{"-a", "example.com:1234"},
			env:  map[string]string{"ADDRESS": "env.example.com:5678"},
			want: params{address: config.NetAddress{Host: "env.example.com", Port: 5678}, storeInterval: 300 * time.Second, filePath: "dumps/metrics.json", restore: true},
		},
		{
			name: "empty env address behaves as unset",
			args: []string{"-a", "example.com:1234"},
			env:  map[string]string{"ADDRESS": ""},
			want: params{address: config.NetAddress{Host: "example.com", Port: 1234}, storeInterval: 300 * time.Second, filePath: "dumps/metrics.json", restore: true},
		},
		{
			name:    "invalid env address names the variable",
			args:    []string{"-a", "example.com:1234"},
			env:     map[string]string{"ADDRESS": "bad"},
			wantErr: "ADDRESS: ",
		},
		{
			name: "storage flags",
			args: []string{"-i", "10", "-f", "/tmp/m.json", "-r=false"},
			want: params{address: config.DefaultAddress(), storeInterval: 10 * time.Second, filePath: "/tmp/m.json", restore: false},
		},
		{
			name: "zero store interval means save on every update",
			args: []string{"-i", "0"},
			want: params{address: config.DefaultAddress(), storeInterval: 0, filePath: "dumps/metrics.json", restore: true},
		},
		{name: "negative store interval", args: []string{"-i", "-1"}, wantErr: "for flag -i: must not be negative"},
		{name: "store interval with a unit is not seconds", args: []string{"-i", "10s"}, wantErr: `invalid value "10s" for flag -i`},
		{
			name:    "bool flag value after a space is a stray argument",
			args:    []string{"-r", "false", "-f", "x.json"},
			wantErr: "unexpected arguments: [false -f x.json]",
		},
		{
			name: "every storage env var beats its flag",
			args: []string{"-i", "10", "-f", "flag.json", "-r=true"},
			env:  map[string]string{"STORE_INTERVAL": "20", "FILE_STORAGE_PATH": "env.json", "RESTORE": "false"},
			want: params{address: config.DefaultAddress(), storeInterval: 20 * time.Second, filePath: "env.json", restore: false},
		},
		{
			name: "env store interval 0 beats flag",
			args: []string{"-i", "10"},
			env:  map[string]string{"STORE_INTERVAL": "0"},
			want: params{address: config.DefaultAddress(), storeInterval: 0, filePath: "dumps/metrics.json", restore: true},
		},
		{
			name: "env restore true beats -r=false",
			args: []string{"-r=false"},
			env:  map[string]string{"RESTORE": "true"},
			want: params{address: config.DefaultAddress(), storeInterval: 300 * time.Second, filePath: "dumps/metrics.json", restore: true},
		},
		{
			name: "empty storage env vars behave as unset",
			args: []string{"-i", "10", "-f", "flag.json", "-r=false"},
			env:  map[string]string{"STORE_INTERVAL": "", "FILE_STORAGE_PATH": "", "RESTORE": ""},
			want: params{address: config.DefaultAddress(), storeInterval: 10 * time.Second, filePath: "flag.json", restore: false},
		},
		{
			name:    "negative env store interval names the variable",
			env:     map[string]string{"STORE_INTERVAL": "-5"},
			wantErr: "STORE_INTERVAL: must not be negative",
		},
		{
			name:    "invalid env restore names the variable",
			env:     map[string]string{"RESTORE": "yes"},
			wantErr: `RESTORE: strconv.ParseBool: parsing "yes"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"ADDRESS", "STORE_INTERVAL", "FILE_STORAGE_PATH", "RESTORE"} {
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
