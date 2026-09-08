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

type BCSConfig struct {
	BaseURL            string
	APIToken           string
	InsecureSkipVerify bool
}

type Config struct {
	OpenAI OpenAIConfig
	BCS    BCSConfig
}

func Load() (Config, error) {
	openAI, err := LoadOpenAI()
	if err != nil {
		return Config{}, err
	}
	return Config{OpenAI: openAI, BCS: LoadBCS()}, nil
}

func LoadOpenAI() (OpenAIConfig, error) {
	cfg := OpenAIConfig{
		APIKey:          os.Getenv("OPENAI_API_KEY"),
		Model:           os.Getenv("OPENAI_MODEL"),
		BaseURL:         os.Getenv("OPENAI_BASE_URL"),
		ByAzure:         os.Getenv("OPENAI_BY_AZURE") == "true",
		ReasoningEffort: os.Getenv("OPENAI_REASONING_EFFORT"),
	}
	if cfg.APIKey == "" {
		return OpenAIConfig{}, fmt.Errorf("缺少环境变量 OPENAI_API_KEY")
	}
	if cfg.Model == "" {
		return OpenAIConfig{}, fmt.Errorf("缺少环境变量 OPENAI_MODEL")
	}
	return cfg, nil
}

// LoadBCS 读取不依赖模型配置的 BCS 客户端配置。
func LoadBCS() BCSConfig {
	return BCSConfig{
		BaseURL:            os.Getenv("BCS_BASE_URL"),
		APIToken:           os.Getenv("BCS_API_TOKEN"),
		InsecureSkipVerify: os.Getenv("BCS_INSECURE_SKIP_VERIFY") != "false",
	}
}
