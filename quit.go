package main

import (
	"sync"
	"time"
)

// uiQuitter is the UI thread's window. Terminate only ends the message loop on that thread.
type uiQuitter interface {
	Dispatch(func())
	Terminate()
}

// quitSeq shuts the runtime down, removes the tray, then asks the UI thread to exit.
// A second call does nothing. If the UI thread does not run Terminate before timeout, release runs and exit is called.
type quitSeq struct {
	once     sync.Once
	shutdown func()
	cleanup  func()
	release  func()
	win      func() uiQuitter
	exit     func(int)
	timeout  time.Duration
}

func (q *quitSeq) run() {
	if q == nil {
		return
	}
	q.once.Do(func() {
		if q.shutdown != nil {
			q.shutdown()
		}
		if q.cleanup != nil {
			q.cleanup()
		}
		var win uiQuitter
		if q.win != nil {
			win = q.win()
		}
		requestUITerminate(win, q.release, q.exit, q.timeout)
	})
}

func requestUITerminate(win uiQuitter, release func(), exit func(int), timeout time.Duration) {
	if release == nil {
		release = func() {}
	}
	if exit == nil {
		exit = func(int) {}
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if win == nil {
		release()
		exit(0)
		return
	}
	done := make(chan struct{})
	win.Dispatch(func() {
		win.Terminate()
		close(done)
	})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		release()
		exit(0)
	}
}
