package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"rulerefinery/internal/config"
)

// GeminiClient Gemini 客户端
type GeminiClient struct {
	BaseClient
}

// NewGeminiClient 创建 Gemini 客户端
func NewGeminiClient(cfg config.ProviderConfig, httpClient *http.Client) *GeminiClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
	}
	if cfg.Model == "" {
		cfg.Model = "gemini-pro"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 1000
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}

	return &GeminiClient{
		BaseClient: BaseClient{
			Config:     cfg,
			HTTPClient: httpClient,
			Provider:   "Gemini",
		},
	}
}

// GeminiRequest Gemini 请求结构
type GeminiRequest struct {
	Contents          []GeminiContent          `json:"contents"`
	GenerationConfig  GeminiGenerationConfig   `json:"generationConfig,omitempty"`
	SystemInstruction *GeminiSystemInstruction `json:"systemInstruction,omitempty"`
}

// GeminiContent 内容结构
type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart 内容部分
type GeminiPart struct {
	Text string `json:"text"`
}

// GeminiSystemInstruction 系统指令
type GeminiSystemInstruction struct {
	Parts []GeminiPart `json:"parts"`
}

// GeminiGenerationConfig 生成配置
type GeminiGenerationConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

// GeminiResponse Gemini 响应结构
type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
}

// GeminiCandidate 候选项
type GeminiCandidate struct {
	Content      GeminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

// Chat 发送聊天请求
func (c *GeminiClient) Chat(ctx context.Context, prompt string) (string, error) {
	reqBody := GeminiRequest{
		Contents: []GeminiContent{
			{
				Parts: []GeminiPart{
					{Text: prompt},
				},
			},
		},
		GenerationConfig: GeminiGenerationConfig{
			Temperature:     c.Config.Temperature,
			MaxOutputTokens: c.Config.MaxTokens,
		},
	}

	// 如果有系统提示词，添加 systemInstruction
	if c.Config.Prompt != "" {
		reqBody.SystemInstruction = &GeminiSystemInstruction{
			Parts: []GeminiPart{
				{Text: c.Config.Prompt},
			},
		}
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	// Gemini API URL 格式: /models/{model}:generateContent?key={api_key}
	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s",
		c.Config.BaseURL, c.Config.Model, c.Config.APIKey)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content in response")
	}

	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
}
