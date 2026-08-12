package pluginruntime

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	maxManifestBytes  = 64 * 1024
	minAPISecretBytes = 32
)

type pluginEntry struct {
	manifest    Manifest
	root        string
	staticRoot  string
	staticEntry string
	runtimeURL  *url.URL
	apiURL      *url.URL
	apiSecret   []byte
}

type Registry struct {
	cfg config.PluginConfig

	mu          sync.RWMutex
	entries     map[string]*pluginEntry
	diagnostics map[string]string
	lastRefresh time.Time
	refreshMu   sync.Mutex
}

func NewRegistry(cfg config.PluginConfig) *Registry {
	cfg.Directory = strings.TrimSpace(cfg.Directory)
	if cfg.Directory == "" {
		cfg.Directory = "./data/plugins"
	}
	if cfg.RefreshIntervalSeconds <= 0 {
		cfg.RefreshIntervalSeconds = 2
	}
	if cfg.ProxyTimeoutSeconds <= 0 {
		cfg.ProxyTimeoutSeconds = 30
	}
	if cfg.MaxRequestBodyBytes <= 0 {
		cfg.MaxRequestBodyBytes = 2 * 1024 * 1024
	}
	if cfg.MaxResponseBodyBytes <= 0 {
		cfg.MaxResponseBodyBytes = 16 * 1024 * 1024
	}

	return &Registry{
		cfg:         cfg,
		entries:     make(map[string]*pluginEntry),
		diagnostics: make(map[string]string),
	}
}

func (r *Registry) Config() config.PluginConfig {
	return r.cfg
}

func (r *Registry) List(role string) []PublicPlugin {
	if !r.cfg.Enabled {
		return []PublicPlugin{}
	}
	r.ensureFresh()

	r.mu.RLock()
	defer r.mu.RUnlock()

	items := make([]PublicPlugin, 0, len(r.entries))
	for _, entry := range r.entries {
		if !roleCanAccess(role, entry.manifest.Visibility) {
			continue
		}
		items = append(items, entry.manifest.public())
	}
	slices.SortFunc(items, func(a, b PublicPlugin) int {
		if a.SortOrder != b.SortOrder {
			return a.SortOrder - b.SortOrder
		}
		return strings.Compare(a.ID, b.ID)
	})
	return items
}

func (r *Registry) get(id, role string) (*pluginEntry, bool) {
	entry, ok := r.getAny(id)
	if ok && !roleCanAccess(role, entry.manifest.Visibility) {
		return nil, false
	}
	return entry, ok
}

func (r *Registry) getAny(id string) (*pluginEntry, bool) {
	if !r.cfg.Enabled || !pluginIDPattern.MatchString(id) {
		return nil, false
	}
	r.ensureFresh()

	r.mu.RLock()
	entry, ok := r.entries[id]
	r.mu.RUnlock()
	return entry, ok
}

// GamePolicy describes the balance-movement permissions of a game plugin.
type GamePolicy struct {
	ID        string
	MaxStake  float64
	MaxPayout float64
}

// GamePolicy authenticates a game sidecar bearer token and returns its caps.
// Only enabled plugins that declared game.enabled may move balance, and the
// token must match the plugin's shared .api-secret.
func (r *Registry) GamePolicy(id, bearer string) (*GamePolicy, bool) {
	entry, ok := r.getAny(id)
	if !ok || entry.manifest.Game == nil || !entry.manifest.Game.Enabled {
		return nil, false
	}
	if len(entry.apiSecret) == 0 || !validBearer(entry.apiSecret, bearer) {
		return nil, false
	}
	return &GamePolicy{
		ID:        id,
		MaxStake:  entry.manifest.Game.MaxStake,
		MaxPayout: entry.manifest.Game.MaxPayout,
	}, true
}

func validBearer(secret []byte, bearer string) bool {
	token, ok := strings.CutPrefix(strings.TrimSpace(bearer), "Bearer ")
	if !ok {
		return false
	}
	return len(token) == len(secret) && subtle.ConstantTimeCompare([]byte(token), secret) == 1
}

func (r *Registry) Status() RegistryStatus {
	if r.cfg.Enabled {
		r.ensureFresh()
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	plugins := make([]RegistryPluginStatus, 0, len(r.entries))
	for _, entry := range r.entries {
		plugins = append(plugins, RegistryPluginStatus{
			PublicPlugin: entry.manifest.public(),
			Runtime:      entry.manifest.Runtime.Type,
		})
	}
	slices.SortFunc(plugins, func(a, b RegistryPluginStatus) int {
		return strings.Compare(a.ID, b.ID)
	})
	diagnostics := make(map[string]string, len(r.diagnostics))
	for id, message := range r.diagnostics {
		diagnostics[id] = message
	}
	return RegistryStatus{
		Enabled:     r.cfg.Enabled,
		LastRefresh: r.lastRefresh,
		Plugins:     plugins,
		Errors:      diagnostics,
	}
}

func (r *Registry) ForceRefresh() error {
	if !r.cfg.Enabled {
		return nil
	}

	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	return r.refreshLocked()
}

func roleCanAccess(role, visibility string) bool {
	return visibility == "user" || (visibility == "admin" && role == "admin")
}

func (r *Registry) ensureFresh() {
	interval := time.Duration(r.cfg.RefreshIntervalSeconds) * time.Second
	r.mu.RLock()
	fresh := !r.lastRefresh.IsZero() && time.Since(r.lastRefresh) < interval
	r.mu.RUnlock()
	if fresh {
		return
	}

	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	r.mu.RLock()
	fresh = !r.lastRefresh.IsZero() && time.Since(r.lastRefresh) < interval
	r.mu.RUnlock()
	if fresh {
		return
	}

	if err := r.refreshLocked(); err != nil {
		slog.Warn("plugin registry refresh failed; keeping previous snapshot", "error", err)
		r.mu.Lock()
		r.lastRefresh = time.Now()
		r.mu.Unlock()
	}
}

func (r *Registry) refreshLocked() error {
	entries, diagnostics, err := r.scan()
	if err != nil {
		return err
	}

	r.mu.Lock()
	previousDiagnostics := r.diagnostics
	r.entries = entries
	r.diagnostics = diagnostics
	r.lastRefresh = time.Now()
	r.mu.Unlock()

	for id, message := range diagnostics {
		if previousDiagnostics[id] != message {
			slog.Warn("plugin disabled by invalid manifest", "plugin", id, "error", message)
		}
	}
	return nil
}

func (r *Registry) scan() (map[string]*pluginEntry, map[string]string, error) {
	directories, err := os.ReadDir(r.cfg.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]*pluginEntry), make(map[string]string), nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read plugin directory %q: %w", r.cfg.Directory, err)
	}

	entries := make(map[string]*pluginEntry)
	diagnostics := make(map[string]string)
	for _, directory := range directories {
		if !directory.IsDir() || strings.HasPrefix(directory.Name(), ".") {
			continue
		}
		entry, err := r.loadPlugin(directory.Name())
		if err != nil {
			diagnostics[directory.Name()] = err.Error()
			continue
		}
		if !entry.manifest.Enabled {
			continue
		}
		entries[entry.manifest.ID] = entry
	}
	return entries, diagnostics, nil
}

func (r *Registry) loadPlugin(directoryName string) (*pluginEntry, error) {
	root := filepath.Join(r.cfg.Directory, directoryName)
	manifestPath, err := resolveInside(root, "manifest.json")
	if err != nil {
		return nil, fmt.Errorf("manifest path: %w", err)
	}
	manifestBytes, err := readLimitedFile(manifestPath, maxManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := manifest.normalizeAndValidate(directoryName); err != nil {
		return nil, err
	}

	entry := &pluginEntry{manifest: manifest, root: root}
	if !manifest.Enabled {
		return entry, nil
	}
	if manifest.Runtime.Type == "static" {
		entryPath, err := resolveInside(root, manifest.Runtime.Entry)
		if err != nil {
			return nil, fmt.Errorf("runtime.entry: %w", err)
		}
		info, err := os.Stat(entryPath)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("runtime.entry must reference a regular file")
		}
		entry.staticEntry = entryPath
		entry.staticRoot = filepath.Dir(entryPath)
	} else {
		entry.runtimeURL, _ = parsePluginBaseURL(manifest.Runtime.BaseURL, manifest.ID)
	}

	if manifest.API != nil {
		entry.apiURL, _ = parsePluginBaseURL(manifest.API.BaseURL, manifest.ID)
		secretPath, err := resolveInside(root, manifest.API.SecretFile)
		if err != nil {
			return nil, fmt.Errorf("api.secret_file: %w", err)
		}
		secret, err := readLimitedFile(secretPath, 4096)
		if err != nil {
			return nil, fmt.Errorf("read api.secret_file: %w", err)
		}
		entry.apiSecret = bytes.TrimSpace(secret)
		if !validAPISecret(entry.apiSecret) {
			return nil, fmt.Errorf("api.secret_file must contain 32-256 printable ASCII characters without spaces")
		}
	}

	return entry, nil
}

func validAPISecret(secret []byte) bool {
	if len(secret) < minAPISecretBytes || len(secret) > 256 {
		return false
	}
	for _, value := range secret {
		if value <= 0x20 || value >= 0x7f {
			return false
		}
	}
	return true
}

func resolveInside(root, relative string) (string, error) {
	if err := validateRelativePath(relative); err != nil {
		return "", err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(absoluteRoot, filepath.FromSlash(relative))
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", err
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes plugin directory")
	}
	return resolvedCandidate, nil
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d bytes", limit)
	}
	return data, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("unexpected trailing JSON value")
}
