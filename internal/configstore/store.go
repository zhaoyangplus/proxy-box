package configstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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

var mu sync.Mutex

func BaseDir() (string, error) {
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
