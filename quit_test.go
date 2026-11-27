package main

import (
	"strings"
	"testing"
	"time"
)

type fakeQuitUI struct {
	run       bool
	order     *[]string
	terminate int
}

func (f *fakeQuitUI) Dispatch(fn func()) {
	if f != nil && f.run && fn != nil {
		fn()
	}
}

func (f *fakeQuitUI) Terminate() {
	if f == nil {
		return
	}
	f.terminate++
	if f.order != nil {
		*f.order = append(*f.order, "terminate")
	}
}

func TestQuitShutsDownBeforeTerminateOnce(t *testing.T) {
	var order []string
	ui := &fakeQuitUI{run: true, order: &order}
	q := &quitSeq{
		shutdown: func() { order = append(order, "shutdown") },
		cleanup:  func() { order = append(order, "cleanup") },
		release:  func() { order = append(order, "release") },
		win:      func() uiQuitter { return ui },
		exit:     func(int) { order = append(order, "exit") },
		timeout:  time.Second,
	}
	q.run()
	q.run()
	if strings.Join(order, ",") != "shutdown,cleanup,terminate" {
		t.Fatalf("order = %v terminate %d", order, ui.terminate)
	}
	if ui.terminate != 1 {
		t.Fatalf("terminate %d", ui.terminate)
	}
}

func TestQuitExitsWhenWindowIgnoresTerminate(t *testing.T) {
	released := false
	exited := false
	ui := &fakeQuitUI{run: false}
	q := &quitSeq{
		shutdown: func() {},
		cleanup:  func() {},
		release:  func() { released = true },
		win:      func() uiQuitter { return ui },
		exit: func(code int) {
			if code != 0 {
				t.Fatalf("code %d", code)
			}
			exited = true
		},
		timeout: 20 * time.Millisecond,
	}
	q.run()
	if !released || !exited {
		t.Fatalf("released %v exited %v", released, exited)
	}
	if ui.terminate != 0 {
		t.Fatalf("terminate %d", ui.terminate)
	}
}
