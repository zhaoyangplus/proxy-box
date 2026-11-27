package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"proxy-box/internal/configstore"
)

func TestSubDue(t *testing.T) {
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	dueSub := configstore.Subscription{ID: "a", Enabled: true, UpdateIntervalMin: 1, UpdatedAt: now.Add(-time.Minute)}
	if !subDue(dueSub, now, backoffHold{}) {
		t.Fatal("elapsed interval should be due")
	}
	fresh := dueSub
	fresh.UpdatedAt = now.Add(-30 * time.Second)
	if subDue(fresh, now, backoffHold{}) {
		t.Fatal("half an interval is not due")
	}
	manual := dueSub
	manual.UpdateIntervalMin = 0
	if subDue(manual, now, backoffHold{}) {
		t.Fatal("interval 0 stays manual")
	}
	off := dueSub
	off.Enabled = false
	if subDue(off, now, backoffHold{}) {
		t.Fatal("disabled subscription is skipped")
	}
	never := dueSub
	never.UpdatedAt = time.Time{}
	if !subDue(never, now, backoffHold{}) {
		t.Fatal("zero UpdatedAt is due")
	}
	held := backoffHold{until: now.Add(time.Minute)}
	if subDue(never, now, held) {
		t.Fatal("backoff holds a subscription whose UpdatedAt is zero")
	}
	if !subDue(never, held.until, held) {
		t.Fatal("backoff expiry makes a zero UpdatedAt due again")
	}
}

func TestNextBackoffGrowsAndCaps(t *testing.T) {
	wait := time.Duration(0)
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour}
	for i, exp := range want {
		wait = nextBackoff(wait)
		if wait != exp {
			t.Fatalf("step %d = %s, want %s", i, wait, exp)
		}
	}
}

func TestSchedulerRunsDueSubscriptionsAndBacksOff(t *testing.T) {
	start := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	now := start
	subs := []configstore.Subscription{
		{ID: "off", Enabled: false, UpdateIntervalMin: 1},
		{ID: "manual", Enabled: true, UpdateIntervalMin: 0},
		{ID: "fresh", Enabled: true, UpdateIntervalMin: 1, UpdatedAt: start.Add(-30 * time.Second)},
		{ID: "due", Enabled: true, UpdateIntervalMin: 10, UpdatedAt: start.Add(-10 * time.Minute)},
		{ID: "new", Enabled: true, UpdateIntervalMin: 1},
	}
	fail := map[string]bool{"new": true}
	var calls []string
	var events int
	s := newSubScheduler(nil)
	s.now = func() time.Time { return now }
	s.load = func() ([]configstore.Subscription, error) {
		out := make([]configstore.Subscription, len(subs))
		copy(out, subs)
		for i := range out {
			if out[i].ID == "fresh" {
				out[i].UpdatedAt = now.Add(-30 * time.Second)
			}
		}
		return out, nil
	}
	s.update = func(_ context.Context, sub configstore.Subscription) (configstore.Subscription, error) {
		calls = append(calls, sub.ID)
		if fail[sub.ID] {
			sub.LastError = "订阅下载失败，服务器返回错误"
			replaceSub(subs, sub)
			return sub, errors.New("subscription status 500")
		}
		sub.LastError = ""
		sub.UpdatedAt = now
		sub.NodeCount = 2
		replaceSub(subs, sub)
		return sub, nil
	}
	s.after = func() { events++ }

	s.RunOnce(context.Background())
	if strings.Join(calls, ",") != "due,new" {
		t.Fatalf("first calls = %v", calls)
	}
	if events != 1 {
		t.Fatalf("events %d", events)
	}
	if subs[4].LastError == "" || !subs[4].UpdatedAt.IsZero() {
		t.Fatalf("failed sub = %+v", subs[4])
	}
	if subs[3].NodeCount != 2 || subs[3].LastError != "" {
		t.Fatalf("success sub = %+v", subs[3])
	}

	now = start.Add(30 * time.Second)
	calls = nil
	s.RunOnce(context.Background())
	if len(calls) != 0 || events != 1 {
		t.Fatalf("early retry calls %v events %d", calls, events)
	}

	now = start.Add(time.Minute)
	s.RunOnce(context.Background())
	if strings.Join(calls, ",") != "new" {
		t.Fatalf("second failure calls = %v", calls)
	}
	if s.holds["new"].wait != 2*time.Minute {
		t.Fatalf("backoff = %s", s.holds["new"].wait)
	}

	now = start.Add(2 * time.Minute)
	calls = nil
	s.RunOnce(context.Background())
	if len(calls) != 0 {
		t.Fatalf("inside doubled backoff calls %v", calls)
	}

	fail["new"] = false
	now = start.Add(3 * time.Minute)
	s.RunOnce(context.Background())
	if strings.Join(calls, ",") != "new" {
		t.Fatalf("reset calls = %v", calls)
	}
	if _, ok := s.holds["new"]; ok {
		t.Fatal("success left a backoff hold")
	}
	if subs[4].LastError != "" || subs[4].NodeCount != 2 {
		t.Fatalf("recovered sub = %+v", subs[4])
	}

	now = subs[4].UpdatedAt.Add(30 * time.Second)
	calls = nil
	s.RunOnce(context.Background())
	if len(calls) != 0 {
		t.Fatalf("after reset, half interval calls %v", calls)
	}
	now = subs[4].UpdatedAt.Add(time.Minute)
	s.RunOnce(context.Background())
	if strings.Join(calls, ",") != "new" {
		t.Fatalf("next interval calls = %v", calls)
	}
}

func TestSchedulerSkipsBusySubscription(t *testing.T) {
	flight := newSubFlight()
	if !flight.try("due") {
		t.Fatal("claim")
	}
	called := false
	s := newSubScheduler(flight)
	s.now = func() time.Time { return time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC) }
	s.load = func() ([]configstore.Subscription, error) {
		return []configstore.Subscription{{ID: "due", Enabled: true, UpdateIntervalMin: 1}}, nil
	}
	s.update = func(context.Context, configstore.Subscription) (configstore.Subscription, error) {
		called = true
		return configstore.Subscription{}, nil
	}
	s.RunOnce(context.Background())
	if called {
		t.Fatal("ran while the manual update held the subscription")
	}
	flight.done("due")
	s.RunOnce(context.Background())
	if !called {
		t.Fatal("did not run after the manual update released the subscription")
	}
}

func TestSchedulerLoopFiresUntilStopped(t *testing.T) {
	var n atomic.Int32
	s := newSubScheduler(nil)
	s.tick = 150 * time.Millisecond
	s.load = func() ([]configstore.Subscription, error) {
		return []configstore.Subscription{{ID: "a", Enabled: true, UpdateIntervalMin: 1}}, nil
	}
	s.update = func(context.Context, configstore.Subscription) (configstore.Subscription, error) {
		n.Add(1)
		return configstore.Subscription{ID: "a"}, nil
	}
	s.Start()
	deadline := time.Now().Add(2 * time.Second)
	for n.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	s.Stop()
	if n.Load() < 2 {
		t.Fatalf("scheduler loop ran %d updates", n.Load())
	}
}

func TestSchedulerStopCancelsInFlightUpdate(t *testing.T) {
	entered := make(chan struct{})
	s := newSubScheduler(nil)
	s.tick = 5 * time.Millisecond
	s.load = func() ([]configstore.Subscription, error) {
		return []configstore.Subscription{{ID: "due", Enabled: true, UpdateIntervalMin: 1}}, nil
	}
	s.update = func(ctx context.Context, sub configstore.Subscription) (configstore.Subscription, error) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-ctx.Done()
		return sub, ctx.Err()
	}
	s.Start()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("update did not start")
	}
	stopped := make(chan struct{})
	go func() {
		s.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return")
	}
}

func TestSchedulerTickStaysDefault(t *testing.T) {
	useTempConfig(t)
	t.Setenv("PROXYBOX_SCHEDULER_TICK", "5s")
	s := startSubScheduler(&appRuntime{flight: newSubFlight()})
	t.Cleanup(s.Stop)
	if s.tick != 30*time.Second {
		t.Fatalf("tick %s", s.tick)
	}
}

func TestBroadcastSubs(t *testing.T) {
	hub := newSSEHub()
	ch := hub.subscribe()
	defer hub.unsubscribe(ch)
	(&appRuntime{hub: hub}).broadcastSubs()
	select {
	case raw := <-ch:
		if string(raw) != `{"type":"subs","shouldDiag":false}` && !strings.Contains(string(raw), `"type":"subs"`) {
			t.Fatalf("event %s", raw)
		}
	case <-time.After(time.Second):
		t.Fatal("no subs event")
	}
}

func replaceSub(subs []configstore.Subscription, sub configstore.Subscription) {
	for i := range subs {
		if subs[i].ID == sub.ID {
			subs[i] = sub
			return
		}
	}
}
