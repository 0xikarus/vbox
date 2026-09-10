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
