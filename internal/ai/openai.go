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

// OpenAIClient OpenAI 客户端
type OpenAIClient struct {
	BaseClient
}

// NewOpenAIClient 创建 OpenAI 客户端
func NewOpenAIClient(cfg config.ProviderConfig, httpClient *http.Client) *OpenAIClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 1000
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.7
	}

	return &OpenAIClient{
		BaseClient: BaseClient{
			Config:     cfg,
			HTTPClient: httpClient,
			Provider:   "OpenAI",
		},
	}
}

// Chat 发送聊天请求
func (c *OpenAIClient) Chat(ctx context.Context, prompt string) (string, error) {
	messages := []Message{
		{
			Role:    "user",
			Content: prompt,
		},
	}

	// 如果有系统提示词，添加到开头
	if c.Config.Prompt != "" {
		messages = append([]Message{
			{
				Role:    "system",
				Content: c.Config.Prompt,
			},
		}, messages...)
	}

	reqBody := ChatRequest{
		Model:       c.Config.Model,
		Messages:    messages,
		MaxTokens:   c.Config.MaxTokens,
		Temperature: c.Config.Temperature,
		Stream:      false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.Config.BaseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Config.APIKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return chatResp.Choices[0].Message.Content, nil
}
