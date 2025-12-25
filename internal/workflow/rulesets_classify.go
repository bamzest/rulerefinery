package workflow

import (
	"context"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"

	"rulerefinery/internal/config"
	"rulerefinery/internal/loader"
	"rulerefinery/internal/proxy"
	"rulerefinery/internal/rules"
)

// HandleGenerateRuleSets 处理规则集分类、下载和优化
func HandleGenerateRuleSets(configFile, ruleSetsConfigPath, outputRulesetsPath string) {
	log.Info().Msgf("=== 规则集分类处理模式 ===")
	log.Info().Msgf("规则集配置文件: %s", ruleSetsConfigPath)
	log.Info().Msgf("输出目录: %s", outputRulesetsPath)

	// 创建临时下载目录
	tmpDownloadPath := "./tmp/rulesets_download"
	if err := os.MkdirAll(tmpDownloadPath, 0755); err != nil {
		log.Fatal().Msgf("创建临时下载目录失败: %v", err)
	}

	// 确保临时目录被清理（即使发生 panic）
	defer func() {
		if err := os.RemoveAll("./tmp"); err != nil {
			log.Warn().Msgf("清理临时目录失败: %v", err)
		} else {
			log.Info().Msg("临时目录已清理")
		}
	}()

	// 加载主配置文件
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		log.Fatal().Msgf("加载配置文件失败: %v", err)
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

	// 加载规则集配置文件
	log.Info().Msgf("加载规则集配置文件: %s", ruleSetsConfigPath)
	ruleSetsConfigData, err := config.LoadRuleSetsConfig(ruleSetsConfigPath)
	if err != nil {
		log.Fatal().Msgf("加载规则配置文件失败: %v", err)
	}

	// 显示规则集配置统计
	totalURLs := 0
	totalFiles := 0
	totalRules := 0
	for _, ruleset := range ruleSetsConfigData.ClassifiedRules {
		totalURLs += len(ruleset.URLs)
		totalFiles += len(ruleset.Files)
		totalRules += len(ruleset.Rules)
	}
	log.Info().Msgf("规则集配置加载成功: %d 个规则集, %d 个 URL 来源, %d 个本地文件, %d 条手工规则",
		len(ruleSetsConfigData.ClassifiedRules), totalURLs, totalFiles, totalRules)

	// 创建规则加载器
	rulesLoader := loader.NewRulesLoader(ruleSetsConfigData, proxyPool, tmpDownloadPath)

	// 加载所有规则
	log.Info().Msg("开始下载和加载规则文件...")
	rulesetFiles, err := rulesLoader.LoadAllRules(ctx)
	if err != nil {
		log.Warn().Msgf("部分规则加载失败: %v", err)
	}

	if len(rulesetFiles) == 0 {
		log.Info().Msg("没有需要处理的规则文件")
		return
	}

	log.Info().Msgf("规则加载完成: 成功加载 %d 个规则集", len(rulesetFiles))

	// 合并和优化规则集（始终自动去重和智能排序）
	log.Info().Msg("开始合并和优化规则集...")
	if err := processRulesets(rulesetFiles, ruleSetsConfigData, outputRulesetsPath); err != nil {
		log.Fatal().Msgf("规则优化失败: %v", err)
	}

	log.Info().Msg("规则集处理完成！")
	log.Info().Msgf("规则集已保存到: %s", outputRulesetsPath)
}

// processRulesets 处理规则集：去重、排序、导出
func processRulesets(rulesetFiles map[string][]string, ruleSetsConfig *config.RuleSetsConfig, outputRulesetsPath string) error {
	// 创建优化器
	optimizer := rules.NewOptimizer()

	// 加载所有规则文件
	totalFiles := 0
	for rulesetName, files := range rulesetFiles {
		for _, filePath := range files {
			if err := optimizer.LoadRuleFile(filePath, rulesetName); err != nil {
				log.Warn().Msgf("加载规则文件失败 %s: %v", filePath, err)
				continue
			}
			totalFiles++
		}
	}

	log.Info().Msgf("已加载 %d 个规则文件到优化器", totalFiles)

	// 设置每个规则集的过滤器配置
	log.Info().Msg("开始配置规则集过滤器...")
	for rulesetName, rulesetConfig := range ruleSetsConfig.ClassifiedRules {
		if len(rulesetConfig.Filters) > 0 || len(rulesetConfig.Excludes) > 0 {
			log.Info().Msgf("配置规则集 '%s': filters=%d, excludes=%d", rulesetName, len(rulesetConfig.Filters), len(rulesetConfig.Excludes))
		}
		if err := optimizer.SetRulesetFilters(rulesetName, rulesetConfig.Filters, rulesetConfig.Excludes); err != nil {
			log.Warn().Msgf("设置规则集 '%s' 过滤器失败: %v", rulesetName, err)
		}
	}

	// 去重
	log.Info().Msg("开始去重规则...")
	optimizer.Deduplicate()
	log.Info().Msg("规则去重完成")

	// 导出优化后的规则
	log.Info().Msgf("开始导出规则集到: %s", outputRulesetsPath)
	if err := optimizer.Export(outputRulesetsPath); err != nil {
		return fmt.Errorf("导出规则集失败: %w", err)
	}

	return nil
}
