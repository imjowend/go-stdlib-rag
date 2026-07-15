package main

import (
	"time"
)

// limiter enforces a sliding-60s-window ceiling on both tokens-per-minute and
// requests-per-minute, so we stay safely under Jina's free-tier limits.
type limiter struct {
	tpm, rpm int
	events   []event
}

type event struct {
	at     time.Time
	tokens int
}

func newLimiter(tpm, rpm int) *limiter {
	return &limiter{tpm: tpm, rpm: rpm}
}

// wait blocks until sending a request of the given token size keeps both the
// token and request counts within budget over the trailing 60 seconds.
func (l *limiter) wait(tokens int) {
	for {
		now := time.Now()
		l.prune(now)

		sumTokens := tokens
		for _, e := range l.events {
			sumTokens += e.tokens
		}
		reqCount := len(l.events) + 1

		if sumTokens <= l.tpm && reqCount <= l.rpm {
			l.events = append(l.events, event{at: now, tokens: tokens})
			return
		}

		// Sleep until the oldest event ages out of the window, then re-check.
		sleep := time.Until(l.events[0].at.Add(time.Minute)) + 50*time.Millisecond
		if sleep < 100*time.Millisecond {
			sleep = 100 * time.Millisecond
		}
		time.Sleep(sleep)
	}
}

func (l *limiter) prune(now time.Time) {
	cutoff := now.Add(-time.Minute)
	i := 0
	for i < len(l.events) && l.events[i].at.Before(cutoff) {
		i++
	}
	l.events = l.events[i:]
}
