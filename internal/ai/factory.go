package ai

import (
	"fmt"
	"net/http"

	"rulerefinery/internal/config"
)

// NewClient 创建 AI 客户端
func NewClient(aiConfig config.AIConfig, httpClient *http.Client) (Client, error) {
	if !aiConfig.IsAIEnabled() {
		return nil, fmt.Errorf("AI is not enabled: provider or API key is missing")
	}

	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// 构造 ProviderConfig 用于初始化具体的客户端
	providerCfg := config.ProviderConfig{
		Enabled:     true,
		APIKey:      aiConfig.APIKey,
		BaseURL:     aiConfig.BaseURL,
		Model:       aiConfig.Model,
		Prompt:      "", // 不再使用通用 prompt，改用 Prompts 中的特定 prompt
		MaxTokens:   aiConfig.MaxTokens,
		Temperature: aiConfig.Temperature,
	}

	switch aiConfig.Provider {
	case "openai":
		return NewOpenAIClient(providerCfg, httpClient), nil
	case "grok":
		return NewGrokClient(providerCfg, httpClient), nil
	case "gemini":
		return NewGeminiClient(providerCfg, httpClient), nil
	case "deepseek":
		return NewDeepSeekClient(providerCfg, httpClient), nil
	default:
		return nil, fmt.Errorf("unsupported AI provider: %s", aiConfig.Provider)
	}
}
