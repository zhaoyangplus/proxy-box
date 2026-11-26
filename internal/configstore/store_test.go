package configstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	real := filepath.Join(home, ".proxy-box")
	before := []string{"config.json", "core.json", "subscriptions.json", "state.json"}
	stamps := map[string]fileStamp{}
	for _, name := range before {
		stamps[name] = stampFile(filepath.Join(real, name))
	}

	dir, err := os.MkdirTemp("", "proxy-box-configstore-")
	if err != nil {
		panic(err)
	}
	SetBaseDir(dir)
	code := m.Run()
	os.RemoveAll(dir)

	for _, name := range before {
		if stamps[name] != stampFile(filepath.Join(real, name)) {
			os.Stderr.WriteString("test wrote " + filepath.Join(real, name) + "\n")
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}

type fileStamp struct {
	exists bool
	size   int64
	mod    int64
}

func sameSubscriptions(got, want []Subscription) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		g, w := got[i], want[i]
		if !g.UpdatedAt.Equal(w.UpdatedAt) {
			return false
		}
		g.UpdatedAt = w.UpdatedAt
		switch {
		case g.Usage == nil && w.Usage == nil:
		case g.Usage == nil || w.Usage == nil || *g.Usage != *w.Usage:
			return false
		}
		g.Usage = w.Usage
		if g != w {
			return false
		}
	}
	return true
}

func stampFile(path string) fileStamp {
	st, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, size: st.Size(), mod: st.ModTime().UnixNano()}
}

func useDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetBaseDir(dir)
	return dir
}

func TestLoadCoreSettingsCreatesStableSecret(t *testing.T) {
	dirA := useDir(t)
	first, err := LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if first.MixedPort != 27890 || first.ControllerPort != 27891 || first.AIDedicatedPort != 27892 {
		t.Fatalf("ports = %d %d %d", first.MixedPort, first.ControllerPort, first.AIDedicatedPort)
	}
	if first.Mode != "rule" {
		t.Fatalf("mode = %q", first.Mode)
	}
	if first.Enabled || first.BypassCN || first.SystemProxy {
		t.Fatalf("zero flags were set: %+v", first)
	}
	if len(first.Secret) < 32 {
		t.Fatalf("secret = %q", first.Secret)
	}

	second, err := LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if second.Secret != first.Secret {
		t.Fatalf("secret changed %q -> %q", first.Secret, second.Secret)
	}

	raw, err := os.ReadFile(filepath.Join(dirA, "core.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(first.Secret)) {
		t.Fatal("core.json does not contain the secret")
	}
	if _, err := os.Stat(filepath.Join(dirA, "subscriptions.json")); !os.IsNotExist(err) {
		t.Fatal("LoadCoreSettings created subscriptions.json")
	}
	if _, err := os.Stat(filepath.Join(dirA, "config.json")); !os.IsNotExist(err) {
		t.Fatal("LoadCoreSettings created config.json")
	}

	dirB := t.TempDir()
	SetBaseDir(dirB)
	other, err := LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if other.Secret == first.Secret {
		t.Fatal("two new directories got the same secret")
	}
}

func TestConcurrentFirstLoadSharesSecret(t *testing.T) {
	useDir(t)
	const n = 8
	secrets := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cfg, err := LoadCoreSettings()
			errs[i] = err
			secrets[i] = cfg.Secret
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if secrets[i] == "" || secrets[i] != secrets[0] {
			t.Fatalf("secret[%d] = %q, secret[0] = %q", i, secrets[i], secrets[0])
		}
	}
}

func TestSubscriptionsRoundTripStaysOutOfCoreFile(t *testing.T) {
	dir := useDir(t)
	core, err := LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	updated := time.Date(2026, 11, 26, 6, 7, 8, 0, time.UTC)
	subs := []Subscription{
		{
			ID:                "sub_a",
			Name:              "甲",
			URL:               "https://example.com/a?x=1&y=2",
			UserAgent:         "ua-1",
			Filter:            "vmess|香港",
			Enabled:           true,
			UpdateIntervalMin: 60,
			UpdatedAt:         updated,
			LastError:         "boom",
			NodeCount:         4,
			Usage:             &SubUsage{Upload: 1, Download: 2, Total: 3, Expire: 4},
		},
		{
			ID:                "sub-b",
			Name:              "乙",
			URL:               "https://example.com/b",
			Enabled:           false,
			UpdateIntervalMin: 0,
			NodeCount:         0,
		},
	}
	if err := SaveSubscriptions(subs); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if !sameSubscriptions(got, subs) {
		t.Fatalf("subscriptions = %+v", got)
	}

	again, err := LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if again.Secret != core.Secret {
		t.Fatal("saving subscriptions changed the core secret")
	}

	core.MixedPort = 27990
	if err := SaveCoreSettings(core); err != nil {
		t.Fatal(err)
	}
	got, err = LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if !sameSubscriptions(got, subs) {
		t.Fatal("saving core settings changed subscriptions")
	}

	coreRaw, err := os.ReadFile(filepath.Join(dir, "core.json"))
	if err != nil {
		t.Fatal(err)
	}
	subRaw, err := os.ReadFile(filepath.Join(dir, "subscriptions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(coreRaw, []byte("example.com")) {
		t.Fatal("core.json contains a subscription url")
	}
	if bytes.Contains(subRaw, []byte(core.Secret)) {
		t.Fatal("subscriptions.json contains the core secret")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(err) {
		t.Fatal("subscription store created config.json")
	}
}

func TestSaveNilSubscriptionsWritesEmptyArray(t *testing.T) {
	dir := useDir(t)
	if err := SaveSubscriptions(nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "subscriptions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Fatalf("subscriptions.json = %s", raw)
	}
	got, err := LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestCorruptCoreFileIsLeftInPlace(t *testing.T) {
	dir := useDir(t)
	path := filepath.Join(dir, "core.json")
	if err := os.WriteFile(path, []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCoreSettings(); err == nil {
		t.Fatal("expected error")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{" {
		t.Fatalf("core.json = %s", raw)
	}
}

func TestAppConfigStillUsesConfigJSON(t *testing.T) {
	dir := useDir(t)
	core, err := LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig()
	if cfg.PreferredClient != "auto" || !cfg.MinimizeToTray || !cfg.IsAutoOptimizeEnabled() || !cfg.IsAutoSyncEnabled() {
		t.Fatalf("defaults = %+v", cfg)
	}
	cfg.PreferredClient = "clash_mi"
	cfg.ExcludeRegionsRegex = "HK"
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded := LoadConfig()
	if loaded.PreferredClient != "clash_mi" || loaded.ExcludeRegionsRegex != "HK" {
		t.Fatalf("loaded = %+v", loaded)
	}
	again, err := LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if again.Secret != core.Secret {
		t.Fatal("SaveConfig changed the core secret")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["secret"]; ok {
		t.Fatal("config.json contains secret")
	}
	if _, ok := decoded["url"]; ok {
		t.Fatal("config.json contains a subscription url")
	}
}

func TestNewSubscriptionIDCharset(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 64; i++ {
		id, err := NewSubscriptionID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) < 8 {
			t.Fatalf("id %q is short", id)
		}
		for _, r := range id {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			default:
				t.Fatalf("id %q has %q", id, r)
			}
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestConcurrentSavesKeepBothFiles(t *testing.T) {
	dir := useDir(t)
	settings := CoreSettings{
		Enabled:         true,
		MixedPort:       27890,
		ControllerPort:  27891,
		AIDedicatedPort: 27892,
		Secret:          "fixed-secret",
		Mode:            "global",
		BypassCN:        true,
	}
	subs := []Subscription{{
		ID:      "sub_1",
		Name:    "n",
		URL:     "https://example.com/s",
		Enabled: true,
	}}
	var wg sync.WaitGroup
	errCh := make(chan error, 40)
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := SaveCoreSettings(settings); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer wg.Done()
			if err := SaveSubscriptions(subs); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	gotCore, err := LoadCoreSettings()
	if err != nil {
		t.Fatal(err)
	}
	if gotCore != settings {
		t.Fatalf("core = %+v", gotCore)
	}
	gotSubs, err := LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(gotSubs) != 1 || gotSubs[0] != subs[0] {
		t.Fatalf("subs = %+v", gotSubs)
	}
	if _, err := os.Stat(filepath.Join(dir, "core.json")); err != nil {
		t.Fatal(err)
	}
}
