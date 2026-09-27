package main

import (
	"flag"
	"testing"

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
			want: flags{address: config.DefaultAddress},
		},
		{
			name: "number address",
			args: []string{"-a", "127.0.0.1:1234"},
			want: flags{address: config.NetAddress{Host: "127.0.0.1", Port: 1234}},
		},
		{
			name: "text address",
			args: []string{"-a", "example.com:1234"},
			want: flags{address: config.NetAddress{Host: "example.com", Port: 1234}},
		},
		{
			name:    "agent-only flag -r is unknown",
			args:    []string{"-r", "40", "-a", "example.com:1234"},
			wantErr: "flag provided but not defined",
		},
		{name: "address without port", args: []string{"-a", "example.com"}, wantErr: "missing port in address"},
		{name: "port out of range", args: []string{"-a", ":70000"}, wantErr: "outside 0-65535"},
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
