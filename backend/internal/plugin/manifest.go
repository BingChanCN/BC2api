package pluginruntime

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const ManifestSchemaVersion = 1

var pluginIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// Manifest is the hot-reloaded contract stored in <plugin-dir>/<id>/manifest.json.
type Manifest struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	Description   string          `json:"description,omitempty"`
	Enabled       bool            `json:"enabled"`
	Visibility    string          `json:"visibility"`
	SortOrder     int             `json:"sort_order"`
	Runtime       RuntimeManifest `json:"runtime"`
	API           *APIManifest    `json:"api,omitempty"`
	Game          *GameManifest   `json:"game,omitempty"`
}

type RuntimeManifest struct {
	Type    string `json:"type"`
	Entry   string `json:"entry,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
	SPA     bool   `json:"spa,omitempty"`
}

type APIManifest struct {
	BaseURL        string   `json:"base_url"`
	SecretFile     string   `json:"secret_file"`
	AllowedMethods []string `json:"allowed_methods,omitempty"`
}

// GameManifest declares that a plugin may move user balance through the
// internal game ledger. Caps are enforced by the main service on every
// transaction; 0 means the operation is disabled for this game.
type GameManifest struct {
	Enabled   bool    `json:"enabled"`
	MaxStake  float64 `json:"max_stake"`
	MaxPayout float64 `json:"max_payout"`
}

// PublicPlugin is safe to return to an authenticated browser.
type PublicPlugin struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Visibility  string `json:"visibility"`
	SortOrder   int    `json:"sort_order"`
	HasAPI      bool   `json:"has_api"`
	UIPath      string `json:"ui_path"`
}

func (m Manifest) public() PublicPlugin {
	return PublicPlugin{
		ID:          m.ID,
		Name:        m.Name,
		Version:     m.Version,
		Description: m.Description,
		Visibility:  m.Visibility,
		SortOrder:   m.SortOrder,
		HasAPI:      m.API != nil,
		UIPath:      "/plugin-runtime/" + m.ID + "/ui/",
	}
}

func (m *Manifest) normalizeAndValidate(directoryName string) error {
	m.ID = strings.TrimSpace(m.ID)
	m.Name = strings.TrimSpace(m.Name)
	m.Version = strings.TrimSpace(m.Version)
	m.Description = strings.TrimSpace(m.Description)
	m.Visibility = strings.ToLower(strings.TrimSpace(m.Visibility))
	m.Runtime.Type = strings.ToLower(strings.TrimSpace(m.Runtime.Type))
	m.Runtime.Entry = strings.TrimSpace(m.Runtime.Entry)
	m.Runtime.BaseURL = strings.TrimSpace(m.Runtime.BaseURL)

	if m.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf("schema_version must be %d", ManifestSchemaVersion)
	}
	if !pluginIDPattern.MatchString(m.ID) {
		return fmt.Errorf("id must match %s", pluginIDPattern)
	}
	if directoryName != m.ID {
		return fmt.Errorf("plugin directory %q must match id %q", directoryName, m.ID)
	}
	if m.Name == "" || len(m.Name) > 80 {
		return fmt.Errorf("name must contain 1-80 characters")
	}
	if len(m.Version) > 40 {
		return fmt.Errorf("version must not exceed 40 characters")
	}
	if len(m.Description) > 500 {
		return fmt.Errorf("description must not exceed 500 characters")
	}
	if m.Visibility != "user" && m.Visibility != "admin" {
		return fmt.Errorf("visibility must be user or admin")
	}

	switch m.Runtime.Type {
	case "static":
		if m.Runtime.Entry == "" {
			m.Runtime.Entry = "public/index.html"
		}
		if err := validateRelativePath(m.Runtime.Entry); err != nil {
			return fmt.Errorf("runtime.entry: %w", err)
		}
		entryPath := filepath.ToSlash(filepath.Clean(filepath.FromSlash(m.Runtime.Entry)))
		if !strings.HasPrefix(entryPath, "public/") {
			return fmt.Errorf("runtime.entry must be inside public/")
		}
		m.Runtime.Entry = entryPath
		m.Runtime.BaseURL = ""
	case "proxy":
		if _, err := parsePluginBaseURL(m.Runtime.BaseURL, m.ID); err != nil {
			return fmt.Errorf("runtime.base_url: %w", err)
		}
		m.Runtime.Entry = ""
	default:
		return fmt.Errorf("runtime.type must be static or proxy")
	}

	if m.API != nil {
		m.API.BaseURL = strings.TrimSpace(m.API.BaseURL)
		m.API.SecretFile = strings.TrimSpace(m.API.SecretFile)
		if _, err := parsePluginBaseURL(m.API.BaseURL, m.ID); err != nil {
			return fmt.Errorf("api.base_url: %w", err)
		}
		if err := validateRelativePath(m.API.SecretFile); err != nil {
			return fmt.Errorf("api.secret_file: %w", err)
		}
		if !strings.HasPrefix(filepath.Base(m.API.SecretFile), ".") {
			return fmt.Errorf("api.secret_file must use a hidden filename")
		}
		methods, err := normalizeMethods(m.API.AllowedMethods)
		if err != nil {
			return err
		}
		m.API.AllowedMethods = methods
	}

	if m.Game != nil && m.Game.Enabled {
		if m.API == nil {
			return fmt.Errorf("game.enabled requires an api section with a shared secret_file")
		}
		if m.Game.MaxStake <= 0 {
			return fmt.Errorf("game.max_stake must be greater than 0")
		}
		if m.Game.MaxPayout <= 0 {
			return fmt.Errorf("game.max_payout must be greater than 0")
		}
	}

	return nil
}

func normalizeMethods(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{"DELETE", "GET", "PATCH", "POST", "PUT"}, nil
	}
	allowed := map[string]bool{
		"DELETE": true,
		"GET":    true,
		"HEAD":   true,
		"PATCH":  true,
		"POST":   true,
		"PUT":    true,
	}
	seen := make(map[string]struct{}, len(values))
	methods := make([]string, 0, len(values))
	for _, value := range values {
		method := strings.ToUpper(strings.TrimSpace(value))
		if !allowed[method] {
			return nil, fmt.Errorf("api.allowed_methods contains unsupported method %q", value)
		}
		if _, ok := seen[method]; ok {
			continue
		}
		seen[method] = struct{}{}
		methods = append(methods, method)
	}
	slices.Sort(methods)
	return methods, nil
}

func validateRelativePath(value string) error {
	if value == "" || filepath.IsAbs(value) {
		return fmt.Errorf("must be a non-empty relative path")
	}
	cleaned := filepath.Clean(filepath.FromSlash(value))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("must stay inside the plugin directory")
	}
	return nil
}

func parsePluginBaseURL(raw, pluginID string) (*url.URL, error) {
	parsed, err := parseBaseURL(raw)
	if err != nil {
		return nil, err
	}
	expectedHost := "sub2api-plugin-" + pluginID
	if parsed.Hostname() != expectedHost {
		return nil, fmt.Errorf("host must be %q", expectedHost)
	}
	return parsed, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("must be an absolute http(s) URL")
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return nil, fmt.Errorf("must not contain credentials, query, or fragment")
	}
	return parsed, nil
}
