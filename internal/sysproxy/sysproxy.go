// Package sysproxy sets and restores the Windows per-user system proxy.
// The backup file stores the previous Internet Settings values for RecoverIfStale.
package sysproxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"proxy-box/internal/configstore"
)

// DefaultBypass is the ProxyOverride value used for a normal system proxy.
// It is the v2rayN Windows exception list with <local> in front.
const DefaultBypass = "<local>;localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*;192.168.*"

const backupName = "sysproxy-backup.json"

const (
	nameProxyEnable   = "ProxyEnable"
	nameProxyServer   = "ProxyServer"
	nameProxyOverride = "ProxyOverride"
	nameAutoConfigURL = "AutoConfigURL"
)

// InternetSetOptionW flags. 39 notifies settings listeners. 37 reloads WinINet.
const (
	internetOptionSettingsChanged uintptr = 39
	internetOptionRefresh         uintptr = 37
)

// Registry reads and writes values under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings.
// DeleteValue succeeds when the value is already absent.
type Registry interface {
	GetString(name string) (value string, exists bool, err error)
	GetDWord(name string) (value uint32, exists bool, err error)
	SetString(name, value string) error
	SetDWord(name string, value uint32) error
	DeleteValue(name string) error
}

type dwordState struct {
	Existed bool   `json:"existed"`
	Value   uint32 `json:"value"`
}

type stringState struct {
	Existed bool   `json:"existed"`
	Value   string `json:"value"`
}

// backup is the on-disk record. Original key states stay as first captured.
// AppliedServer is the ProxyServer value this program wrote.
type backup struct {
	ProxyEnable   dwordState  `json:"proxyEnable"`
	ProxyServer   stringState `json:"proxyServer"`
	ProxyOverride stringState `json:"proxyOverride"`
	AutoConfigURL stringState `json:"autoConfigURL"`
	AppliedServer string      `json:"appliedServer"`
}

// Client sets and restores the system proxy through an injected registry and
// an injected InternetSetOptionW callback.
type Client struct {
	mu        sync.Mutex
	reg       Registry
	setOption func(option uintptr) error
	path      string
}

// New returns a Client that stores its backup at backupPath.
// setOption receives InternetSetOptionW option flags. Tests pass a fake.
func New(reg Registry, setOption func(option uintptr) error, backupPath string) *Client {
	return &Client{reg: reg, setOption: setOption, path: backupPath}
}

// BackupPath is ~/.proxy-box/sysproxy-backup.json, beside the configstore files.
func BackupPath() (string, error) {
	dir, err := configstore.BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, backupName), nil
}

var (
	liveOnce sync.Once
	live     *Client
	liveErr  error
)

func liveClient() (*Client, error) {
	liveOnce.Do(func() {
		path, err := BackupPath()
		if err != nil {
			liveErr = err
			return
		}
		live = New(windowsRegistry{}, internetSetOption, path)
	})
	return live, liveErr
}

// Set enables the live Windows system proxy.
func Set(host string, port int, bypass string) error {
	c, err := liveClient()
	if err != nil {
		return err
	}
	return c.Set(host, port, bypass)
}

// Restore puts back the live Windows system proxy.
func Restore() error {
	c, err := liveClient()
	if err != nil {
		return err
	}
	return c.Restore()
}

// RecoverIfStale heals a leftover live backup when the core is not running.
func RecoverIfStale(coreRunning bool) error {
	c, err := liveClient()
	if err != nil {
		return err
	}
	return c.RecoverIfStale(coreRunning)
}

// Set writes ProxyEnable=1, ProxyServer=host:port, and ProxyOverride=bypass,
// then deletes AutoConfigURL and refreshes WinINet. An empty bypass uses
// DefaultBypass. The first call saves the previous values. Later calls keep
// that backup and only update AppliedServer.
func (c *Client) Set(host string, port int, bypass string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.set(host, port, bypass)
}

// Restore is idempotent. With no backup it returns nil.
// When the current ProxyServer differs from AppliedServer, another program
// owns the setting. Restore then deletes the backup and leaves the registry
// unchanged. Otherwise it restores each key, refreshes WinINet, and deletes
// the backup. Keys that did not exist are deleted rather than set to empty.
func (c *Client) Restore() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.restore()
}

// RecoverIfStale calls Restore when a backup exists and the core is not running.
func (c *Client) RecoverIfStale(coreRunning bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if coreRunning {
		return nil
	}
	return c.restore()
}

func (c *Client) set(host string, port int, bypass string) error {
	if err := c.ready(); err != nil {
		return err
	}
	server, err := proxyServer(host, port)
	if err != nil {
		return err
	}
	if bypass == "" {
		bypass = DefaultBypass
	}

	b, exists, err := loadBackup(c.path)
	if err != nil {
		return err
	}
	if exists && b.AppliedServer == "" {
		return fmt.Errorf("系统代理备份缺少 appliedServer")
	}
	if !exists {
		b, err = c.snapshot()
		if err != nil {
			return err
		}
	}
	b.AppliedServer = server
	if err := saveBackup(c.path, b); err != nil {
		return err
	}
	if err := c.apply(server, bypass); err != nil {
		return err
	}
	return c.notify()
}

func (c *Client) restore() error {
	if err := c.ready(); err != nil {
		return err
	}
	b, exists, err := loadBackup(c.path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if b.AppliedServer == "" {
		return fmt.Errorf("系统代理备份缺少 appliedServer")
	}
	current, ok, err := c.reg.GetString(nameProxyServer)
	if err != nil {
		return err
	}
	if !ok || current != b.AppliedServer {
		return removeBackup(c.path)
	}
	if err := c.restoreDWord(nameProxyEnable, b.ProxyEnable); err != nil {
		return err
	}
	if err := c.restoreString(nameProxyOverride, b.ProxyOverride); err != nil {
		return err
	}
	if err := c.restoreString(nameAutoConfigURL, b.AutoConfigURL); err != nil {
		return err
	}
	if err := c.restoreString(nameProxyServer, b.ProxyServer); err != nil {
		return err
	}
	if err := c.notify(); err != nil {
		return err
	}
	return removeBackup(c.path)
}

func (c *Client) ready() error {
	if c.reg == nil {
		return fmt.Errorf("未配置注册表")
	}
	if c.path == "" {
		return fmt.Errorf("未配置系统代理备份路径")
	}
	return nil
}

func (c *Client) snapshot() (backup, error) {
	var b backup
	enable, ok, err := c.reg.GetDWord(nameProxyEnable)
	if err != nil {
		return b, err
	}
	b.ProxyEnable = dwordState{Existed: ok, Value: enable}

	server, ok, err := c.reg.GetString(nameProxyServer)
	if err != nil {
		return b, err
	}
	b.ProxyServer = stringState{Existed: ok, Value: server}

	override, ok, err := c.reg.GetString(nameProxyOverride)
	if err != nil {
		return b, err
	}
	b.ProxyOverride = stringState{Existed: ok, Value: override}

	auto, ok, err := c.reg.GetString(nameAutoConfigURL)
	if err != nil {
		return b, err
	}
	b.AutoConfigURL = stringState{Existed: ok, Value: auto}
	return b, nil
}

func (c *Client) apply(server, bypass string) error {
	if err := c.reg.SetString(nameProxyServer, server); err != nil {
		return err
	}
	if err := c.reg.SetDWord(nameProxyEnable, 1); err != nil {
		return err
	}
	if err := c.reg.SetString(nameProxyOverride, bypass); err != nil {
		return err
	}
	return c.reg.DeleteValue(nameAutoConfigURL)
}

func (c *Client) restoreDWord(name string, state dwordState) error {
	if !state.Existed {
		return c.reg.DeleteValue(name)
	}
	return c.reg.SetDWord(name, state.Value)
}

func (c *Client) restoreString(name string, state stringState) error {
	if !state.Existed {
		return c.reg.DeleteValue(name)
	}
	return c.reg.SetString(name, state.Value)
}

func (c *Client) notify() error {
	if c.setOption == nil {
		return fmt.Errorf("未配置 WinINet 刷新")
	}
	if err := c.setOption(internetOptionSettingsChanged); err != nil {
		return err
	}
	return c.setOption(internetOptionRefresh)
}

func proxyServer(host string, port int) (string, error) {
	if host == "" || strings.ContainsAny(host, " \t;:\\/") {
		return "", fmt.Errorf("系统代理主机无效")
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("系统代理端口无效")
	}
	return host + ":" + strconv.Itoa(port), nil
}

func loadBackup(path string) (backup, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return backup{}, false, nil
		}
		return backup{}, false, err
	}
	var b backup
	if err := json.Unmarshal(data, &b); err != nil {
		return backup{}, false, fmt.Errorf("读取系统代理备份失败: %w", err)
	}
	return b, true, nil
}

func saveBackup(path string, b backup) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("保存系统代理备份失败: %w", err)
	}
	return nil
}

func removeBackup(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "sysproxy-backup-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
