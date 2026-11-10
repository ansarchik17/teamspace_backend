package config

import (
	"os"
	"testing"
	"time"
)

func TestGetFlexibleDurationSupportsDurationString(t *testing.T) {
	t.Setenv("IDP_STATE_TTL", "10m")

	got := getFlexibleDuration([]string{"IDP_STATE_TTL", "IDP_STATE_TTL_SECONDS"}, time.Minute)
	if got != 10*time.Minute {
		t.Fatalf("expected 10m, got %s", got)
	}
}

func TestGetFlexibleDurationSupportsSecondsInteger(t *testing.T) {
	t.Setenv("IDP_STATE_TTL_SECONDS", "600")

	got := getFlexibleDuration([]string{"IDP_STATE_TTL", "IDP_STATE_TTL_SECONDS"}, time.Minute)
	if got != 600*time.Second {
		t.Fatalf("expected 600s, got %s", got)
	}
}

func TestGetEnvAsSliceTrimsCommaSeparatedValues(t *testing.T) {
	t.Setenv("IDP_SCOPES", "user:basic:read, user:phone:read, user:email:read")

	got := getEnvAsSlice("IDP_SCOPES", nil)
	want := []string{"user:basic:read", "user:phone:read", "user:email:read"}
	if len(got) != len(want) {
		t.Fatalf("expected %d scopes, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected scope at index %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestLoadUsesConfiguredAlemDefaultModel(t *testing.T) {
	t.Setenv("ALEM_DEFAULT_MODEL", "openai/gpt-oss-120b")
	t.Setenv("ALEM_MODEL", "alemgpt-intent")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Alem.DefaultModel != "openai/gpt-oss-120b" {
		t.Fatalf("unexpected Alem default model: %q", cfg.Alem.DefaultModel)
	}
}

func TestLoadFallsBackToWorkingAlemModel(t *testing.T) {
	t.Setenv("ALEM_DEFAULT_MODEL", "")
	t.Setenv("ALEM_MODEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Alem.DefaultModel != "openai/gpt-oss-120b" {
		t.Fatalf("unexpected fallback Alem model: %q", cfg.Alem.DefaultModel)
	}
}

func TestLoadEmptyKazModelDisablesKazLLM(t *testing.T) {
	t.Setenv("ALEM_KAZ_MODEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Alem.KazModel != "" {
		t.Fatalf("empty ALEM_KAZ_MODEL must disable KazLLM, got %q", cfg.Alem.KazModel)
	}
}

func TestLoadUnsetKazModelUsesDefault(t *testing.T) {
	t.Setenv("ALEM_KAZ_MODEL", "") // restores the original value after the test
	if err := os.Unsetenv("ALEM_KAZ_MODEL"); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Alem.KazModel != "astanahub/alemllm" {
		t.Fatalf("unexpected default KazLLM model: %q", cfg.Alem.KazModel)
	}
}
