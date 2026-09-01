package config

import (
	"fmt"
	"os"
)

type OpenAIConfig struct {
	APIKey          string
	Model           string
	BaseURL         string
	ByAzure         bool
	ReasoningEffort string
}

type Config struct {
	OpenAI OpenAIConfig
}

func Load() (Config, error) {
	cfg := Config{
		OpenAI: OpenAIConfig{
			APIKey:          os.Getenv("OPENAI_API_KEY"),
			Model:           os.Getenv("OPENAI_MODEL"),
			BaseURL:         os.Getenv("OPENAI_BASE_URL"),
			ByAzure:         os.Getenv("OPENAI_BY_AZURE") == "true",
			ReasoningEffort: os.Getenv("OPENAI_REASONING_EFFORT"),
		},
	}

	if cfg.OpenAI.APIKey == "" {
		return Config{}, fmt.Errorf("缺少环境变量 OPENAI_API_KEY")
	}
	if cfg.OpenAI.Model == "" {
		return Config{}, fmt.Errorf("缺少环境变量 OPENAI_MODEL")
	}

	return cfg, nil
}
