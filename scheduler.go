package main

import (
	"context"
	"os"
	"sync"
	"time"

	"proxy-box/internal/configstore"
)

const (
	schedulerTickDefault = 30 * time.Second
	backoffStart         = time.Minute
	backoffMax           = time.Hour
)

// subFlight lets one update run per subscription id.
// A manual update and the scheduler share one flight.
type subFlight struct {
	mu   sync.Mutex
	busy map[string]struct{}
}

func newSubFlight() *subFlight {
	return &subFlight{busy: map[string]struct{}{}}
}

func (f *subFlight) try(id string) bool {
	if f == nil || id == "" {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.busy[id]; ok {
		return false
	}
	f.busy[id] = struct{}{}
	return true
}

func (f *subFlight) done(id string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	delete(f.busy, id)
	f.mu.Unlock()
}

// beginSub claims id. The returned function releases it.
// A nil function means another update of that subscription is running.
func (a *appRuntime) beginSub(id string) func() {
	if a == nil || a.flight == nil {
		return func() {}
	}
	if !a.flight.try(id) {
		return nil
	}
	return func() { a.flight.done(id) }
}

type backoffHold struct {
	wait  time.Duration
	until time.Time
}

func nextBackoff(prev time.Duration) time.Duration {
	if prev <= 0 {
		return backoffStart
	}
	next := prev * 2
	if next > backoffMax {
		return backoffMax
	}
	return next
}

// subDue reports whether a scheduled update should run.
// UpdateIntervalMin 0 and a disabled subscription are never due.
// hold.until blocks retries after a failure, including when UpdatedAt is zero.
func subDue(sub configstore.Subscription, now time.Time, hold backoffHold) bool {
	if !sub.Enabled || sub.UpdateIntervalMin <= 0 {
		return false
	}
	if !hold.until.IsZero() && now.Before(hold.until) {
		return false
	}
	if sub.UpdatedAt.IsZero() {
		return true
	}
	return now.Sub(sub.UpdatedAt) >= time.Duration(sub.UpdateIntervalMin)*time.Minute
}

type subScheduler struct {
	now    func() time.Time
	tick   time.Duration
	load   func() ([]configstore.Subscription, error)
	update func(context.Context, configstore.Subscription) (configstore.Subscription, error)
	after  func()
	flight *subFlight

	mu    sync.Mutex
	holds map[string]backoffHold

	stopOnce sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

func newSubScheduler(flight *subFlight) *subScheduler {
	if flight == nil {
		flight = newSubFlight()
	}
	return &subScheduler{
		now:    time.Now,
		tick:   schedulerTickDefault,
		flight: flight,
		holds:  map[string]backoffHold{},
	}
}

func (s *subScheduler) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

// RunOnce updates every due subscription. A busy id is skipped and does not change its backoff.
func (s *subScheduler) RunOnce(ctx context.Context) {
	if s == nil || s.load == nil || s.update == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	subs, err := s.load()
	if err != nil || ctx.Err() != nil {
		return
	}
	ran := false
	for _, sub := range subs {
		if ctx.Err() != nil {
			break
		}
		now := s.clock()
		s.mu.Lock()
		hold := s.holds[sub.ID]
		due := subDue(sub, now, hold)
		s.mu.Unlock()
		if !due {
			continue
		}
		if !s.flight.try(sub.ID) {
			continue
		}
		_, upErr := s.update(ctx, sub)
		s.flight.done(sub.ID)
		finished := s.clock()
		s.mu.Lock()
		if upErr != nil {
			wait := nextBackoff(s.holds[sub.ID].wait)
			s.holds[sub.ID] = backoffHold{wait: wait, until: finished.Add(wait)}
		} else {
			delete(s.holds, sub.ID)
		}
		s.mu.Unlock()
		ran = true
	}
	if ran && s.after != nil {
		s.after()
	}
}

func (s *subScheduler) Start() {
	if s == nil || s.cancel != nil {
		return
	}
	if s.tick <= 0 {
		s.tick = schedulerTickDefault
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(s.tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.RunOnce(ctx)
			}
		}
	}()
}

func (s *subScheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		if s.done != nil {
			<-s.done
		}
	})
}

func schedulerTick() time.Duration {
	raw := os.Getenv("PROXYBOX_SCHEDULER_TICK")
	if raw == "" {
		return schedulerTickDefault
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < time.Second {
		return schedulerTickDefault
	}
	return d
}

func (a *appRuntime) broadcastSubs() {
	if a == nil || a.hub == nil {
		return
	}
	a.hub.broadcast(sseEvent{Type: "subs"})
}

func startSubScheduler(a *appRuntime) *subScheduler {
	if a != nil && a.flight == nil {
		a.flight = newSubFlight()
	}
	flight := (*subFlight)(nil)
	if a != nil {
		flight = a.flight
	}
	s := newSubScheduler(flight)
	s.tick = schedulerTick()
	s.load = configstore.LoadSubscriptions
	s.update = func(ctx context.Context, sub configstore.Subscription) (configstore.Subscription, error) {
		next, err := a.updateSubscription(ctx, sub)
		if err != nil {
			return next, err
		}
		_, _ = a.reloadSubs(ctx)
		return next, nil
	}
	s.after = a.broadcastSubs
	s.Start()
	return s
}
