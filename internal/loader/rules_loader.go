package loader

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rs/zerolog/log"

	"rulerefinery/internal/config"
	"rulerefinery/internal/proxy"
)

// RulesLoader 规则加载器
type RulesLoader struct {
	config          *config.RuleSetsConfig
	loader          *Loader
	proxyPool       *proxy.Pool
	savePath        string          // 规则保存路径
	excludedSources map[string]bool // 已排除的来源（URL 或路径）
	mu              sync.RWMutex    // 保护 excludedSources
}

// NewRulesLoader 创建规则加载器
func NewRulesLoader(ruleSetsConfig *config.RuleSetsConfig, proxyPool *proxy.Pool, savePath string) *RulesLoader {
	// 创建基础加载器（用于下载文件）
	loader := NewLoader(proxyPool, 10) // 默认 10 个并发下载

	return &RulesLoader{
		config:          ruleSetsConfig,
		loader:          loader,
		proxyPool:       proxyPool,
		savePath:        savePath,
		excludedSources: make(map[string]bool),
	}
}

// LoadAllRules 加载所有规则
// 返回：规则集名称 -> 规则文件路径列表
func (rl *RulesLoader) LoadAllRules(ctx context.Context) (map[string][]string, error) {
	result := make(map[string][]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(rl.config.ClassifiedRules))

	log.Info().Msgf("开始加载 %d 个规则集...", len(rl.config.ClassifiedRules))

	// 并发加载每个规则集
	for name, rulesetConfig := range rl.config.ClassifiedRules {
		wg.Add(1)
		go func(rulesetName string, ruleset config.RulesetConfig) {
			defer wg.Done()

			files, err := rl.loadRuleset(ctx, rulesetName, ruleset)
			if err != nil {
				errChan <- fmt.Errorf("加载规则集 '%s' 失败: %w", rulesetName, err)
				return
			}

			if len(files) > 0 {
				mu.Lock()
				result[rulesetName] = files
				mu.Unlock()
				log.Info().Msgf("规则集 '%s': 成功加载 %d 个文件", rulesetName, len(files))
			}
		}(name, rulesetConfig)
	}

	// 等待所有加载完成
	wg.Wait()
	close(errChan)

	// 收集错误
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		log.Warn().Msgf("%d 个规则集加载失败", len(errors))
		for _, err := range errors {
			log.Info().Msgf("  - %v", err)
		}
	}

	log.Info().Msgf("规则加载完成: 成功 %d 个规则集", len(result))
	return result, nil
}

// loadRuleset 加载单个规则集
func (rl *RulesLoader) loadRuleset(ctx context.Context, name string, ruleset config.RulesetConfig) ([]string, error) {
	var files []string

	totalSources := len(ruleset.URLs) + len(ruleset.Files) + len(ruleset.Rules)
	log.Info().Msgf("加载规则集 '%s' (%s)，来源数: %d (URLs: %d, Files: %d, Rules: %d)",
		name, ruleset.Description, totalSources, len(ruleset.URLs), len(ruleset.Files), len(ruleset.Rules))

	// 先加载该规则集的排除规则
	if len(ruleset.ExcludeSources) > 0 {
		log.Info().Msgf("  处理 %d 个排除规则...", len(ruleset.ExcludeSources))
		for _, exclude := range ruleset.ExcludeSources {
			rl.markSourceAsExcluded(exclude)
			log.Info().Msgf("    排除: %s", exclude)
		}
	}

	// 处理 URL 来源
	for i, url := range ruleset.URLs {
		// 检查是否在排除列表中
		if rl.isSourceExcluded(url) {
			log.Info().Msgf("  URL %d 已排除（已在其他规则集中分类）: %s", i+1, url)
			continue
		}

		filePath, err := rl.loadURLSource(ctx, name, url, i)
		if err != nil {
			log.Warn().Msgf("  URL 来源 %d 加载失败: %v", i+1, err)
			continue
		}

		if filePath != "" {
			files = append(files, filePath)
			// 标记此 URL 已被加载，加入排除列表
			rl.markSourceAsExcluded(url)
			log.Info().Msgf("  URL %d: %s", i+1, filepath.Base(filePath))
		}
	}

	// 处理本地文件来源
	for i, file := range ruleset.Files {
		// 检查是否在排除列表中
		if rl.isSourceExcluded(file) {
			log.Info().Msgf("  本地文件 %d 已排除（已在其他规则集中分类）: %s", i+1, file)
			continue
		}

		filePath, err := rl.loadLocalSource(name, file)
		if err != nil {
			log.Info().Msgf("  警告: 本地文件 %d 加载失败: %v", i+1, err)
			continue
		}

		if filePath != "" {
			files = append(files, filePath)
			// 标记此文件已被加载，加入排除列表
			rl.markSourceAsExcluded(file)
			log.Info().Msgf("  本地文件 %d: %s", i+1, filepath.Base(filePath))
		}
	}

	// 处理手工添加的规则
	if len(ruleset.Rules) > 0 {
		filePath, err := rl.loadManualRules(name, ruleset.Rules)
		if err != nil {
			log.Warn().Msgf("手工规则加载失败: %v", err)
		} else if filePath != "" {
			files = append(files, filePath)
			log.Info().Msgf("  手工规则: %d 条", len(ruleset.Rules))
		}
	}

	return files, nil
}

// loadURLSource 加载 URL 来源
func (rl *RulesLoader) loadURLSource(ctx context.Context, rulesetName string, urlStr string, index int) (string, error) {
	// 解析 URL
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("解析 URL 失败: %w", err)
	}

	// 从 URL 提取文件名
	fileName := filepath.Base(parsedURL.Path)
	if fileName == "" || fileName == "/" || fileName == "." {
		// 无法提取文件名，使用随机名称
		fileName = generateRandomFileName() + ".list"
	}

	// 从 URL 提取仓库信息（如果是 GitHub）
	// 例如: https://raw.githubusercontent.com/owner/repo/branch/path/file.list
	// 提取: owner/repo
	var repoPath string
	if strings.Contains(parsedURL.Host, "github") {
		pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
		if len(pathParts) >= 2 {
			// 提取 owner/repo
			repoPath = filepath.Join(pathParts[0], pathParts[1])
		}
	}

	// 构建保存路径
	// 格式: savePath/rulesetName/owner/repo/filename
	var rulesetDir string
	if repoPath != "" {
		rulesetDir = filepath.Join(rl.savePath, rulesetName, repoPath)
	} else {
		rulesetDir = filepath.Join(rl.savePath, rulesetName)
	}

	if err := os.MkdirAll(rulesetDir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	savePath := filepath.Join(rulesetDir, fileName)

	// 如果文件已存在，添加索引避免冲突
	if _, err := os.Stat(savePath); err == nil {
		ext := filepath.Ext(fileName)
		base := strings.TrimSuffix(fileName, ext)
		savePath = filepath.Join(rulesetDir, fmt.Sprintf("%s_%d%s", base, index, ext))
	}

	// 检查文件是否已存在
	if _, err := os.Stat(savePath); err == nil {
		// 文件已存在，直接返回
		log.Info().Msgf("  - 使用缓存: %s", filepath.Base(savePath))
		return savePath, nil
	}

	// 下载文件
	log.Info().Msgf("  下载: %s", urlStr)
	content, err := rl.loader.Load(ctx, urlStr)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}

	// 保存文件
	if err := os.WriteFile(savePath, content, 0644); err != nil {
		return "", fmt.Errorf("保存文件失败: %w", err)
	}

	return savePath, nil
}

// loadLocalSource 加载本地来源
func (rl *RulesLoader) loadLocalSource(rulesetName string, filePath string) (string, error) {
	// 检查文件是否存在
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("本地文件不存在: %s", filePath)
		}
		return "", fmt.Errorf("访问本地文件失败: %w", err)
	}

	// 返回绝对路径
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("获取绝对路径失败: %w", err)
	}

	return absPath, nil
}

// loadManualRules 加载手工添加的规则
func (rl *RulesLoader) loadManualRules(rulesetName string, rules []string) (string, error) {
	if len(rules) == 0 {
		return "", nil
	}

	// 创建规则集目录
	rulesetDir := filepath.Join(rl.savePath, rulesetName)
	if err := os.MkdirAll(rulesetDir, 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	// 生成临时文件保存手工规则
	savePath := filepath.Join(rulesetDir, "manual_rules.list")

	// 将规则内容写入文件（每行一条规则）
	content := strings.Join(rules, "\n")
	if err := os.WriteFile(savePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("保存手工规则失败: %w", err)
	}

	return savePath, nil
}

// GetRulesetFiles 获取指定规则集的文件列表
func (rl *RulesLoader) GetRulesetFiles(rulesetName string) ([]string, error) {
	ruleset, err := rl.config.GetRulesetConfig(rulesetName)
	if err != nil {
		return nil, err
	}

	return rl.loadRuleset(context.Background(), rulesetName, *ruleset)
}

// GetStats 获取统计信息
func (rl *RulesLoader) GetStats() map[string]interface{} {
	totalRulesets := len(rl.config.ClassifiedRules)
	totalURLs := 0
	totalFiles := 0
	totalRules := 0

	for _, ruleset := range rl.config.ClassifiedRules {
		totalURLs += len(ruleset.URLs)
		totalFiles += len(ruleset.Files)
		totalRules += len(ruleset.Rules)
	}

	return map[string]interface{}{
		"total_rulesets": totalRulesets,
		"total_urls":     totalURLs,
		"total_files":    totalFiles,
		"total_rules":    totalRules,
	}
}

// generateRandomFileName 生成随机文件名（用于无法从 URL 提取文件名的情况）
func generateRandomFileName() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		// 如果随机生成失败，使用时间戳
		return fmt.Sprintf("file_%d", os.Getpid())
	}
	return "file_" + hex.EncodeToString(bytes)
}

// isSourceExcluded 检查来源是否已被排除
func (rl *RulesLoader) isSourceExcluded(source string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.excludedSources[source]
}

// markSourceAsExcluded 标记来源为已排除
func (rl *RulesLoader) markSourceAsExcluded(source string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.excludedSources[source] = true
}
