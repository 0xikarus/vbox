package railway

import "time"

// Back off only while the exact submitted deployment makes no visible progress.
// A status transition restores prompt polling; cancellation and readiness deadlines
// remain controlled by the caller. No deployment mutation is retried here.
type deploymentPoll struct {
	base   time.Duration
	delay  time.Duration
	status string
}

// Volume attachment and deletion can remain pending for minutes. Back off
// unchanged observations so one lifecycle operation cannot consume the shared
// hourly request budget with a two-second status query throughout its timeout.
type volumePoll struct {
	base  time.Duration
	delay time.Duration
}

func (p *volumePoll) next() time.Duration {
	if p.delay == 0 {
		p.delay = p.base
	} else {
		cap := max(p.base, 20*time.Second)
		if p.delay >= cap/2 {
			p.delay = cap
		} else {
			p.delay *= 2
		}
	}
	return p.delay
}

func (p *deploymentPoll) next(status string) time.Duration {
	if p.delay == 0 || status != p.status {
		p.delay = p.base
	} else {
		cap := max(p.base, 10*time.Second)
		if p.delay >= cap/2 {
			p.delay = cap
		} else {
			p.delay *= 2
		}
	}
	p.status = status
	return p.delay
}
