package rules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog/log"

	"rulerefinery/internal/ai"
	"rulerefinery/internal/config"
	"rulerefinery/internal/utils"

	"gopkg.in/yaml.v3"
)

// RuleCategory 规则分类结果
type RuleCategory struct {
	Name        string   `yaml:"-"`           // 分类名称（作为 map key）
	Description string   `yaml:"description"` // 分类描述
	URLs        []string `yaml:"urls"`        // URL 来源列表
	Files       []string `yaml:"files"`       // 本地文件列表
	Rules       []string `yaml:"rules"`       // 手工添加的规则内容
	Confidence  float64  `yaml:"-"`           // AI 分类置信度（内部使用）
}

// RuleClassificationResult AI 分类结果
type RuleClassificationResult struct {
	Categories map[string]RuleCategory
	Unmatched  []RuleFileInfo // 无法分类的规则
}

// ClassifyRulesWithAI 使用 AI 对规则文件进行分类
// promptFile: 可选的提示词文件路径，如果指定则将提示词保存到文件
func ClassifyRulesWithAI(ctx context.Context, ruleFiles []RuleFileInfo, aiClient ai.Client, existingRules *config.RuleSetsConfig, promptTemplate string, promptFile ...string) (*RuleClassificationResult, error) {
	if len(ruleFiles) == 0 {
		return &RuleClassificationResult{
			Categories: make(map[string]RuleCategory),
		}, nil
	}

	// 过滤出未分类的规则
	unclassifiedRules := filterUnclassifiedRules(ruleFiles, existingRules)
	if len(unclassifiedRules) == 0 {
		log.Info().Msg("所有规则已分类，无需 AI 处理")
		return &RuleClassificationResult{
			Categories: convertExistingRules(existingRules),
		}, nil
	}

	log.Info().Msgf("需要 AI 分类的规则文件: %d 个", len(unclassifiedRules))

	// 构建 AI 提示词
	prompt := buildClassificationPrompt(unclassifiedRules, promptTemplate)

	// 如果指定了提示词文件路径，则保存到文件
	if len(promptFile) > 0 && promptFile[0] != "" {
		if err := os.WriteFile(promptFile[0], []byte(prompt), 0644); err != nil {
			log.Warn().Msgf("保存AI提示词到文件失败: %v", err)
		} else {
			log.Info().Msgf("AI提示词已保存到: %s", promptFile[0])
		}
	}

	// 调用 AI 进行分类
	log.Info().Msg("正在使用 AI 分析规则内容...")
	response, err := aiClient.Chat(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("AI 分类失败: %w", err)
	}

	log.Info().Msgf("AI 响应已接收，响应长度: %d 字符", len(response))

	// 如果指定了提示词文件路径，追加保存 AI 响应结果
	if len(promptFile) > 0 && promptFile[0] != "" {
		responseContent := fmt.Sprintf("\n\n%s\n=== AI 响应结果 ===\n%s\n%s\n\n%s\n",
			strings.Repeat("=", 80),
			strings.Repeat("=", 80),
			strings.Repeat("=", 80),
			response)

		// 以追加模式打开文件
		f, err := os.OpenFile(promptFile[0], os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			log.Warn().Msgf("追加AI响应到文件失败: %v", err)
		} else {
			defer f.Close()
			if _, err := f.WriteString(responseContent); err != nil {
				log.Warn().Msgf("写入AI响应失败: %v", err)
			} else {
				log.Info().Msgf("AI响应已追加到: %s", promptFile[0])
			}
		}
	}

	// 解析 AI 响应
	result, err := parseClassificationResponse(response, unclassifiedRules)
	if err != nil {
		return nil, fmt.Errorf("解析 AI 响应失败: %w", err)
	}

	// 合并现有分类
	if existingRules != nil {
		mergeExistingRules(result, existingRules)
	}

	log.Info().Msgf("AI 分类完成: 生成 %d 个分类", len(result.Categories))
	return result, nil
}

// filterUnclassifiedRules 过滤出未分类的规则
func filterUnclassifiedRules(ruleFiles []RuleFileInfo, existingRules *config.RuleSetsConfig) []RuleFileInfo {
	if existingRules == nil || len(existingRules.ClassifiedRules) == 0 {
		return ruleFiles
	}

	// 构建已分类规则的 URL 映射
	classifiedURLs := make(map[string]bool)
	for _, ruleset := range existingRules.ClassifiedRules {
		for _, url := range ruleset.URLs {
			classifiedURLs[url] = true
		}
	}

	// 过滤
	var unclassified []RuleFileInfo
	for _, rule := range ruleFiles {
		if !classifiedURLs[rule.GitHubURL] {
			unclassified = append(unclassified, rule)
		}
	}

	return unclassified
}

// buildClassificationPrompt 构建 AI 分类提示词
func buildClassificationPrompt(ruleFiles []RuleFileInfo, promptTemplate string) string {
	// 构建规则文件信息
	var ruleFilesContent strings.Builder

	for i, rule := range ruleFiles {
		ruleFilesContent.WriteString(fmt.Sprintf("### 规则文件 %d\n", i+1))
		ruleFilesContent.WriteString(fmt.Sprintf("- 文件名: %s\n", rule.FileName))

		// URL: 优先使用 GitHubURL，如果为空则使用 FilePath（本地文件）
		urlOrPath := rule.GitHubURL
		if urlOrPath == "" {
			urlOrPath = rule.FilePath
		}
		ruleFilesContent.WriteString(fmt.Sprintf("- URL: %s\n", urlOrPath))

		ruleFilesContent.WriteString(fmt.Sprintf("- 规则数量: %d\n", rule.RuleCount))
		ruleFilesContent.WriteString(fmt.Sprintf("- 规则示例:\n```\n%s\n```\n\n", strings.Join(rule.Examples, "\n")))
	}

	// 使用模板替换占位符
	prompt := strings.ReplaceAll(promptTemplate, "{RULE_FILES_INFO}", ruleFilesContent.String())

	return prompt
}

// parseClassificationResponse 解析 AI 分类响应
func parseClassificationResponse(response string, ruleFiles []RuleFileInfo) (*RuleClassificationResult, error) {
	// 提取 YAML 代码块
	yamlContent := extractYAMLBlock(response)
	if yamlContent == "" {
		return nil, fmt.Errorf("未找到 YAML 格式的分类结果")
	}

	// 解析 YAML
	var parsed struct {
		ClassifiedRules map[string]struct {
			Description string   `yaml:"description"`
			URLs        []string `yaml:"urls"`
			Files       []string `yaml:"files"`
		} `yaml:"classified_rules"`
	}

	if err := yaml.Unmarshal([]byte(yamlContent), &parsed); err != nil {
		return nil, fmt.Errorf("解析 YAML 失败: %w", err)
	}

	// 转换为内部结构
	result := &RuleClassificationResult{
		Categories: make(map[string]RuleCategory),
	}

	// 构建 URL 到文件的映射
	urlToFile := make(map[string]RuleFileInfo)
	for _, file := range ruleFiles {
		urlToFile[file.GitHubURL] = file
	}

	// 转换分类结果
	classifiedURLs := make(map[string]bool)
	classifiedFiles := make(map[string]bool)
	for name, ruleset := range parsed.ClassifiedRules {
		category := RuleCategory{
			Name:        name,
			Description: ruleset.Description,
			URLs:        ruleset.URLs,
			Files:       ruleset.Files,
		}
		result.Categories[name] = category

		// 记录已分类的 URL 和本地文件
		for _, url := range ruleset.URLs {
			classifiedURLs[url] = true
		}
		for _, file := range ruleset.Files {
			classifiedFiles[file] = true
		}
	}

	// 找出未分类的规则
	for _, file := range ruleFiles {
		// 检查规则是否已分类（在 URLs 或 Files 中任意一个出现即视为已分类）
		isClassified := false

		// 检查 GitHubURL（如果存在）
		if file.GitHubURL != "" && classifiedURLs[file.GitHubURL] {
			isClassified = true
		}

		// 检查 FilePath（如果存在且尚未标记为已分类）
		// 使用标准化路径进行检查，确保 ./ 前缀不影响匹配
		if !isClassified && file.FilePath != "" {
			normalizedPath := utils.NormalizeLocalPath(file.FilePath)
			if classifiedFiles[normalizedPath] {
				isClassified = true
			} else if classifiedFiles[file.FilePath] {
				// 兼容：也检查原始路径
				isClassified = true
			}
		}

		if !isClassified {
			result.Unmatched = append(result.Unmatched, file)
		}
	}

	return result, nil
}

// extractYAMLBlock 提取 YAML 代码块
func extractYAMLBlock(text string) string {
	// 查找 ```yaml 或 ``` 代码块
	lines := strings.Split(text, "\n")
	var yamlLines []string
	inBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```yaml") || strings.HasPrefix(trimmed, "```yml") {
			inBlock = true
			continue
		} else if strings.HasPrefix(trimmed, "```") && !inBlock {
			inBlock = true
			continue
		} else if strings.HasPrefix(trimmed, "```") && inBlock {
			break
		}

		if inBlock {
			yamlLines = append(yamlLines, line)
		}
	}

	if len(yamlLines) > 0 {
		return strings.Join(yamlLines, "\n")
	}

	// 如果没有代码块，尝试直接解析整个响应
	if strings.Contains(text, "rulesets:") {
		return text
	}

	return ""
}

// mergeExistingRules 合并现有分类
func mergeExistingRules(result *RuleClassificationResult, existingRules *config.RuleSetsConfig) {
	for name, ruleset := range existingRules.ClassifiedRules {
		if existing, ok := result.Categories[name]; ok {
			// 合并 URLs（去重）
			urlSet := make(map[string]bool)
			for _, url := range existing.URLs {
				urlSet[url] = true
			}
			for _, url := range ruleset.URLs {
				urlSet[url] = true
			}

			urls := make([]string, 0, len(urlSet))
			for url := range urlSet {
				urls = append(urls, url)
			}

			// 合并 Files
			fileSet := make(map[string]bool)
			for _, file := range existing.Files {
				fileSet[file] = true
			}
			for _, file := range ruleset.Files {
				fileSet[file] = true
			}

			files := make([]string, 0, len(fileSet))
			for file := range fileSet {
				files = append(files, file)
			}

			result.Categories[name] = RuleCategory{
				Name:        name,
				Description: existing.Description,
				URLs:        urls,
				Files:       files,
			}
		} else {
			// 添加新分类
			result.Categories[name] = RuleCategory{
				Name:        name,
				Description: ruleset.Description,
				URLs:        ruleset.URLs,
				Files:       ruleset.Files,
			}
		}
	}
}

// convertExistingRules 转换现有规则为分类结果
func convertExistingRules(existingRules *config.RuleSetsConfig) map[string]RuleCategory {
	if existingRules == nil {
		return make(map[string]RuleCategory)
	}

	categories := make(map[string]RuleCategory)
	for name, ruleset := range existingRules.ClassifiedRules {
		categories[name] = RuleCategory{
			Name:        name,
			Description: ruleset.Description,
			URLs:        ruleset.URLs,
			Files:       ruleset.Files,
			Rules:       ruleset.Rules,
		}
	}
	return categories
}

// ExportToClassifiedRulesYAML 导出分类结果到 classified rules yaml 文件
func ExportToClassifiedRulesYAML(result *RuleClassificationResult, outputPath string) error {
	// 构建输出结构
	output := struct {
		ClassifiedRules map[string]config.RulesetConfig `yaml:"classified_rules"`
	}{
		ClassifiedRules: make(map[string]config.RulesetConfig),
	}

	for name, category := range result.Categories {
		output.ClassifiedRules[name] = config.RulesetConfig{
			Description: category.Description,
			URLs:        category.URLs,
			Files:       category.Files,
			Rules:       category.Rules,
		}
	}

	// 生成 YAML 内容
	yamlData, err := yaml.Marshal(&output)
	if err != nil {
		return fmt.Errorf("生成 YAML 失败: %w", err)
	}

	// 直接使用 YAML 内容，不添加注释头
	content := string(yamlData)

	// 确保目录存在
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	log.Info().Msgf("规则分类结果已保存到: %s", outputPath)

	// 如果有未分类的规则，输出警告
	if len(result.Unmatched) > 0 {
		log.Warn().Msgf("%d 个规则未能分类，请手动检查", len(result.Unmatched))
		unmatchedPath := strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "_unmatched.txt"
		if err := exportUnmatchedRules(result.Unmatched, unmatchedPath); err != nil {
			log.Info().Msgf("导出未分类规则失败: %v", err)
		} else {
			log.Info().Msgf("未分类规则列表已保存到: %s", unmatchedPath)
		}
	}

	return nil
}

// exportUnmatchedRules 导出未分类的规则列表
func exportUnmatchedRules(unmatched []RuleFileInfo, outputPath string) error {
	var sb strings.Builder
	sb.WriteString("# 未分类的规则文件\n")
	sb.WriteString("# 这些规则无法自动分类，请手动检查并添加到 rulesets.yaml\n\n")

	for i, rule := range unmatched {
		sb.WriteString(fmt.Sprintf("## %d. %s\n", i+1, rule.FileName))

		// URL: 优先使用 GitHubURL，如果为空则使用 FilePath（本地文件）
		urlOrPath := rule.GitHubURL
		if urlOrPath == "" {
			urlOrPath = rule.FilePath
		}
		sb.WriteString(fmt.Sprintf("URL: %s\n", urlOrPath))

		sb.WriteString(fmt.Sprintf("规则数量: %d\n", rule.RuleCount))
		sb.WriteString(fmt.Sprintf("示例规则:\n%s\n\n", strings.Join(rule.Examples, "\n")))
	}

	return os.WriteFile(outputPath, []byte(sb.String()), 0644)
}

// ExportClassifiedRulesConfig 导出完整的规则配置（包括现有和新增的）
func ExportClassifiedRulesConfig(ruleSets *config.RuleSetsConfig, outputPath string) error {
	// 构建输出结构
	output := struct {
		ClassifiedRules map[string]config.RulesetConfig `yaml:"classified_rules"`
	}{
		ClassifiedRules: ruleSets.ClassifiedRules,
	}

	// 生成 YAML 内容
	yamlData, err := yaml.Marshal(&output)
	if err != nil {
		return fmt.Errorf("生成 YAML 失败: %w", err)
	}

	// 确保目录存在
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(outputPath, yamlData, 0644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	log.Info().Msgf("规则配置已保存到: %s", outputPath)
	return nil
}
