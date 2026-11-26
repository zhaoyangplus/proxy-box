package configstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

// AppConfig stores user preferences and custom path overrides for CursorBox.
type AppConfig struct {
	PreferredClient      string `json:"preferredClient,omitempty"` // "auto", "clash_verge", "clash_mi", "v2rayn"
	CustomClashDir       string `json:"customClashDir,omitempty"`
	CustomClashExe       string `json:"customClashExe,omitempty"`
	CustomClashMiDir     string `json:"customClashMiDir,omitempty"`
	CustomClashMiExe     string `json:"customClashMiExe,omitempty"`
	CustomV2rayNExe      string `json:"customV2rayNExe,omitempty"`
	CustomCursorExe      string `json:"customCursorExe,omitempty"`
	CustomAntigravityExe string `json:"customAntigravityExe,omitempty"`
	CustomCodexExe       string `json:"customCodexExe,omitempty"`
	CustomPortOverride   int    `json:"customPortOverride,omitempty"`
	ExcludeRegionsRegex  string `json:"excludeRegionsRegex,omitempty"`
	SyncUserEnv          bool   `json:"syncUserEnv"`
	MinimizeToTray       bool   `json:"minimizeToTray"`
	AutoOptimizeNode     *bool  `json:"autoOptimizeNode,omitempty"`
	AutoSyncOnChange     *bool  `json:"autoSyncOnChange,omitempty"`
	NodeSelectionMode    string `json:"nodeSelectionMode,omitempty"` // "auto" or "manual"
	ManualNodeName       string `json:"manualNodeName,omitempty"`
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
	ScriptBackedUp bool                         `json:"scriptBackedUp"`
}

var (
	mu              sync.Mutex
	baseDirOverride atomic.Value // string, tests only
)

// SetBaseDir sends config reads and writes to dir.
// Tests set it so they do not touch ~/.proxy-box. An empty dir uses the home directory.
func SetBaseDir(dir string) {
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

func LoadConfig() AppConfig {
	mu.Lock()
	defer mu.Unlock()

	cfg := AppConfig{
		PreferredClient:  "auto",
		MinimizeToTray:   true,
		AutoOptimizeNode: BoolPtr(true),
		AutoSyncOnChange: BoolPtr(true),
	}
	base, err := BaseDir()
	if err != nil {
		return cfg
	}
	data, err := os.ReadFile(filepath.Join(base, "config.json"))
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	if cfg.PreferredClient == "" {
		cfg.PreferredClient = "auto"
	}
	if cfg.AutoOptimizeNode == nil {
		cfg.AutoOptimizeNode = BoolPtr(true)
	}
	if cfg.AutoSyncOnChange == nil {
		cfg.AutoSyncOnChange = BoolPtr(true)
	}
	return cfg
}

func SaveConfig(cfg AppConfig) error {
	mu.Lock()
	defer mu.Unlock()

	if cfg.PreferredClient == "" {
		cfg.PreferredClient = "auto"
	}
	if cfg.AutoOptimizeNode == nil {
		cfg.AutoOptimizeNode = BoolPtr(true)
	}
	if cfg.AutoSyncOnChange == nil {
		cfg.AutoSyncOnChange = BoolPtr(true)
	}
	base, err := BaseDir()
	if err != nil {
		return err
	}
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
	Secret          string `json:"secret"`
	Mode            string `json:"mode"` // rule, global, or direct
	BypassCN        bool   `json:"bypassCN"`
	SystemProxy     bool   `json:"systemProxy"`
}

// LoadCoreSettings reads core.json. The first call creates a file with default ports and a random Secret.
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
	secret, err := randomSecret()
	if err != nil {
		return CoreSettings{}, err
	}
	return CoreSettings{
		MixedPort:       defaultMixedPort,
		ControllerPort:  defaultControllerPort,
		AIDedicatedPort: defaultAIPort,
		Secret:          secret,
		Mode:            defaultMode,
	}, nil
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
