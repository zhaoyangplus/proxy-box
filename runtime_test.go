package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"proxy-box/internal/core"
)

type fakeCore struct {
	log     *[]string
	running bool
	mode    string
	ports   core.Ports
	version string
}

func (f *fakeCore) Start(context.Context, string, core.ConfigParams, []core.ProviderSpec) (core.Status, error) {
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

func (f *fakeCore) Reload(context.Context, core.ConfigParams, []core.ProviderSpec) error {
	return nil
}

func (f *fakeCore) SwitchMode(_ context.Context, mode string) error {
	f.mode = mode
	return nil
}

func (f *fakeCore) Status() core.Status {
	return core.Status{
		Running: f.running,
		Mode:    f.mode,
		Ports:   f.ports,
		Version: f.version,
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
