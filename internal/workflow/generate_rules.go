package workflow

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"rulerefinery/internal/ai"
	"rulerefinery/internal/config"
	"rulerefinery/internal/github"
	"rulerefinery/internal/proxy"
	"rulerefinery/internal/rules"
)

// HandleAIClassifyRules 处理 AI 生成规则集配置的完整流程
// 功能说明：
//  1. 从 GitHub 下载规则文件
//  2. 使用 AI 分析规则内容并分类（可加载现有 classified_rules_file 做增量生成）
//  3. 生成新分类到 aiGeneratedClassifiedRules（仅包含本次新增的分类）
//  4. 将新分类自动合并到 classifiedRulesFile（去重，保留现有配置）
//
// 参数：
//   - configFile: config.yaml 路径
//   - classifiedRulesFile: 现有规则分类文件路径（AI结果会自动合并到此文件）
//   - aiGeneratedClassifiedRules: AI 生成的新规则分类文件输出路径（仅包含本次新增）
func HandleAIClassifyRules(configFile, classifiedRulesFile, aiGeneratedClassifiedRules string) {
	log.Info().Msgf("=== AI 规则集自动分类模式 ===")
	log.Info().Msgf("规则分类文件: %s", classifiedRulesFile)
	log.Info().Msgf("AI 输出文件: %s", aiGeneratedClassifiedRules)

	// 验证输出路径不为空
	if aiGeneratedClassifiedRules == "" {
		log.Fatal().Msg("错误: AI 输出文件路径为空，请在 config.yaml 中配置 rulesets.ai_generated_classified_rules")
	}

	// 加载配置文件
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		log.Fatal().Msgf("加载配置文件失败: %v", err)
	}

	// 检查 AI 配置
	if !cfg.AI.IsAIEnabled() {
		log.Fatal().Msg("错误: AI 未配置，无法生成规则分类。请在 config.yaml 中配置 AI 相关设置")
	}

	ctx := context.Background()

	// 初始化代理池
	proxyPool, err := proxy.NewPool(cfg.Proxy.URLs, cfg.Proxy.Enabled)
	if err != nil {
		log.Fatal().Msgf("初始化代理池失败: %v", err)
	}
	if proxyPool.IsEnabled() {
		log.Info().Msgf("代理已启用: %s", proxyPool.GetCurrentProxy())
	}

	// === 步骤 1: 加载现有规则集配置 ===
	var existingRuleSets *config.RuleSetsConfig
	existingFiles := make(map[string]bool) // 已有的本地规则文件路径
	existingURLs := make(map[string]bool)  // 已有的 URL（包括 GitHub Raw URL）

	// 使用 classifiedRulesFile 加载现有配置（如果指定且文件存在）
	if classifiedRulesFile != "" {
		if _, err := os.Stat(classifiedRulesFile); err == nil {
			log.Info().Msgf("检测到现有规则集配置: %s", classifiedRulesFile)
			existingRuleSets, err = config.LoadRuleSetsConfig(classifiedRulesFile)
			if err != nil {
				log.Warn().Msgf("加载规则配置失败: %v", err)
				existingRuleSets = nil
			} else if existingRuleSets != nil && len(existingRuleSets.ClassifiedRules) > 0 {
				log.Info().Msgf("已加载现有配置: %d 个规则集", len(existingRuleSets.ClassifiedRules))

				// 提取所有已有的本地文件和 URLs
				for _, ruleset := range existingRuleSets.ClassifiedRules {
					// 本地文件路径
					for _, file := range ruleset.Files {
						existingFiles[file] = true
					}
					// URL（包括 GitHub Raw URL）
					for _, url := range ruleset.URLs {
						existingURLs[url] = true
					}
				}
				log.Info().Msgf("已有规则数量: %d 个 URL，%d 个本地文件", len(existingURLs), len(existingFiles))
			} else {
				log.Info().Msgf("配置文件为空，将从头开始生成")
			}
		} else {
			log.Info().Msgf("规则分类文件不存在: %s，将创建新文件", classifiedRulesFile)
		}
	} else {
		log.Info().Msg("未指定规则分类文件，将从头开始生成")
	}

	// === 步骤 2: 过滤并下载 GitHub 规则 ===
	log.Info().Msg("开始过滤和下载 GitHub 规则集...")

	// 使用配置的下载路径
	downloadPath := cfg.RuleSources.GitHub.DownloadPath
	if err := os.MkdirAll(downloadPath, 0755); err != nil {
		log.Fatal().Msgf("创建下载目录失败: %v", err)
	}

	ghClient, err := github.NewClient(
		cfg.RuleSources.GitHub.Token,
		proxyPool,
		downloadPath,
		cfg.RuleSources.GitHub.OrganizeByRepo,
		cfg.RuleSources.GitHub.DownloadThreads,
		cfg.RuleSources.GitHub.OverwriteRuleFile,
	)
	if err != nil {
		log.Fatal().Msgf("创建 GitHub 客户端失败: %v", err)
	}

	// 转换仓库配置
	repos := make([]github.RepoConfig, len(cfg.RuleSources.GitHub.Repositories))
	for i, repo := range cfg.RuleSources.GitHub.Repositories {
		filters := make([]github.FilterRule, len(repo.Filters))
		for j, filter := range repo.Filters {
			filters[j] = github.FilterRule{
				Pattern: filter.Pattern,
				Type:    filter.Type,
			}
		}

		repos[i] = github.RepoConfig{
			Owner:    repo.Owner,
			Repo:     repo.Repo,
			Branch:   repo.Branch,
			Path:     repo.Path,
			Filters:  filters,
			Excludes: repo.Excludes, // 使用 glob 模式排除文件
		}
	}

	// 获取规则文件
	results, err := ghClient.FetchMultipleRepos(ctx, repos)
	if err != nil {
		log.Fatal().Msgf("获取 GitHub 规则集失败: %v", err)
	}

	// 收集下载的规则文件
	var downloadedRuleFiles []string
	var githubRuleFileMap = make(map[string]*github.RuleFile)
	totalDownloaded := 0
	skippedCount := 0

	for repoKey, ruleFiles := range results {
		if len(ruleFiles) > 0 {
			log.Info().Msgf("仓库 %s: 找到 %d 个规则文件", repoKey, len(ruleFiles))
		}
		for i := range ruleFiles {
			// 检查 URL 是否有效（下载成功的文件才有本地路径）
			if ruleFiles[i].URL == "" {
				log.Warn().Msgf("跳过无效文件（下载失败）: %s/%s", repoKey, ruleFiles[i].Path)
				continue
			}

			// 构建 GitHub Raw URL
			rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
				ruleFiles[i].Owner, ruleFiles[i].Repo, ruleFiles[i].Branch, ruleFiles[i].Path)

			// 检查是否已在现有配置中
			if existingURLs[rawURL] {
				skippedCount++
				continue
			}

			downloadedRuleFiles = append(downloadedRuleFiles, ruleFiles[i].URL)
			githubRuleFileMap[ruleFiles[i].URL] = &ruleFiles[i]
			totalDownloaded++
		}
	}

	if skippedCount > 0 {
		log.Info().Msgf("跳过已分类的规则: %d 个", skippedCount)
	}

	if totalDownloaded == 0 {
		log.Info().Msg("所有规则都已在配置中，无需处理新文件")
		if existingRuleSets != nil {
			log.Info().Msgf("当前配置: %d 个规则集",
				len(existingRuleSets.ClassifiedRules))
		}
		return
	}

	log.Info().Msgf("新规则文件总数: %d", totalDownloaded)

	// === 步骤 3: 初始化日志目录（仅在有新规则时） ===
	// AI 日志保存到 logging.output_dir/ai 目录下
	logDir := filepath.Join(cfg.Logging.OutputDir, "ai")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatal().Msgf("创建日志目录失败: %v", err)
	}
	log.Info().Msgf("AI 提示词将保存到: %s/ai_rule_classification_batch_*.log", logDir)

	// === 步骤 4: 分析下载的规则文件 ===
	log.Info().Msgf("开始分析 %d 个新下载的规则文件...", len(downloadedRuleFiles))

	ruleFileInfos, err := rules.AnalyzeRuleFiles(downloadedRuleFiles, 5)
	if err != nil {
		log.Fatal().Msgf("分析规则文件失败: %v", err)
	}

	log.Info().Msgf("规则文件分析完成: %d 个文件", len(ruleFileInfos))

	// 添加 GitHub URL 信息
	for i := range ruleFileInfos {
		if ghRuleFile, ok := githubRuleFileMap[ruleFileInfos[i].FilePath]; ok {
			// GitHub 规则：构建 GitHub Raw URL
			rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
				ghRuleFile.Owner, ghRuleFile.Repo, ghRuleFile.Branch, ghRuleFile.Path)
			ruleFileInfos[i].GitHubURL = rawURL
		}
	}

	// === 步骤 4: 分批进行 AI 分类 ===
	log.Info().Msg("开始分批进行 AI 分类...")

	// 记录 AI 提示词模板
	log.Info().Msg("========================================")
	log.Info().Msg("AI 提示词模板:")
	log.Info().Msg("========================================")
	log.Info().Msg(cfg.AI.Prompts.RuleClassification)
	log.Info().Msg("========================================")

	// 创建 AI 客户端
	var httpClient *http.Client
	if proxyPool.IsEnabled() {
		httpClient, _ = proxyPool.GetHTTPClient(120)
	}
	if httpClient == nil {
		timeout := cfg.AI.AIRequestTimeout
		if timeout <= 0 {
			timeout = 120 // 默认 120 秒
		}
		httpClient = &http.Client{Timeout: time.Duration(timeout) * time.Second}
	}

	aiClient, err := ai.NewClient(cfg.AI, httpClient)
	if err != nil {
		log.Fatal().Msgf("创建 AI 客户端失败: %v", err)
	}

	// 分批处理
	batchSize := 20 // 每批 20 个文件
	totalBatches := (len(ruleFileInfos) + batchSize - 1) / batchSize
	concurrency := cfg.AI.BatchConcurrency
	if concurrency <= 0 {
		concurrency = 3 // 默认并发数
	}

	log.Info().Msgf("将分 %d 批处理，每批 %d 个文件，并发数 %d", totalBatches, batchSize, concurrency)

	// 定义批次任务结构
	type batchTask struct {
		idx        int
		start      int
		end        int
		batch      []rules.RuleFileInfo
		promptFile string
	}

	type batchResult struct {
		idx       int
		result    *rules.RuleClassificationResult
		err       error
		unmatched []rules.RuleFileInfo
	}

	// 创建任务和结果通道
	tasks := make(chan batchTask, totalBatches)
	batchResults := make(chan batchResult, totalBatches)

	// 启动并发 worker
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range tasks {
				log.Info().Msgf("[Worker %d] 处理批次 %d/%d: 规则文件 %d-%d",
					workerID, task.idx+1, totalBatches, task.start+1, task.end)

				// 为每批创建独立的超时上下文
				classifyCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)

				// AI 分类
				batchRes, err := rules.ClassifyRulesWithAI(
					classifyCtx, task.batch, aiClient, nil,
					cfg.AI.Prompts.RuleClassification, task.promptFile)
				cancel()

				if err != nil {
					log.Info().Msgf("[Worker %d] 批次 %d/%d 分类失败: %v",
						workerID, task.idx+1, totalBatches, err)
					batchResults <- batchResult{
						idx:       task.idx,
						err:       err,
						unmatched: task.batch,
					}
				} else {
					log.Info().Msgf("[Worker %d] 批次 %d/%d 完成: 生成 %d 个分类，%d 个未分类",
						workerID, task.idx+1, totalBatches,
						len(batchRes.Categories), len(batchRes.Unmatched))
					batchResults <- batchResult{
						idx:    task.idx,
						result: batchRes,
					}
				}
			}
		}(i)
	}

	// 发送所有任务
	for batchIdx := 0; batchIdx < totalBatches; batchIdx++ {
		start := batchIdx * batchSize
		end := start + batchSize
		if end > len(ruleFileInfos) {
			end = len(ruleFileInfos)
		}

		batch := ruleFileInfos[start:end]
		promptFile := filepath.Join(logDir, fmt.Sprintf("ai_rule_classification_batch_%d.log", batchIdx+1))

		tasks <- batchTask{
			idx:        batchIdx,
			start:      start,
			end:        end,
			batch:      batch,
			promptFile: promptFile,
		}
	}
	close(tasks)

	// 等待所有 worker 完成
	go func() {
		wg.Wait()
		close(batchResults)
	}()

	// 收集所有结果
	allCategories := make(map[string]*rules.RuleCategory)
	var allUnmatched []rules.RuleFileInfo
	completedBatches := 0

	for result := range batchResults {
		completedBatches++
		if result.err != nil {
			// 失败的批次加入未分类列表
			allUnmatched = append(allUnmatched, result.unmatched...)
		} else {
			// 合并分类结果
			for name, category := range result.result.Categories {
				nameLower := strings.ToLower(name)
				if existing, ok := allCategories[nameLower]; ok {
					// 合并到已有分类
					existing.URLs = append(existing.URLs, category.URLs...)
					existing.Files = append(existing.Files, category.Files...)
					existing.Rules = append(existing.Rules, category.Rules...)
				} else {
					// 新分类
					categoryCopy := category
					allCategories[nameLower] = &categoryCopy
				}
			}
			// 合并未分类
			allUnmatched = append(allUnmatched, result.result.Unmatched...)
		}
		log.Info().Msgf("进度: %d/%d 批次已完成", completedBatches, totalBatches)
	}

	log.Info().Msgf("所有批次处理完成")
	log.Info().Msgf("  - 总分类数: %d", len(allCategories))
	log.Info().Msgf("  - 未分类数: %d", len(allUnmatched))

	// === 步骤 5: 去重并合并结果 ===
	// 对每个分类的 URLs、Files 和 Rules 进行去重
	for _, category := range allCategories {
		// URLs 去重
		urlSet := make(map[string]bool)
		uniqueURLs := make([]string, 0, len(category.URLs))
		for _, url := range category.URLs {
			if !urlSet[url] {
				urlSet[url] = true
				uniqueURLs = append(uniqueURLs, url)
			}
		}
		category.URLs = uniqueURLs

		// Files 去重
		fileSet := make(map[string]bool)
		uniqueFiles := make([]string, 0, len(category.Files))
		for _, file := range category.Files {
			if !fileSet[file] {
				fileSet[file] = true
				uniqueFiles = append(uniqueFiles, file)
			}
		}
		category.Files = uniqueFiles

		// Rules 去重
		ruleSet := make(map[string]bool)
		uniqueRules := make([]string, 0, len(category.Rules))
		for _, rule := range category.Rules {
			if !ruleSet[rule] {
				ruleSet[rule] = true
				uniqueRules = append(uniqueRules, rule)
			}
		}
		category.Rules = uniqueRules
	}

	// 将 map 转换为 RuleClassificationResult
	finalResult := &rules.RuleClassificationResult{
		Categories: make(map[string]rules.RuleCategory),
	}

	for name, category := range allCategories {
		nameLower := strings.ToLower(name)
		finalResult.Categories[nameLower] = *category
	}

	// 去除已分类的规则（从 allUnmatched 中移除已被任何批次分类的规则）
	classifiedURLs := make(map[string]bool)
	classifiedFiles := make(map[string]bool)
	for _, category := range finalResult.Categories {
		for _, url := range category.URLs {
			classifiedURLs[url] = true
		}
		for _, file := range category.Files {
			classifiedFiles[file] = true
		}
	}

	// 过滤真正未分类的规则
	var trulyUnmatched []rules.RuleFileInfo
	for _, file := range allUnmatched {
		// 检查规则是否已分类（在 URLs 或 Files 中任意一个出现即视为已分类）
		isClassified := false

		// 检查 GitHubURL（如果存在）
		if file.GitHubURL != "" && classifiedURLs[file.GitHubURL] {
			isClassified = true
		}

		// 检查 FilePath（如果存在且尚未标记为已分类）
		// 使用标准化路径进行检查，确保 ./ 前缀不影响匹配
		if !isClassified && file.FilePath != "" {
			// 标准化路径（移除 ./ 前缀）
			normalizedPath := file.FilePath
			if strings.HasPrefix(normalizedPath, "./") {
				normalizedPath = normalizedPath[2:]
			}
			if classifiedFiles[normalizedPath] {
				isClassified = true
			} else if classifiedFiles[file.FilePath] {
				// 兼容：也检查原始路径
				isClassified = true
			}
		}

		if !isClassified {
			trulyUnmatched = append(trulyUnmatched, file)
		}
	}

	// 对真正未分类的规则去重（基于 GitHubURL 或 FilePath）
	unmatchedMap := make(map[string]rules.RuleFileInfo)
	for _, file := range trulyUnmatched {
		key := file.GitHubURL
		if key == "" {
			key = file.FilePath
		}
		unmatchedMap[key] = file
	}

	finalResult.Unmatched = make([]rules.RuleFileInfo, 0, len(unmatchedMap))
	for _, file := range unmatchedMap {
		finalResult.Unmatched = append(finalResult.Unmatched, file)
	}

	// 导出到 AI 生成的输出文件
	log.Info().Msgf("导出新规则集分类到: %s", aiGeneratedClassifiedRules)
	if err := rules.ExportToClassifiedRulesYAML(finalResult, aiGeneratedClassifiedRules); err != nil {
		log.Fatal().Msgf("导出规则配置失败: %v", err)
	}

	// === 新增功能：合并到 classified_rules_file ===
	if classifiedRulesFile != "" {
		log.Info().Msgf("开始合并新分类到: %s", classifiedRulesFile)

		// 加载或创建 classified_rules_file
		var targetRuleSets *config.RuleSetsConfig
		if existingRuleSets != nil {
			// 使用已加载的现有配置
			targetRuleSets = existingRuleSets
		} else {
			// 创建新配置
			targetRuleSets = &config.RuleSetsConfig{
				ClassifiedRules: make(map[string]config.RulesetConfig),
			}
		}

		// 合并新分类到目标配置
		mergedCount := 0
		updatedCount := 0
		for name, category := range finalResult.Categories {
			nameLower := strings.ToLower(name)

			if existingConfig, exists := targetRuleSets.ClassifiedRules[nameLower]; exists {
				// 已存在的分类，合并 URLs、Files 和 Rules
				// 使用 map 去重
				urlSet := make(map[string]bool)
				for _, url := range existingConfig.URLs {
					urlSet[url] = true
				}
				for _, url := range category.URLs {
					urlSet[url] = true
				}

				fileSet := make(map[string]bool)
				for _, file := range existingConfig.Files {
					fileSet[file] = true
				}
				for _, file := range category.Files {
					fileSet[file] = true
				}

				ruleSet := make(map[string]bool)
				for _, rule := range existingConfig.Rules {
					ruleSet[rule] = true
				}
				for _, rule := range category.Rules {
					ruleSet[rule] = true
				}

				// 转换为切片
				mergedURLs := make([]string, 0, len(urlSet))
				for url := range urlSet {
					mergedURLs = append(mergedURLs, url)
				}
				mergedFiles := make([]string, 0, len(fileSet))
				for file := range fileSet {
					mergedFiles = append(mergedFiles, file)
				}
				mergedRules := make([]string, 0, len(ruleSet))
				for rule := range ruleSet {
					mergedRules = append(mergedRules, rule)
				}

				// 更新配置（保留原有的 description 和其他字段）
				description := existingConfig.Description
				if description == "" && category.Description != "" {
					description = category.Description
				}

				targetRuleSets.ClassifiedRules[nameLower] = config.RulesetConfig{
					Description:    description,
					URLs:           mergedURLs,
					Files:          mergedFiles,
					Rules:          mergedRules,
					ExcludeSources: existingConfig.ExcludeSources,
					Filters:        existingConfig.Filters,
					Excludes:       existingConfig.Excludes,
				}
				updatedCount++
			} else {
				// 新分类，直接添加
				targetRuleSets.ClassifiedRules[nameLower] = config.RulesetConfig{
					Description: category.Description,
					URLs:        category.URLs,
					Files:       category.Files,
					Rules:       category.Rules,
				}
				mergedCount++
			}
		}

		// 导出合并后的配置到 classified_rules_file
		if err := rules.ExportClassifiedRulesConfig(targetRuleSets, classifiedRulesFile); err != nil {
			log.Error().Msgf("合并配置到 %s 失败: %v", classifiedRulesFile, err)
		} else {
			log.Info().Msgf("配置已合并到: %s", classifiedRulesFile)
			log.Info().Msgf("  - 新增分类: %d 个", mergedCount)
			log.Info().Msgf("  - 更新分类: %d 个", updatedCount)
			log.Info().Msgf("  - 总分类数: %d 个", len(targetRuleSets.ClassifiedRules))
		}
	}

	// 显示统计信息
	totalCategories := len(finalResult.Categories)
	totalRules := 0
	for _, category := range finalResult.Categories {
		totalRules += len(category.URLs) + len(category.Files) + len(category.Rules)
	}

	log.Info().Msg("规则集分类完成!")
	log.Info().Msgf("  - 规则集文件: %s", aiGeneratedClassifiedRules)
	log.Info().Msgf("  - 新增分类: %d 个", totalCategories)
	log.Info().Msgf("  - 分类规则: %d 个", totalRules)
	log.Info().Msgf("  - 未分类: %d 个", len(finalResult.Unmatched))
	log.Info().Msgf("  - AI提示词文件: %s/ai_rule_classification_batch_*.log", logDir)

	// 导出未分类列表
	if len(finalResult.Unmatched) > 0 {
		unmatchedPath := strings.TrimSuffix(aiGeneratedClassifiedRules, filepath.Ext(aiGeneratedClassifiedRules)) + "_unmatched.txt"
		f, err := os.Create(unmatchedPath)
		if err == nil {
			for _, rule := range finalResult.Unmatched {
				fmt.Fprintf(f, "%s\n", rule.FileName)
			}
			f.Close()
			log.Info().Msgf("  - 未分类列表: %s", unmatchedPath)
		}
	}

	// 提示用户下一步操作
	log.Info().Msgf("\n下一步操作:")
	if existingRuleSets != nil {
		log.Info().Msgf("1. 检查 %s 中的新增分类", aiGeneratedClassifiedRules)
		log.Info().Msgf("2. 配置已自动更新到输出文件")
		log.Info().Msgf("3. 再次运行命令继续处理剩余规则（如有）")
	} else {
		log.Info().Msgf("1. 检查 %s 中的分类结果", aiGeneratedClassifiedRules)
		log.Info().Msgf("2. 配置已保存，可直接使用")
		log.Info().Msgf("3. 再次运行命令继续处理剩余规则（如有）")
	}
}
