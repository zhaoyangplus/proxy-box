package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"proxy-box/internal/configstore"
)

func TestSubscriptionUpdateKeepsEditsAndDropsDeleted(t *testing.T) {
	useTempConfig(t)
	t.Setenv("LOCALAPPDATA", t.TempDir())

	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	arrived := 0
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrived++
		if arrived == 2 {
			once.Do(func() { close(ready) })
		}
		mu.Unlock()
		<-release
		_, _ = io.WriteString(w, "proxies:\n  - {name: alpha, type: http, server: 127.0.0.1, port: 1}\n")
	}))
	defer srv.Close()
	t.Cleanup(func() { once.Do(func() { close(release) }) })

	subs := []configstore.Subscription{
		{ID: "keep", Name: "old", URL: srv.URL, UserAgent: "ua", Filter: "keep-me", Enabled: true},
		{ID: "gone", Name: "gone", URL: srv.URL, Enabled: true},
	}
	if err := configstore.SaveSubscriptions(subs); err != nil {
		t.Fatal(err)
	}
	rt := &appRuntime{
		core:      &fakeCore{},
		coreDir:   t.TempDir(),
		proxyURLs: func() []string { return nil },
	}

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for _, sub := range subs {
		sub := sub
		go func() {
			defer wg.Done()
			_, err := rt.updateSubscription(context.Background(), sub)
			errCh <- err
		}()
	}
	<-ready

	edit := httptest.NewRequest(http.MethodPut, "/api/subs", strings.NewReader(`{"id":"keep","name":"renamed","enabled":false}`))
	editRec := httptest.NewRecorder()
	rt.updateSubFields(editRec, edit)
	if editRec.Code != http.StatusOK {
		t.Fatalf("edit %s", editRec.Body.String())
	}
	del := httptest.NewRequest(http.MethodDelete, "/api/subs?id=gone", nil)
	delRec := httptest.NewRecorder()
	rt.deleteSub(delRec, del)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete %s", delRec.Body.String())
	}

	close(release)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	stored, err := configstore.LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored = %+v", stored)
	}
	got := stored[0]
	if got.ID != "keep" || got.Name != "renamed" || got.Enabled || got.Filter != "keep-me" || got.UserAgent != "ua" || got.NodeCount != 1 || got.LastError != "" {
		t.Fatalf("stored = %+v", got)
	}
}
