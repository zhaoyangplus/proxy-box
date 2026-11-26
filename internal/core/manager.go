package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const readyTimeout = 5 * time.Second

var (
	ErrAlreadyRunning = errors.New("core already running")
	ErrNotRunning     = errors.New("core is not running")

	controllerHTTP = &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   250 * time.Millisecond,
				KeepAlive: -1,
			}).DialContext,
			DisableKeepAlives: true,
		},
	}
)

type phase int

const (
	phaseStopped phase = iota
	phaseStarting
	phaseRunning
)

// Status is the last known state of the managed mihomo process.
type Status struct {
	Running    bool
	PID        int
	Ports      Ports
	Version    string
	Mode       string
	ConfigPath string
	BinaryPath string
}

// Manager owns config.yaml, the mihomo process, and its kill-on-close job.
type Manager struct {
	dir        string
	mu         sync.Mutex
	phase      phase
	generation int
	run        *running
	params     ConfigParams
	providers  []ProviderSpec
	ports      Ports
	version    string
	configPath string
	binaryPath string
}

// NewManager prepares a manager for coreDir. The directory is created on Start.
func NewManager(coreDir string) *Manager {
	abs, err := filepath.Abs(coreDir)
	if err != nil {
		abs = coreDir
	}
	return &Manager{dir: abs}
}

// Start renders the config, launches mihomo hidden, and waits until /version answers.
// The returned ports are the ones actually bound. A second Start fails until Stop.
func (m *Manager) Start(ctx context.Context, binaryPath string, params ConfigParams, providers []ProviderSpec) (st Status, err error) {
	m.mu.Lock()
	if m.phase != phaseStopped {
		m.mu.Unlock()
		return Status{}, ErrAlreadyRunning
	}
	gen := m.generation + 1
	m.generation = gen
	m.phase = phaseStarting
	m.mu.Unlock()

	defer func() {
		if err == nil {
			return
		}
		m.mu.Lock()
		same := m.generation == gen
		var run *running
		if same {
			run = m.run
			m.run = nil
			m.phase = phaseStopped
		}
		m.mu.Unlock()
		if same {
			cleanupRun(run)
		}
	}()

	if _, statErr := os.Stat(binaryPath); statErr != nil {
		return Status{}, statErr
	}
	if !fileHasMZ(binaryPath) {
		return Status{}, ErrNoPE
	}
	binaryPath, err = filepath.Abs(binaryPath)
	if err != nil {
		return Status{}, err
	}
	ports, err := AllocatePorts(params)
	if err != nil {
		return Status{}, err
	}
	params.MixedPort = ports.Mixed
	params.ControllerPort = ports.Controller
	params.AIDedicatedPort = ports.AIDedicated
	body, err := Render(params, providers)
	if err != nil {
		return Status{}, err
	}
	if err = os.MkdirAll(m.dir, 0755); err != nil {
		return Status{}, err
	}
	configPath := filepath.Join(m.dir, ConfigFileName)
	if err = writeFileAtomic(configPath, []byte(body)); err != nil {
		return Status{}, err
	}
	logPath := filepath.Join(m.dir, LogFileName)
	child, err := startChild(binaryPath, m.dir, logPath, []string{"-d", m.dir})
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	if m.generation != gen {
		m.mu.Unlock()
		cleanupRun(child)
		return Status{}, ErrAlreadyRunning
	}
	m.run = child
	m.params = params
	m.providers = cloneProviders(providers)
	m.ports = ports
	m.configPath = configPath
	m.binaryPath = binaryPath
	m.version = ""
	m.mu.Unlock()

	readyCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		readyCtx, cancel = context.WithTimeout(ctx, readyTimeout)
		defer cancel()
	}
	ver, waitErr := waitReady(readyCtx, ports.Controller, params.Secret)
	if waitErr != nil {
		return Status{}, fmt.Errorf("mihomo not ready: %w\n%s", waitErr, logTail(logPath))
	}
	m.mu.Lock()
	if m.generation != gen || m.phase != phaseStarting {
		m.mu.Unlock()
		return Status{}, ErrAlreadyRunning
	}
	m.phase = phaseRunning
	m.version = ver
	st = m.snapshot()
	m.mu.Unlock()
	return st, nil
}

// Stop kills the child and closes the job. It is safe to call more than once.
func (m *Manager) Stop() error {
	m.mu.Lock()
	run := m.run
	m.run = nil
	m.phase = phaseStopped
	m.generation++
	m.mu.Unlock()
	cleanupRun(run)
	return nil
}

// Reload rewrites config.yaml with the running ports and PUTs that absolute path.
func (m *Manager) Reload(ctx context.Context, params ConfigParams, providers []ProviderSpec) error {
	m.mu.Lock()
	if m.phase != phaseRunning || m.run == nil {
		m.mu.Unlock()
		return ErrNotRunning
	}
	params.MixedPort = m.ports.Mixed
	params.ControllerPort = m.ports.Controller
	params.AIDedicatedPort = m.ports.AIDedicated
	if strings.TrimSpace(params.Mode) == "" {
		params.Mode = m.params.Mode
	}
	if params.Secret == "" {
		params.Secret = m.params.Secret
	}
	port := m.ports.Controller
	path := m.configPath
	m.mu.Unlock()

	body, err := Render(params, providers)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, []byte(body)); err != nil {
		return err
	}
	if err := putConfigPath(ctx, port, params.Secret, path); err != nil {
		return err
	}
	m.mu.Lock()
	if m.phase == phaseRunning {
		m.params = params
		m.providers = cloneProviders(providers)
	}
	m.mu.Unlock()
	return nil
}

// SwitchMode hot-switches rule, global, or direct and writes the same mode into config.yaml.
func (m *Manager) SwitchMode(ctx context.Context, mode string) error {
	mode, err := normalizeMode(mode, false)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.phase != phaseRunning || m.run == nil {
		m.mu.Unlock()
		return ErrNotRunning
	}
	params := m.params
	providers := cloneProviders(m.providers)
	port := m.ports.Controller
	path := m.configPath
	secret := m.params.Secret
	m.mu.Unlock()

	if err := patchMode(ctx, port, secret, mode); err != nil {
		return err
	}
	params.Mode = mode
	body, err := Render(params, providers)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, []byte(body)); err != nil {
		return err
	}
	m.mu.Lock()
	if m.phase == phaseRunning {
		m.params.Mode = mode
	}
	m.mu.Unlock()
	return nil
}

// Status returns the latest snapshot. It does not call the controller.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot()
}

func (m *Manager) snapshot() Status {
	st := Status{
		Running:    m.phase == phaseRunning,
		Ports:      m.ports,
		Version:    m.version,
		Mode:       m.params.Mode,
		ConfigPath: m.configPath,
		BinaryPath: m.binaryPath,
	}
	if st.Running && m.run != nil && m.run.cmd != nil && m.run.cmd.Process != nil {
		st.PID = m.run.cmd.Process.Pid
	}
	return st
}

func cloneProviders(in []ProviderSpec) []ProviderSpec {
	if len(in) == 0 {
		return nil
	}
	out := make([]ProviderSpec, len(in))
	copy(out, in)
	return out
}

func waitReady(ctx context.Context, port int, secret string) (string, error) {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/version", port)
	var last error
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		ver, err := fetchVersion(ctx, endpoint, secret)
		if err == nil && ver != "" {
			return ver, nil
		}
		last = err
		select {
		case <-ctx.Done():
			if last != nil {
				return "", last
			}
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func fetchVersion(ctx context.Context, endpoint, secret string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	body, status, err := controllerDo(cctx, http.MethodGet, endpoint, secret, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("version http %d", status)
	}
	var resp struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if resp.Version == "" {
		return "", fmt.Errorf("empty version")
	}
	return resp.Version, nil
}

func putConfigPath(ctx context.Context, port int, secret, configPath string) error {
	payload, err := json.Marshal(map[string]string{"path": filepath.ToSlash(configPath)})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/configs?force=true", port)
	body, status, err := controllerDo(ctx, http.MethodPut, endpoint, secret, payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("reload http %d: %s", status, bytes.TrimSpace(body))
	}
	return nil
}

func patchMode(ctx context.Context, port int, secret, mode string) error {
	payload, err := json.Marshal(map[string]string{"mode": mode})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/configs", port)
	body, status, err := controllerDo(ctx, http.MethodPatch, endpoint, secret, payload)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("switch mode http %d: %s", status, bytes.TrimSpace(body))
	}
	return nil
}

func controllerDo(ctx context.Context, method, endpoint, secret string, payload []byte) ([]byte, int, error) {
	var r io.Reader
	if payload != nil {
		r = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, r)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := controllerHTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return data, resp.StatusCode, err
}

func logTail(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(b) > 4000 {
		b = b[len(b)-4000:]
	}
	return string(b)
}
