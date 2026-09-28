package config_test

import (
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetAddress_Set(t *testing.T) {
	tests := []struct {
		name      string
		flagValue string
		want      config.NetAddress
		wantErr   string // a fragment of the expected message; empty means no error
	}{
		{name: "host and port", flagValue: "example.com:60", want: config.NetAddress{Host: "example.com", Port: 60}},
		{name: "port only", flagValue: ":65535", want: config.NetAddress{Host: "", Port: 65535}},
		{name: "IPv6 host loses its brackets", flagValue: "[::1]:8080", want: config.NetAddress{Host: "::1", Port: 8080}},
		{name: "empty value", flagValue: "", want: config.DefaultAddress(), wantErr: "missing port in address"},
		{name: "empty port", flagValue: "example.com:", want: config.DefaultAddress(), wantErr: `port ""`},
		{name: "port is not a number", flagValue: ":abc", want: config.DefaultAddress(), wantErr: `port "abc"`},
		{name: "port out of range 1", flagValue: ":65536", want: config.DefaultAddress(), wantErr: "outside 0-65535"},
		{name: "port out of range 2", flagValue: ":-1", want: config.DefaultAddress(), wantErr: "outside 0-65535"},
		{name: "too many colons", flagValue: "https/:xyz.com:8080", want: config.DefaultAddress(), wantErr: "too many colons"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adr := config.DefaultAddress()

			err := adr.Set(tt.flagValue)

			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
			assert.Equal(t, tt.want, adr)
		})
	}
}

func TestNetAddress_String(t *testing.T) {

	tests := []struct {
		name      string
		flagValue string
		want      string
	}{
		{name: "host and port", flagValue: "example.com:60", want: "example.com:60"},
		{name: "port only", flagValue: ":8080", want: ":8080"},
		{name: "IPv6 host gets its brackets back", flagValue: "[::1]:8080", want: "[::1]:8080"},
	}
	for _, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {
			adr := config.DefaultAddress()
			err := adr.Set(tt.flagValue)

			require.NoError(t, err)
			assert.Equal(t, tt.want, adr.String())
		})
	}
}
