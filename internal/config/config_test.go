package config

import "testing"

func TestLoadBCSDoesNotRequireOpenAIConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("BCS_BASE_URL", "https://bcs.example.com")
	t.Setenv("BCS_API_TOKEN", "token")
	t.Setenv("BCS_INSECURE_SKIP_VERIFY", "false")

	config := LoadBCS()
	if config.BaseURL != "https://bcs.example.com" || config.APIToken != "token" || config.InsecureSkipVerify {
		t.Fatalf("LoadBCS() = %+v", config)
	}
}

func TestLoadOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("OPENAI_MODEL", "model")
	t.Setenv("OPENAI_BASE_URL", "https://openai.example.com")
	t.Setenv("OPENAI_BY_AZURE", "true")
	t.Setenv("OPENAI_REASONING_EFFORT", "high")

	cfg, err := LoadOpenAI()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "key" || cfg.Model != "model" || cfg.BaseURL != "https://openai.example.com" || !cfg.ByAzure || cfg.ReasoningEffort != "high" {
		t.Fatalf("LoadOpenAI() = %+v", cfg)
	}
}
