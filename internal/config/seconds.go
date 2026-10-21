package config

import (
	"errors"
	"strconv"
	"time"
)

// Seconds is a time.Duration given on the command line as bare integer seconds ("300").
type Seconds time.Duration

func (s *Seconds) String() string {
	return strconv.Itoa(int(time.Duration(*s) / time.Second))
}

func (s *Seconds) Set(flagValue string) error {
	sec, err := strconv.Atoi(flagValue)
	if err != nil {
		return err
	}
	if sec < 0 {
		return errors.New("must not be negative")
	}

	*s = Seconds(time.Duration(sec) * time.Second)
	return nil
}
