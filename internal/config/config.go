package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 主配置结构
type Config struct {
	Proxy           ProxyConfig            `yaml:"proxy"`
	AI              AIConfig               `yaml:"ai"`
	RuleSources     RuleSetsGenConfig      `yaml:"rule-sources"`
	AIClassifyRules AIClassifyRulesConfig  `yaml:"ai_classify_rules"`
	GenerateRules   GenerateRulesetsConfig `yaml:"generate_rules"`
	Logging         LoggingConfig          `yaml:"logging"`
}

// LoggingConfig 日志配置
type LoggingConfig struct {
	Level         string `yaml:"level"`
	OutputDir     string `yaml:"output_dir"`
	OutputFile    string `yaml:"output_file"`
	ConsoleOutput bool   `yaml:"console_output"`
	Format        string `yaml:"format"` // text 或 json，默认 text
}

// ProxyConfig 代理配置
type ProxyConfig struct {
	Enabled bool     `yaml:"enabled"`
	URLs    []string `yaml:"urls"` // 支持 socks5://、socks4://、http://、https://
}

// GitHubConfig GitHub 配置
type GitHubConfig struct {
	Token             string             `yaml:"token"`
	DownloadPath      string             `yaml:"download_path"` // 规则文件下载保存路径，默认 ./rulesets/github/rules
	Repositories      []RepositoryConfig `yaml:"repositories"`
	OrganizeByRepo    bool               `yaml:"organize_by_repo"`    // true=按owner/repo/branch组织目录, false=扁平化
	DownloadThreads   int                `yaml:"download_threads"`    // 并发下载线程数，默认10
	OverwriteRuleFile bool               `yaml:"overwrite_rule_file"` // true=覆盖已有规则文件, false=跳过已存在的文件（默认false）
}

// RepositoryConfig GitHub 仓库配置
type RepositoryConfig struct {
	Owner    string       `yaml:"owner"`
	Repo     string       `yaml:"repo"`
	Branch   string       `yaml:"branch"`
	Path     string       `yaml:"path"`     // 仓库内路径
	Filters  []FilterRule `yaml:"filters"`  // 过滤规则列表
	Excludes []string     `yaml:"excludes"` // 排除模式列表（支持 glob 模式，如 *_ipv6.list）
}

// FilterRule 过滤规则
type FilterRule struct {
	Pattern string `yaml:"pattern"` // glob 过滤模式
	Type    string `yaml:"type"`    // 规则类型: surge, quanx, clash-domain, clash-ipcidr, clash-classic
}

// AIClassifyRulesConfig AI 规则分类配置
type AIClassifyRulesConfig struct {
	Enabled                    bool   `yaml:"enabled"`                       // 是否启用
	ClassifiedRulesFile        string `yaml:"classified_rules_file"`         // 规则分类文件路径
	AIGeneratedClassifiedRules string `yaml:"ai_generated_classified_rules"` // AI 生成规则分类文件输出路径
}

// GenerateRulesetsConfig 规则集生成配置
type GenerateRulesetsConfig struct {
	Enabled         bool   `yaml:"enabled"`           // 是否启用
	OutputRulesPath string `yaml:"output_rules_path"` // 规则集输出目录
}

// RuleSetsGenConfig 规则集生成配置
type RuleSetsGenConfig struct {
	GitHub GitHubConfig `yaml:"github"` // GitHub 配置
}

// AIConfig AI 配置
type AIConfig struct {
	Provider         string         `yaml:"provider"`           // AI 提供商 (openai/grok/gemini/deepseek)
	APIKey           string         `yaml:"api_key"`            // API Key
	BaseURL          string         `yaml:"base_url"`           // API Base URL（可选，使用默认值）
	Model            string         `yaml:"model"`              // 模型名称（可选，使用默认值）
	MaxTokens        int            `yaml:"max_tokens"`         // 最大 token 数（可选，默认 1000）
	Temperature      float64        `yaml:"temperature"`        // 温度参数 0.0-2.0（可选，默认 0.7）
	AIRequestTimeout int            `yaml:"ai_request_timeout"` // AI 请求超时时间（秒，默认 120）
	RuleBatchSize    int            `yaml:"rule_batch_size"`    // 每批次分析的规则文件数量（默认 10）
	BatchConcurrency int            `yaml:"batch_concurrency"`  // 并发批次数量（默认 10）
	Prompts          AIPromptConfig `yaml:"prompts"`            // AI 提示词配置
}

// AIPromptConfig AI 提示词配置
type AIPromptConfig struct {
	RuleClassification string `yaml:"rule_classification"` // 规则分类提示词
}

// ProviderConfig AI 提供商配置（内部使用）
type ProviderConfig struct {
	Enabled     bool    `yaml:"enabled"`
	APIKey      string  `yaml:"api_key"`
	BaseURL     string  `yaml:"base_url"`
	Model       string  `yaml:"model"`
	Prompt      string  `yaml:"prompt"` // 已废弃，保留用于兼容
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
}

// LoadConfig 加载配置文件
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// 设置 AI 规则批次大小默认值
	if cfg.AI.RuleBatchSize <= 0 {
		cfg.AI.RuleBatchSize = 10
	}

	// 设置 AI 批次并发数默认值
	if cfg.AI.BatchConcurrency <= 0 {
		cfg.AI.BatchConcurrency = 10
	}

	// 设置 GitHub 下载路径默认值
	if cfg.RuleSources.GitHub.DownloadPath == "" {
		cfg.RuleSources.GitHub.DownloadPath = "./rule_sources/github/rules"
	}

	// 设置 GitHub 并发下载线程数默认值
	if cfg.RuleSources.GitHub.DownloadThreads <= 0 {
		cfg.RuleSources.GitHub.DownloadThreads = 10
	}

	// OverwriteRuleFile 默认为 false（不覆盖已有文件）
	// 注意：YAML 的 bool 零值就是 false，这里仅作说明

	// 设置日志配置默认值
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.OutputDir == "" {
		cfg.Logging.OutputDir = "log"
	}
	if cfg.Logging.OutputFile == "" {
		cfg.Logging.OutputFile = "app.log"
	}
	// ConsoleOutput 默认为 true（如果配置文件中没有显式设置 false）

	return &cfg, nil
}

// IsAIEnabled 检查 AI 是否已启用
func (c *AIConfig) IsAIEnabled() bool {
	return c.Provider != "" && c.APIKey != ""
}

// ValidateAIPrompts 验证 AI 提示词配置
func (c *AIConfig) ValidateAIPrompts() error {
	if c.Prompts.RuleClassification == "" {
		return fmt.Errorf("AI 提示词配置错误: prompts.rule_classification 不能为空")
	}
	return nil
}
