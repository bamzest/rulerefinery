package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RuleSetsConfig 规则集配置
type RuleSetsConfig struct {
	ClassifiedRules map[string]RulesetConfig `yaml:"classified_rules"`
}

// RulesetConfig 规则集配置
type RulesetConfig struct {
	Description    string   `yaml:"description"`               // 规则集描述（可选）
	URLs           []string `yaml:"urls"`                      // URL 来源列表（可选）
	Files          []string `yaml:"files"`                     // 本地文件列表（可选）
	Rules          []string `yaml:"rules"`                     // 手工添加的规则内容（可选）
	ExcludeSources []string `yaml:"exclude_sources,omitempty"` // 排除的规则 URL 或本地路径（可选）
	Filters        []string `yaml:"filters,omitempty"`         // 规则内容过滤器（glob 模式，白名单）
	Excludes       []string `yaml:"excludes,omitempty"`        // 排除的规则内容（glob 模式，黑名单）
}

// LoadRuleSetsConfig 加载规则集配置文件
func LoadRuleSetsConfig(filePath string) (*RuleSetsConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取规则配置文件失败: %w", err)
	}

	var cfg RuleSetsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析规则配置文件失败: %w", err)
	}

	// 验证配置
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("规则配置验证失败: %w", err)
	}

	return &cfg, nil
}

// Validate 验证规则配置
func (c *RuleSetsConfig) Validate() error {
	// 验证规则集配置
	for name, ruleset := range c.ClassifiedRules {
		if len(ruleset.URLs) == 0 && len(ruleset.Files) == 0 && len(ruleset.Rules) == 0 {
			return fmt.Errorf("规则集 '%s' 没有配置 URL、本地文件或手工规则", name)
		}

		// 验证 URL 格式
		for i, url := range ruleset.URLs {
			if url == "" {
				return fmt.Errorf("规则集 '%s' 的第 %d 个 URL 为空", name, i+1)
			}
		}

		// 验证本地文件路径
		for i, file := range ruleset.Files {
			if file == "" {
				return fmt.Errorf("规则集 '%s' 的第 %d 个文件路径为空", name, i+1)
			}
		}
	}

	return nil
}

// GetAllRulesets 获取所有规则集名称
func (c *RuleSetsConfig) GetAllRulesets() []string {
	names := make([]string, 0, len(c.ClassifiedRules))
	for name := range c.ClassifiedRules {
		names = append(names, name)
	}
	return names
}

// GetRulesetConfig 获取指定规则集的配置
func (c *RuleSetsConfig) GetRulesetConfig(name string) (*RulesetConfig, error) {
	ruleset, exists := c.ClassifiedRules[name]
	if !exists {
		return nil, fmt.Errorf("规则集 '%s' 不存在", name)
	}
	return &ruleset, nil
}
