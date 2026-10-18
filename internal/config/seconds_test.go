package config_test

import (
	"testing"
	"time"

	"github.com/Ilyuha888/metrics-alerting/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeconds_Set(t *testing.T) {
	tests := []struct {
		name      string
		flagValue string
		want      time.Duration
		wantErr   string // a fragment of the expected message; empty means no error
	}{
		{name: "zero is allowed", flagValue: "0", want: 0},
		{name: "whole seconds", flagValue: "300", want: 300 * time.Second},
		{name: "negative", flagValue: "-1", want: time.Minute, wantErr: "must not be negative"},
		{name: "fraction", flagValue: "1.5", want: time.Minute, wantErr: `parsing "1.5"`},
		{name: "duration with a unit", flagValue: "5s", want: time.Minute, wantErr: `parsing "5s"`},
		{name: "empty value", flagValue: "", want: time.Minute, wantErr: `parsing ""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := time.Minute

			err := (*config.Seconds)(&d).Set(tt.flagValue)

			if tt.wantErr == "" {
				require.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
			assert.Equal(t, tt.want, d)
		})
	}
}

func TestSeconds_String(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "zero", d: 0, want: "0"},
		{name: "whole seconds", d: 300 * time.Second, want: "300"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, (*config.Seconds)(&tt.d).String())
		})
	}
}
