package ai

import (
	"context"
	"net/http"

	"rulerefinery/internal/config"
)

// Client AI 客户端接口
type Client interface {
	// Chat 发送聊天请求并返回响应
	Chat(ctx context.Context, prompt string) (string, error)

	// GetProviderName 获取提供商名称
	GetProviderName() string
}

// BaseClient 基础客户端实现
type BaseClient struct {
	Config     config.ProviderConfig
	HTTPClient *http.Client
	Provider   string
}

// GetProviderName 实现 Client 接口
func (c *BaseClient) GetProviderName() string {
	return c.Provider
}

// ChatRequest 通用聊天请求结构
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream"`
}

// Message 消息结构
type Message struct {
	Role    string `json:"role"` // system, user, assistant
	Content string `json:"content"`
}

// ChatResponse 通用聊天响应结构
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice 选择项
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage token 使用情况
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
