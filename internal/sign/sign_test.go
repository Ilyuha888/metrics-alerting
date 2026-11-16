package sign_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Ilyuha888/metrics-alerting/internal/sign"
	"github.com/stretchr/testify/assert"
)

const (
	rfcKey  = "Jefe"
	rfcData = "what do ya want for nothing?"
	rfcMAC  = "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
)

func TestSum_RFC4231Case2_MatchesTheVector(t *testing.T) {
	assert.Equal(t, rfcMAC, sign.Sum([]byte(rfcKey), []byte(rfcData)))
}

func TestValid_AcceptsOnlyTheMACOfTheseBytesUnderThisKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		data string
		got  string
		want bool
	}{
		{name: "RFC 4231 case 2 is valid", key: rfcKey, data: rfcData, got: rfcMAC, want: true},
		{name: "uppercase hex of the same MAC is valid", key: rfcKey, data: rfcData, got: strings.ToUpper(rfcMAC), want: true},
		{name: "one changed data byte", key: rfcKey, data: "what do ya want for nothing!", got: rfcMAC},
		{name: "another key", key: "Jeff", data: rfcData, got: rfcMAC},
		{name: "bad hex", key: rfcKey, data: rfcData, got: fmt.Sprintf("zz%s", rfcMAC[2:])},
		{name: "empty string", key: rfcKey, data: rfcData, got: ""},
		{name: "MAC cut by one byte", key: rfcKey, data: rfcData, got: rfcMAC[:len(rfcMAC)-2]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sign.Valid([]byte(tt.key), []byte(tt.data), tt.got))
		})
	}
}
