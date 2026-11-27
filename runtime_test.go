package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"proxy-box/internal/core"
)

type fakeCore struct {
	log         *[]string
	running     bool
	mode        string
	ports       core.Ports
	version     string
	binary      string
	reloads     int
	failReloads int
	modes       []string
	started     core.ConfigParams
	last        core.ConfigParams
	seen        []core.ConfigParams
	onReload    func()
}

func (f *fakeCore) Start(_ context.Context, _ string, params core.ConfigParams, _ []core.ProviderSpec) (core.Status, error) {
	f.started = params
	f.running = true
	if f.mode == "" {
		f.mode = core.ModeRule
	}
	return f.Status(), nil
}

func (f *fakeCore) Stop() error {
	if f.log != nil {
		*f.log = append(*f.log, "stop")
	}
	f.running = false
	return nil
}

func (f *fakeCore) Reload(_ context.Context, params core.ConfigParams, _ []core.ProviderSpec) error {
	f.reloads++
	f.last = params
	f.seen = append(f.seen, params)
	var err error
	if f.failReloads > 0 {
		f.failReloads--
		err = errors.New("reload refused")
	}
	if f.onReload != nil {
		f.onReload()
	}
	return err
}

func (f *fakeCore) SwitchMode(_ context.Context, mode string) error {
	f.mode = mode
	f.modes = append(f.modes, mode)
	return nil
}

func (f *fakeCore) Status() core.Status {
	return core.Status{
		Running:    f.running,
		Mode:       f.mode,
		Ports:      f.ports,
		Version:    f.version,
		BinaryPath: f.binary,
	}
}

func TestShutdownRestoresProxyBeforeStop(t *testing.T) {
	var log []string
	rt := &appRuntime{
		restore: func() error {
			log = append(log, "restore")
			return nil
		},
		core: &fakeCore{log: &log},
	}
	rt.Shutdown()
	rt.Shutdown()
	if strings.Join(log, ",") != "restore,stop,restore,stop" {
		t.Fatalf("log = %v", log)
	}

	log = nil
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt.Shutdown()
		}()
	}
	wg.Wait()
	if len(log)%2 != 0 {
		t.Fatalf("log = %v", log)
	}
	for i := 0; i < len(log); i += 2 {
		if log[i] != "restore" || log[i+1] != "stop" {
			t.Fatalf("order = %v", log)
		}
	}
}
