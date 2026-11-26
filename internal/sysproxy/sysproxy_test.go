package sysproxy

import (
	"os"
	"path/filepath"
	"testing"
)

type memRegistry struct {
	dword map[string]uint32
	strs  map[string]string
}

func newMemRegistry() *memRegistry {
	return &memRegistry{
		dword: map[string]uint32{},
		strs:  map[string]string{},
	}
}

func (m *memRegistry) GetString(name string) (string, bool, error) {
	v, ok := m.strs[name]
	return v, ok, nil
}

func (m *memRegistry) GetDWord(name string) (uint32, bool, error) {
	v, ok := m.dword[name]
	return v, ok, nil
}

func (m *memRegistry) SetString(name, value string) error {
	delete(m.dword, name)
	m.strs[name] = value
	return nil
}

func (m *memRegistry) SetDWord(name string, value uint32) error {
	delete(m.strs, name)
	m.dword[name] = value
	return nil
}

func (m *memRegistry) DeleteValue(name string) error {
	delete(m.dword, name)
	delete(m.strs, name)
	return nil
}

func (m *memRegistry) clone() *memRegistry {
	n := newMemRegistry()
	for k, v := range m.dword {
		n.dword[k] = v
	}
	for k, v := range m.strs {
		n.strs[k] = v
	}
	return n
}

func (m *memRegistry) equal(o *memRegistry) bool {
	if len(m.dword) != len(o.dword) || len(m.strs) != len(o.strs) {
		return false
	}
	for k, v := range m.dword {
		ov, ok := o.dword[k]
		if !ok || ov != v {
			return false
		}
	}
	for k, v := range m.strs {
		ov, ok := o.strs[k]
		if !ok || ov != v {
			return false
		}
	}
	return true
}

type harness struct {
	c    *Client
	reg  *memRegistry
	path string
	opts []uintptr
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		reg:  newMemRegistry(),
		path: filepath.Join(t.TempDir(), backupName),
	}
	h.c = New(h.reg, func(option uintptr) error {
		h.opts = append(h.opts, option)
		return nil
	}, h.path)
	return h
}

func (h *harness) requireRefresh(t *testing.T, pairs int) {
	t.Helper()
	if len(h.opts) != pairs*2 {
		t.Fatalf("WinINet calls = %v, want %d pairs of 39 then 37", h.opts, pairs)
	}
	for i := 0; i < pairs; i++ {
		if h.opts[i*2] != 39 || h.opts[i*2+1] != 37 {
			t.Fatalf("WinINet calls = %v, want 39 then 37", h.opts)
		}
	}
}

func seedAll(t *testing.T, reg *memRegistry) {
	t.Helper()
	must(t, reg.SetDWord(nameProxyEnable, 0))
	must(t, reg.SetString(nameProxyServer, "127.0.0.1:7897"))
	must(t, reg.SetString(nameProxyOverride, "localhost"))
	must(t, reg.SetString(nameAutoConfigURL, "http://127.0.0.1/pac"))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func requireDWord(t *testing.T, reg *memRegistry, name string, value uint32, exists bool) {
	t.Helper()
	got, ok, err := reg.GetDWord(name)
	if err != nil {
		t.Fatal(err)
	}
	if ok != exists || (exists && got != value) {
		t.Fatalf("%s = %d exists=%v, want %d exists=%v", name, got, ok, value, exists)
	}
}

func requireString(t *testing.T, reg *memRegistry, name, value string, exists bool) {
	t.Helper()
	got, ok, err := reg.GetString(name)
	if err != nil {
		t.Fatal(err)
	}
	if ok != exists || (exists && got != value) {
		t.Fatalf("%s = %q exists=%v, want %q exists=%v", name, got, ok, value, exists)
	}
}

func requireOriginal(t *testing.T, reg *memRegistry) {
	t.Helper()
	requireDWord(t, reg, nameProxyEnable, 0, true)
	requireString(t, reg, nameProxyServer, "127.0.0.1:7897", true)
	requireString(t, reg, nameProxyOverride, "localhost", true)
	requireString(t, reg, nameAutoConfigURL, "http://127.0.0.1/pac", true)
}

func requireApplied(t *testing.T, reg *memRegistry, server, bypass string) {
	t.Helper()
	requireDWord(t, reg, nameProxyEnable, 1, true)
	requireString(t, reg, nameProxyServer, server, true)
	requireString(t, reg, nameProxyOverride, bypass, true)
	requireString(t, reg, nameAutoConfigURL, "", false)
}

func requireBackupGone(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if !os.IsNotExist(err) {
		t.Fatalf("backup still present: %v", err)
	}
}

func TestRegistryValueNamesAndDefaultBypass(t *testing.T) {
	if nameProxyEnable != "ProxyEnable" || nameProxyServer != "ProxyServer" || nameProxyOverride != "ProxyOverride" || nameAutoConfigURL != "AutoConfigURL" {
		t.Fatalf("registry names = %s %s %s %s", nameProxyEnable, nameProxyServer, nameProxyOverride, nameAutoConfigURL)
	}
	const want = "<local>;localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*;192.168.*"
	if DefaultBypass != want {
		t.Fatalf("DefaultBypass = %q", DefaultBypass)
	}
}

func TestSetRestoreRoundTrip(t *testing.T) {
	h := newHarness(t)
	seedAll(t, h.reg)

	must(t, h.c.Set("127.0.0.1", 27890, ""))
	requireApplied(t, h.reg, "127.0.0.1:27890", DefaultBypass)
	h.requireRefresh(t, 1)

	b, ok, err := loadBackup(h.path)
	must(t, err)
	if !ok {
		t.Fatal("backup missing")
	}
	if b.AppliedServer != "127.0.0.1:27890" {
		t.Fatalf("AppliedServer = %q", b.AppliedServer)
	}
	if b.ProxyEnable != (dwordState{Existed: true, Value: 0}) {
		t.Fatalf("backed up ProxyEnable = %+v", b.ProxyEnable)
	}
	if b.ProxyServer != (stringState{Existed: true, Value: "127.0.0.1:7897"}) {
		t.Fatalf("backed up ProxyServer = %+v", b.ProxyServer)
	}
	if b.AutoConfigURL != (stringState{Existed: true, Value: "http://127.0.0.1/pac"}) {
		t.Fatalf("backed up AutoConfigURL = %+v", b.AutoConfigURL)
	}

	must(t, h.c.Restore())
	requireOriginal(t, h.reg)
	requireBackupGone(t, h.path)
	h.requireRefresh(t, 2)
}

func TestRepeatedSetKeepsOriginalBackup(t *testing.T) {
	h := newHarness(t)
	seedAll(t, h.reg)

	must(t, h.c.Set("127.0.0.1", 27890, "first"))
	must(t, h.reg.SetString(nameProxyServer, "evil:1"))
	must(t, h.c.Set("127.0.0.1", 27891, "second"))

	requireApplied(t, h.reg, "127.0.0.1:27891", "second")
	b, ok, err := loadBackup(h.path)
	must(t, err)
	if !ok {
		t.Fatal("backup missing")
	}
	if b.AppliedServer != "127.0.0.1:27891" {
		t.Fatalf("AppliedServer = %q", b.AppliedServer)
	}
	if b.ProxyEnable != (dwordState{Existed: true, Value: 0}) ||
		b.ProxyServer != (stringState{Existed: true, Value: "127.0.0.1:7897"}) ||
		b.ProxyOverride != (stringState{Existed: true, Value: "localhost"}) ||
		b.AutoConfigURL != (stringState{Existed: true, Value: "http://127.0.0.1/pac"}) {
		t.Fatalf("backup originals changed: %+v", b)
	}

	must(t, h.c.Restore())
	requireOriginal(t, h.reg)
	requireBackupGone(t, h.path)
}

func TestRestoreDeletesMissingKeys(t *testing.T) {
	h := newHarness(t)
	must(t, h.reg.SetDWord(nameProxyEnable, 0))
	must(t, h.reg.SetString(nameProxyServer, "old:9"))

	must(t, h.c.Set("127.0.0.1", 27890, "bypass"))
	requireString(t, h.reg, nameProxyOverride, "bypass", true)
	requireString(t, h.reg, nameAutoConfigURL, "", false)

	must(t, h.c.Restore())
	requireDWord(t, h.reg, nameProxyEnable, 0, true)
	requireString(t, h.reg, nameProxyServer, "old:9", true)
	requireString(t, h.reg, nameProxyOverride, "", false)
	requireString(t, h.reg, nameAutoConfigURL, "", false)
	requireBackupGone(t, h.path)
}

func TestRestoreLeavesRegistryWhenTakenOver(t *testing.T) {
	h := newHarness(t)
	seedAll(t, h.reg)
	must(t, h.c.Set("127.0.0.1", 27890, "ours"))
	must(t, h.reg.SetString(nameProxyServer, "other:1"))
	kept := h.reg.clone()

	must(t, h.c.Restore())
	if !h.reg.equal(kept) {
		t.Fatal("registry changed after takeover")
	}
	requireBackupGone(t, h.path)
	h.requireRefresh(t, 1)

	must(t, h.c.Restore())
	if !h.reg.equal(kept) {
		t.Fatal("second restore changed the registry")
	}
	h.requireRefresh(t, 1)
}

func TestRecoverIfStale(t *testing.T) {
	t.Run("core running", func(t *testing.T) {
		h := newHarness(t)
		seedAll(t, h.reg)
		must(t, h.c.Set("127.0.0.1", 27890, "ours"))
		must(t, h.c.RecoverIfStale(true))
		requireApplied(t, h.reg, "127.0.0.1:27890", "ours")
		if _, err := os.Stat(h.path); err != nil {
			t.Fatal(err)
		}
		h.requireRefresh(t, 1)
	})

	t.Run("core stopped", func(t *testing.T) {
		h := newHarness(t)
		seedAll(t, h.reg)
		must(t, h.c.Set("127.0.0.1", 27890, "ours"))
		must(t, h.c.RecoverIfStale(false))
		requireOriginal(t, h.reg)
		requireBackupGone(t, h.path)
		h.requireRefresh(t, 2)
	})

	t.Run("taken over", func(t *testing.T) {
		h := newHarness(t)
		seedAll(t, h.reg)
		must(t, h.c.Set("127.0.0.1", 27890, "ours"))
		must(t, h.reg.SetString(nameProxyServer, "other:1"))
		kept := h.reg.clone()
		must(t, h.c.RecoverIfStale(false))
		if !h.reg.equal(kept) {
			t.Fatal("recovery rewrote a taken-over registry")
		}
		requireBackupGone(t, h.path)
	})

	t.Run("no backup", func(t *testing.T) {
		h := newHarness(t)
		must(t, h.c.RecoverIfStale(false))
		h.requireRefresh(t, 0)
	})
}

func TestRestoreIdempotent(t *testing.T) {
	h := newHarness(t)
	must(t, h.c.Restore())
	h.requireRefresh(t, 0)

	seedAll(t, h.reg)
	must(t, h.c.Set("127.0.0.1", 27890, "ours"))
	must(t, h.c.Restore())
	requireOriginal(t, h.reg)
	must(t, h.c.Restore())
	requireOriginal(t, h.reg)
	requireBackupGone(t, h.path)
	h.requireRefresh(t, 2)
}

func TestSetRefusesBackupWithoutAppliedServer(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(h.path, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	seedAll(t, h.reg)
	if err := h.c.Set("127.0.0.1", 27890, "x"); err == nil {
		t.Fatal("expected error")
	}
	requireOriginal(t, h.reg)
	h.requireRefresh(t, 0)
}

func TestSetRejectsBadEndpoint(t *testing.T) {
	cases := []struct {
		host string
		port int
	}{
		{host: "", port: 27890},
		{host: " ", port: 27890},
		{host: "127.0.0.1:80", port: 27890},
		{host: "127.0.0.1", port: 0},
		{host: "127.0.0.1", port: 65536},
		{host: "bad host", port: 27890},
	}
	for _, tc := range cases {
		h := newHarness(t)
		if err := h.c.Set(tc.host, tc.port, "x"); err == nil {
			t.Fatalf("Set(%q, %d) succeeded", tc.host, tc.port)
		}
		h.requireRefresh(t, 0)
		requireBackupGone(t, h.path)
		requireDWord(t, h.reg, nameProxyEnable, 0, false)
		requireString(t, h.reg, nameProxyServer, "", false)
	}
}
