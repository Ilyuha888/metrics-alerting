package agent

import (
	"log"
	"time"
)

type reporter interface {
	Send(Snapshot) error
}

type Agent struct {
	collector    *Collector
	reporter     reporter
	pollInterval time.Duration
	everyN       int
}

// New reports once every everyN polls; a reportInterval that is not a multiple of
// pollInterval rounds down.
func New(c *Collector, r reporter, pollInterval, reportInterval time.Duration) *Agent {
	everyN := int(reportInterval / pollInterval)
	if everyN < 1 {
		everyN = 1
	}
	return &Agent{collector: c, reporter: r, pollInterval: pollInterval, everyN: everyN}
}

// Run never returns. One goroutine does both jobs, which is why the collector needs no
// lock; splitting them wants a mutex, and that waits for the sprint teaching it.
func (a *Agent) Run() {
	for i := 1; ; i++ {
		time.Sleep(a.pollInterval)
		a.step(i)
	}
}

func (a *Agent) step(i int) {
	a.collector.Poll()
	if i%a.everyN != 0 {
		return
	}

	snap := a.collector.Snapshot()
	if err := a.reporter.Send(snap); err != nil {
		// The server may not be up yet; keep the polls for the next report.
		log.Println(err)
		return
	}
	a.collector.Reported(snap)
}
