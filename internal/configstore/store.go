package configstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"proxy-box/internal/clashscript"

	"golang.org/x/sys/windows"
)

var (
	// ErrConfigUnreadable means config.json exists but could not be read.
	ErrConfigUnreadable = errors.New("设置文件无法读取")
	// ErrConfigCorrupt means config.json is not valid JSON for AppConfig.
	ErrConfigCorrupt = errors.New("设置文件已损坏")
)

// AppConfig stores user preferences for the builtin core and the four AI apps.
type AppConfig struct {
	ManagedApps          map[string]bool `json:"managedApps,omitempty"`
	CustomCursorExe      string          `json:"customCursorExe,omitempty"`
	CustomAntigravityExe string          `json:"customAntigravityExe,omitempty"`
	CustomCodexExe       string          `json:"customCodexExe,omitempty"`
	SyncUserEnv          bool            `json:"syncUserEnv"`
	MinimizeToTray       bool            `json:"minimizeToTray"`
	AutoOptimizeNode     *bool           `json:"autoOptimizeNode,omitempty"`
	AutoSyncOnChange     *bool           `json:"autoSyncOnChange,omitempty"`
	NodeSelectionMode    string          `json:"nodeSelectionMode,omitempty"` // "auto" or "manual"
	ManualNodeName       string          `json:"manualNodeName,omitempty"`
	DelayTestURL         string          `json:"delayTestURL,omitempty"`
	SpeedTestURL         string          `json:"speedTestURL,omitempty"`
	SpeedTestMaxMB       int             `json:"speedTestMaxMB,omitempty"`
}

func (c AppConfig) IsAutoOptimizeEnabled() bool {
	if c.AutoOptimizeNode == nil {
		return true
	}
	return *c.AutoOptimizeNode
}

func (c AppConfig) IsAutoSyncEnabled() bool {
	if c.AutoSyncOnChange == nil {
		return true
	}
	return *c.AutoSyncOnChange
}

func BoolPtr(v bool) *bool { return &v }

// IsAppEnabled reports whether the user turned the app's proxy switch on.
func (c AppConfig) IsAppEnabled(id string) bool {
	for _, known := range clashscript.AppIDs {
		if known == id {
			return c.ManagedApps[id]
		}
	}
	return false
}

// EnabledAppIDs returns enabled app ids in clashscript.AppIDs order.
func (c AppConfig) EnabledAppIDs() []string {
	var out []string
	for _, id := range clashscript.AppIDs {
		if c.ManagedApps[id] {
			out = append(out, id)
		}
	}
	return out
}

func allManagedApps(on bool) map[string]bool {
	out := make(map[string]bool, len(clashscript.AppIDs))
	for _, id := range clashscript.AppIDs {
		out[id] = on
	}
	return out
}

func normalizeManagedApps(in map[string]bool) map[string]bool {
	out := allManagedApps(false)
	for _, id := range clashscript.AppIDs {
		out[id] = in[id]
	}
	return out
}

// KeySnapshot records previous values of managed keys before CursorBox modified them.
type KeySnapshot struct {
	Existed bool            `json:"existed"`
	Value   json.RawMessage `json:"value,omitempty"`
}

// TargetBackupState tracks managed modifications per target file.
type TargetBackupState struct {
	Managed      bool                   `json:"managed"`
	FileExisted  bool                   `json:"fileExisted"`
	UpdatedAt    string                 `json:"updatedAt"`
	AppliedProxy string                 `json:"appliedProxy"`
	Keys         map[string]KeySnapshot `json:"keys"`
	EnvKeys      map[string]KeySnapshot `json:"envKeys,omitempty"`
}

// StateManifest stores rollback metadata for all targets.
type StateManifest struct {
	Targets        map[string]TargetBackupState `json:"targets"`
	UserEnvManaged bool                         `json:"userEnvManaged"`
	PrevUserEnv    map[string]KeySnapshot       `json:"prevUserEnv,omitempty"`
}

var (
	mu              sync.Mutex
	baseDirOverride atomic.Value // string, tests only
	lastGood        atomic.Pointer[AppConfig]
	readConfigBytes = os.ReadFile
)

// SetBaseDir sends config reads and writes to dir.
// Tests set it so they do not touch ~/.proxy-box. An empty dir uses the home directory.
func SetBaseDir(dir string) {
	lastGood.Store(nil)
	baseDirOverride.Store(dir)
}

func BaseDir() (string, error) {
	if dir, ok := baseDirOverride.Load().(string); ok && dir != "" {
		if err := os.MkdirAll(filepath.Join(dir, "backups"), 0755); err != nil {
			return "", err
		}
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".proxy-box")
	legacyDir := filepath.Join(home, ".cursor-box")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if st, lErr := os.Stat(legacyDir); lErr == nil && st.IsDir() {
			_ = os.Rename(legacyDir, dir)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func BackupsDir() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "backups"), nil
}

// LoadConfig reads config.json.
// A missing file returns defaults and a nil error, and is created from state.json when that file has managed apps.
// An unreadable or corrupt file returns ErrConfigUnreadable or ErrConfigCorrupt and the last good config, or defaults when nothing has loaded yet.
// The returned config is safe to read. It is not safe to write back over a corrupt file. Use UpdateConfig for read-modify-write, or SaveConfig for an explicit save.
func LoadConfig() (AppConfig, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadConfigLocked()
}

// UpdateConfig reads and writes config.json inside one lock.
// It does not write when the current file is missing its directory, unreadable, or corrupt.
func UpdateConfig(fn func(*AppConfig) error) error {
	if fn == nil {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	cfg, err := loadConfigLocked()
	if err != nil {
		return err
	}
	if err := fn(&cfg); err != nil {
		return err
	}
	return storeConfigLocked(cfg)
}

// SaveConfig writes cfg. When the current file is unreadable or corrupt, that file is copied to config.json.bak first.
// A failed backup leaves the current file in place.
func SaveConfig(cfg AppConfig) error {
	mu.Lock()
	defer mu.Unlock()
	base, err := BaseDir()
	if err != nil {
		return err
	}
	if err := backupUnreadableConfig(base); err != nil {
		return err
	}
	cfg = normalizeConfig(cfg)
	if err := writeConfigFile(base, cfg); err != nil {
		return err
	}
	rememberConfig(cfg)
	return nil
}

func loadConfigLocked() (AppConfig, error) {
	base, err := BaseDir()
	if err != nil {
		return readFallback(), fmt.Errorf("%w: %s", ErrConfigUnreadable, err.Error())
	}
	path := filepath.Join(base, "config.json")
	data, err := readConfigBytes(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := normalizeConfig(defaultAppConfig())
		cfg.ManagedApps = managedAppsFromState(base)
		cfg = normalizeConfig(cfg)
		if err := writeConfigFile(base, cfg); err != nil {
			return cfg, err
		}
		return rememberConfig(cfg), nil
	}
	if err != nil {
		return readFallback(), fmt.Errorf("%w: %s", ErrConfigUnreadable, err.Error())
	}
	cfg := defaultAppConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return readFallback(), fmt.Errorf("%w: %s", ErrConfigCorrupt, err.Error())
	}
	fillAppDefaults(&cfg)
	if cfg.ManagedApps == nil {
		cfg.ManagedApps = managedAppsFromState(base)
		cfg = normalizeConfig(cfg)
		if err := writeConfigFile(base, cfg); err != nil {
			return cfg, err
		}
		return rememberConfig(cfg), nil
	}
	return rememberConfig(normalizeConfig(cfg)), nil
}

func storeConfigLocked(cfg AppConfig) error {
	base, err := BaseDir()
	if err != nil {
		return err
	}
	cfg = normalizeConfig(cfg)
	if err := writeConfigFile(base, cfg); err != nil {
		return err
	}
	rememberConfig(cfg)
	return nil
}

func backupUnreadableConfig(base string) error {
	path := filepath.Join(base, "config.json")
	data, err := readConfigBytes(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConfigUnreadable, err.Error())
	}
	var cfg AppConfig
	if json.Unmarshal(data, &cfg) == nil {
		return nil
	}
	if err := os.WriteFile(path+".bak", data, 0644); err != nil {
		return err
	}
	return nil
}

func readFallback() AppConfig {
	if cfg := lastGood.Load(); cfg != nil {
		return cloneConfig(*cfg)
	}
	cfg := normalizeConfig(defaultAppConfig())
	cfg.ManagedApps = allManagedApps(false)
	return cfg
}

func rememberConfig(cfg AppConfig) AppConfig {
	cloned := cloneConfig(cfg)
	lastGood.Store(&cloned)
	return cloneConfig(cloned)
}

func cloneConfig(cfg AppConfig) AppConfig {
	out := cfg
	if cfg.ManagedApps != nil {
		out.ManagedApps = make(map[string]bool, len(cfg.ManagedApps))
		for id, on := range cfg.ManagedApps {
			out.ManagedApps[id] = on
		}
	}
	if cfg.AutoOptimizeNode != nil {
		v := *cfg.AutoOptimizeNode
		out.AutoOptimizeNode = &v
	}
	if cfg.AutoSyncOnChange != nil {
		v := *cfg.AutoSyncOnChange
		out.AutoSyncOnChange = &v
	}
	return out
}

func normalizeConfig(cfg AppConfig) AppConfig {
	fillAppDefaults(&cfg)
	if cfg.ManagedApps == nil {
		cfg.ManagedApps = allManagedApps(false)
	} else {
		cfg.ManagedApps = normalizeManagedApps(cfg.ManagedApps)
	}
	return cfg
}

func defaultAppConfig() AppConfig {
	return AppConfig{
		MinimizeToTray:   true,
		AutoOptimizeNode: BoolPtr(true),
		AutoSyncOnChange: BoolPtr(true),
	}
}

func fillAppDefaults(cfg *AppConfig) {
	if cfg.AutoOptimizeNode == nil {
		cfg.AutoOptimizeNode = BoolPtr(true)
	}
	if cfg.AutoSyncOnChange == nil {
		cfg.AutoSyncOnChange = BoolPtr(true)
	}
}

func managedAppsFromState(base string) map[string]bool {
	out := allManagedApps(false)
	data, err := os.ReadFile(filepath.Join(base, "state.json"))
	if err != nil {
		return out
	}
	var st StateManifest
	if json.Unmarshal(data, &st) != nil {
		return out
	}
	for _, id := range clashscript.AppIDs {
		if st.Targets[id].Managed {
			out[id] = true
		}
	}
	return out
}

func writeConfigFile(base string, cfg AppConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(base, "config.json"), data, 0644)
}

func LoadState() StateManifest {
	mu.Lock()
	defer mu.Unlock()

	st := StateManifest{
		Targets:     make(map[string]TargetBackupState),
		PrevUserEnv: make(map[string]KeySnapshot),
	}
	base, err := BaseDir()
	if err != nil {
		return st
	}
	data, err := os.ReadFile(filepath.Join(base, "state.json"))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	if st.Targets == nil {
		st.Targets = make(map[string]TargetBackupState)
	}
	if st.PrevUserEnv == nil {
		st.PrevUserEnv = make(map[string]KeySnapshot)
	}
	return st
}

func SaveState(st StateManifest) error {
	mu.Lock()
	defer mu.Unlock()

	base, err := BaseDir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(base, "state.json"), data, 0644)
}

const (
	defaultMixedPort      = 27890
	defaultControllerPort = 27891
	defaultAIPort         = 27892
	defaultSpeedTestPort  = 27893
	defaultMode           = "rule"
	subscriptionIDBytes   = 16
	idAlphabet            = "abcdefghijklmnopqrstuvwxyz0123456789_-"
)

// SubUsage is the subscription-userinfo header.
type SubUsage struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
	Total    int64 `json:"total"`
	Expire   int64 `json:"expire"` // Unix seconds
}

// Subscription is one remote provider. ID is generated by NewSubscriptionID.
type Subscription struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	URL               string    `json:"url"`
	UserAgent         string    `json:"userAgent,omitempty"`
	Filter            string    `json:"filter,omitempty"` // passed through to mihomo proxy-provider.filter
	Enabled           bool      `json:"enabled"`
	UpdateIntervalMin int       `json:"updateIntervalMin"` // 0 means manual updates only
	UpdatedAt         time.Time `json:"updatedAt,omitempty"`
	LastError         string    `json:"lastError,omitempty"`
	NodeCount         int       `json:"nodeCount"`
	Usage             *SubUsage `json:"usage,omitempty"`
}

// CoreSettings is the built-in core. Secret must not be sent to the frontend.
type CoreSettings struct {
	Enabled         bool   `json:"enabled"`
	MixedPort       int    `json:"mixedPort"`
	ControllerPort  int    `json:"controllerPort"`
	AIDedicatedPort int    `json:"aiDedicatedPort"`
	SpeedTestPort   int    `json:"speedTestPort"`
	Secret          string `json:"secret"`
	Mode            string `json:"mode"` // rule, global, or direct
	BypassCN        bool   `json:"bypassCN"`
	SystemProxy     bool   `json:"systemProxy"`
}

// LoadCoreSettings reads core.json. A missing file is created with default ports and a random Secret.
// An existing file fills zero ports, an empty mode, and an empty Secret, and is rewritten only when a field was filled.
func LoadCoreSettings() (CoreSettings, error) {
	mu.Lock()
	defer mu.Unlock()

	path, err := corePath()
	if err != nil {
		return CoreSettings{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg, genErr := newCoreSettings()
		if genErr != nil {
			return CoreSettings{}, genErr
		}
		raw, marshalErr := json.MarshalIndent(cfg, "", "  ")
		if marshalErr != nil {
			return CoreSettings{}, marshalErr
		}
		if writeErr := WriteAtomic(path, raw); writeErr != nil {
			return CoreSettings{}, writeErr
		}
		return cfg, nil
	}
	if err != nil {
		return CoreSettings{}, err
	}
	var cfg CoreSettings
	if err := json.Unmarshal(data, &cfg); err != nil {
		return CoreSettings{}, err
	}
	filled, err := fillCoreDefaults(&cfg)
	if err != nil {
		return CoreSettings{}, err
	}
	if !filled {
		return cfg, nil
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return CoreSettings{}, err
	}
	if err := WriteAtomic(path, raw); err != nil {
		return CoreSettings{}, err
	}
	return cfg, nil
}

// SaveCoreSettings writes core.json.
func SaveCoreSettings(cfg CoreSettings) error {
	mu.Lock()
	defer mu.Unlock()

	path, err := corePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(path, data)
}

// LoadSubscriptions reads subscriptions.json. A missing file is an empty list.
func LoadSubscriptions() ([]Subscription, error) {
	mu.Lock()
	defer mu.Unlock()

	path, err := subscriptionsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Subscription{}, nil
	}
	if err != nil {
		return nil, err
	}
	var subs []Subscription
	if err := json.Unmarshal(data, &subs); err != nil {
		return nil, err
	}
	if subs == nil {
		return []Subscription{}, nil
	}
	return subs, nil
}

// SaveSubscriptions writes subscriptions.json.
func SaveSubscriptions(subs []Subscription) error {
	mu.Lock()
	defer mu.Unlock()

	if subs == nil {
		subs = []Subscription{}
	}
	path, err := subscriptionsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(path, data)
}

// NewSubscriptionID returns an id whose characters are only [a-z0-9_-].
func NewSubscriptionID() (string, error) {
	buf := make([]byte, subscriptionIDBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = idAlphabet[int(b)%len(idAlphabet)]
	}
	return string(buf), nil
}

func corePath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "core.json"), nil
}

func subscriptionsPath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "subscriptions.json"), nil
}

func newCoreSettings() (CoreSettings, error) {
	var cfg CoreSettings
	_, err := fillCoreDefaults(&cfg)
	return cfg, err
}

func fillCoreDefaults(cfg *CoreSettings) (bool, error) {
	filled := false
	if cfg.MixedPort == 0 {
		cfg.MixedPort = defaultMixedPort
		filled = true
	}
	if cfg.ControllerPort == 0 {
		cfg.ControllerPort = defaultControllerPort
		filled = true
	}
	if cfg.AIDedicatedPort == 0 {
		cfg.AIDedicatedPort = defaultAIPort
		filled = true
	}
	if cfg.SpeedTestPort == 0 {
		cfg.SpeedTestPort = defaultSpeedTestPort
		filled = true
	}
	if cfg.Mode == "" {
		cfg.Mode = defaultMode
		filled = true
	}
	if cfg.Secret == "" {
		secret, err := randomSecret()
		if err != nil {
			return false, err
		}
		cfg.Secret = secret
		filled = true
	}
	return filled, nil
}

func randomSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// WriteAtomic replaces path with data. A failed write leaves an existing file in place.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	closed = true
	if err := replaceFile(tmpName, path); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

func replaceFile(oldPath, newPath string) error {
	from, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
