package agent

import "time"

func (s *Sender) SetDelays(delays []time.Duration) {
	s.delays = delays
}
