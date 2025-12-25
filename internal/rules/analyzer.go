package rules

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// RuleFileInfo 规则文件信息
type RuleFileInfo struct {
	FilePath  string   // 文件路径
	FileName  string   // 文件名
	GitHubURL string   // GitHub Raw URL
	RuleCount int      // 规则总数
	Examples  []string // 规则示例（前N条）
}

// AnalyzeRuleFiles 分析规则文件
func AnalyzeRuleFiles(filePaths []string, exampleCount int) ([]RuleFileInfo, error) {
	var results []RuleFileInfo

	for _, filePath := range filePaths {
		info, err := analyzeRuleFile(filePath, exampleCount)
		if err != nil {
			// 跳过错误的文件，继续处理其他文件
			continue
		}
		results = append(results, info)
	}

	return results, nil
}

// analyzeRuleFile 分析单个规则文件
func analyzeRuleFile(filePath string, exampleCount int) (RuleFileInfo, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return RuleFileInfo{}, err
	}
	defer file.Close()

	var examples []string
	ruleCount := 0

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}

		ruleCount++

		// 收集示例
		if len(examples) < exampleCount {
			examples = append(examples, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return RuleFileInfo{}, err
	}

	return RuleFileInfo{
		FilePath:  filePath,
		FileName:  extractFileName(filePath),
		RuleCount: ruleCount,
		Examples:  examples,
	}, nil
}

// FormatRuleFilesBatchForAI 格式化规则文件批次用于 AI 分析
func FormatRuleFilesBatchForAI(batch []RuleFileInfo) string {
	var builder strings.Builder

	for i, info := range batch {
		builder.WriteString(fmt.Sprintf("### 规则文件 %d: %s\n", i+1, extractFileName(info.FilePath)))
		builder.WriteString(fmt.Sprintf("- 文件路径: %s\n", info.FilePath))
		if info.GitHubURL != "" {
			builder.WriteString(fmt.Sprintf("- GitHub URL: %s\n", info.GitHubURL))
		}
		builder.WriteString(fmt.Sprintf("- 规则总数: %d 条\n", info.RuleCount))
		builder.WriteString("- 规则示例:\n")

		for j, example := range info.Examples {
			builder.WriteString(fmt.Sprintf("  %d. %s\n", j+1, example))
		}
		builder.WriteString("\n")
	}

	return builder.String()
}

// extractFileName 从路径中提取文件名
func extractFileName(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return path
}

// AnalyzeRuleSimilarity 分析两个规则文件的相似度
// 返回值：相似度是否超过阈值, 错误信息
// threshold: 相似度阈值 (0.0 - 1.0)
func AnalyzeRuleSimilarity(file1Path, file2Path string, threshold float64) (bool, error) {
	// 读取两个文件的规则内容
	rules1, err := loadRulePayloads(file1Path)
	if err != nil {
		return false, fmt.Errorf("读取文件1失败: %w", err)
	}

	rules2, err := loadRulePayloads(file2Path)
	if err != nil {
		return false, fmt.Errorf("读取文件2失败: %w", err)
	}

	// 如果任一文件为空，视为不相似
	if len(rules1) == 0 || len(rules2) == 0 {
		return false, nil
	}

	// 计算 Jaccard 相似度
	// Jaccard = |交集| / |并集|
	similarity := calculateJaccardSimilarity(rules1, rules2)

	return similarity >= threshold, nil
}

// loadRulePayloads 加载规则文件的有效载荷（去除规则类型前缀）
// 例如：DOMAIN-SUFFIX,google.com -> google.com
func loadRulePayloads(filePath string) (map[string]bool, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	payloads := make(map[string]bool)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") || strings.HasPrefix(line, ";") {
			continue
		}

		// 提取规则的 payload 部分
		// 格式：RULE-TYPE,payload[,options]
		parts := strings.Split(line, ",")
		if len(parts) >= 2 {
			// 使用第二部分作为 payload（去除前后空格）
			payload := strings.TrimSpace(parts[1])
			// 规范化域名（转小写）
			payload = strings.ToLower(payload)
			payloads[payload] = true
		} else {
			// 如果不是标准格式，使用整行（可能是 domain.list 格式）
			// domain.list 格式：example.com 或 .example.com
			payload := strings.ToLower(line)
			payloads[payload] = true
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return payloads, nil
}

// calculateJaccardSimilarity 计算 Jaccard 相似度
func calculateJaccardSimilarity(set1, set2 map[string]bool) float64 {
	if len(set1) == 0 && len(set2) == 0 {
		return 1.0 // 两个空集合视为完全相同
	}

	// 计算交集大小
	intersectionCount := 0
	for item := range set1 {
		if set2[item] {
			intersectionCount++
		}
	}

	// 计算并集大小
	unionCount := len(set1) + len(set2) - intersectionCount

	if unionCount == 0 {
		return 0.0
	}

	// Jaccard 相似度 = |交集| / |并集|
	return float64(intersectionCount) / float64(unionCount)
}

// GetRuleSimilarityReport 获取详细的相似度分析报告
func GetRuleSimilarityReport(file1Path, file2Path string) (string, error) {
	rules1, err := loadRulePayloads(file1Path)
	if err != nil {
		return "", fmt.Errorf("读取文件1失败: %w", err)
	}

	rules2, err := loadRulePayloads(file2Path)
	if err != nil {
		return "", fmt.Errorf("读取文件2失败: %w", err)
	}

	// 计算统计信息
	intersectionCount := 0
	var commonRules []string
	for item := range rules1 {
		if rules2[item] {
			intersectionCount++
			if len(commonRules) < 10 {
				commonRules = append(commonRules, item)
			}
		}
	}

	unionCount := len(rules1) + len(rules2) - intersectionCount
	similarity := float64(intersectionCount) / float64(unionCount)

	// 生成报告
	var report strings.Builder
	report.WriteString(fmt.Sprintf("文件1: %s (%d 条规则)\n", extractFileName(file1Path), len(rules1)))
	report.WriteString(fmt.Sprintf("文件2: %s (%d 条规则)\n", extractFileName(file2Path), len(rules2)))
	report.WriteString(fmt.Sprintf("相似度: %.2f%% (Jaccard)\n", similarity*100))
	report.WriteString(fmt.Sprintf("交集: %d 条\n", intersectionCount))
	report.WriteString(fmt.Sprintf("并集: %d 条\n", unionCount))
	report.WriteString(fmt.Sprintf("文件1独有: %d 条\n", len(rules1)-intersectionCount))
	report.WriteString(fmt.Sprintf("文件2独有: %d 条\n", len(rules2)-intersectionCount))

	if len(commonRules) > 0 {
		report.WriteString("\n共同规则示例（前10条）:\n")
		for i, rule := range commonRules {
			report.WriteString(fmt.Sprintf("  %d. %s\n", i+1, rule))
		}
	}

	return report.String(), nil
}
