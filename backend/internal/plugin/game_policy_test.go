package pluginruntime

import (
	"testing"
)

func TestGamePolicyAuthAndCaps(t *testing.T) {
	root := t.TempDir()
	secret := "0123456789abcdef0123456789abcdef"
	writeTestPlugin(t, root, Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ID:            "coinflip",
		Name:          "Coinflip",
		Version:       "1.0.0",
		Enabled:       true,
		Visibility:    "user",
		Runtime:       RuntimeManifest{Type: "static", Entry: "public/index.html"},
		API:           &APIManifest{BaseURL: "http://sub2api-plugin-coinflip:8080", SecretFile: ".api-secret"},
		Game:          &GameManifest{Enabled: true, MaxStake: 10, MaxPayout: 100},
	}, "<h1>coinflip</h1>", secret)

	registry := newTestRegistry(root)
	if err := registry.ForceRefresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	policy, ok := registry.GamePolicy("coinflip", "Bearer "+secret)
	if !ok {
		t.Fatal("valid bearer rejected")
	}
	if policy.MaxStake != 10 || policy.MaxPayout != 100 {
		t.Fatalf("caps = %+v, want 10/100", policy)
	}

	if _, ok := registry.GamePolicy("coinflip", "Bearer wrong-token-000000000000000000"); ok {
		t.Fatal("wrong bearer accepted")
	}
	if _, ok := registry.GamePolicy("coinflip", secret); ok {
		t.Fatal("bearer without prefix accepted")
	}
	if _, ok := registry.GamePolicy("unknown-game", "Bearer "+secret); ok {
		t.Fatal("unknown game accepted")
	}
}

func TestGamePolicyRejectsDisabledGame(t *testing.T) {
	root := t.TempDir()
	secret := "0123456789abcdef0123456789abcdef"
	writeTestPlugin(t, root, Manifest{
		SchemaVersion: ManifestSchemaVersion,
		ID:            "coinflip",
		Name:          "Coinflip",
		Version:       "1.0.0",
		Enabled:       true,
		Visibility:    "user",
		Runtime:       RuntimeManifest{Type: "static", Entry: "public/index.html"},
		API:           &APIManifest{BaseURL: "http://sub2api-plugin-coinflip:8080", SecretFile: ".api-secret"},
		Game:          &GameManifest{Enabled: true, MaxStake: 10, MaxPayout: 100},
	}, "<h1>coinflip</h1>", secret)

	registry := newTestRegistry(root)
	if err := registry.ForceRefresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	manifest := readTestManifest(t, root+"/coinflip/manifest.json")
	manifest.Enabled = false
	writeJSONFile(t, root+"/coinflip/manifest.json", manifest)
	if err := registry.ForceRefresh(); err != nil {
		t.Fatalf("refresh after disable: %v", err)
	}

	if _, ok := registry.GamePolicy("coinflip", "Bearer "+secret); ok {
		t.Fatal("disabled game still authenticated")
	}
}

func TestGameManifestValidation(t *testing.T) {
	base := func() Manifest {
		return Manifest{
			SchemaVersion: ManifestSchemaVersion,
			ID:            "g",
			Name:          "g",
			Version:       "1",
			Enabled:       true,
			Visibility:    "user",
			Runtime:       RuntimeManifest{Type: "static", Entry: "public/index.html"},
		}
	}

	// game.enabled 但缺 api 段
	m := base()
	m.Game = &GameManifest{Enabled: true, MaxStake: 1, MaxPayout: 1}
	if err := m.normalizeAndValidate("g"); err == nil {
		t.Fatal("game without api section accepted")
	}

	// 缺少正数上限
	m = base()
	m.API = &APIManifest{BaseURL: "http://sub2api-plugin-g:8080", SecretFile: ".api-secret"}
	m.Game = &GameManifest{Enabled: true, MaxStake: 0, MaxPayout: 1}
	if err := m.normalizeAndValidate("g"); err == nil {
		t.Fatal("zero max_stake accepted")
	}

	// 合法配置
	m = base()
	m.API = &APIManifest{BaseURL: "http://sub2api-plugin-g:8080", SecretFile: ".api-secret"}
	m.Game = &GameManifest{Enabled: true, MaxStake: 10, MaxPayout: 100}
	if err := m.normalizeAndValidate("g"); err != nil {
		t.Fatalf("valid game manifest rejected: %v", err)
	}
}
