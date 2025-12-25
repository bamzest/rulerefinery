package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"rulerefinery/internal/config"
	"rulerefinery/internal/workflow"
)

var (
	configFile = flag.String("config", "config.yaml", "配置文件路径")
	help       = flag.Bool("help", false, "显示帮助信息")
)

var (
	Version = "dev" // 版本号，编译时通过 -ldflags 注入
)

func main() {
	flag.Parse()

	// 显示帮助信息
	if *help {
		printHelp()
		os.Exit(0)
	}

	// 加载配置文件并初始化日志
	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置文件失败: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志系统
	if err := initLogger(cfg.Logging); err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志系统失败: %v\n", err)
		os.Exit(1)
	}

	log.Info().Msgf("程序启动 version=%s config=%s ai_classify=%v generate_rules=%v", Version, *configFile, cfg.AIClassifyRules.Enabled, cfg.GenerateRules.Enabled)

	// 检查是否至少启用了一个功能
	if !cfg.AIClassifyRules.Enabled && !cfg.GenerateRules.Enabled {
		log.Fatal().Msg("错误: 必须至少启用一个功能（ai_classify_rules.enabled 或 generate_rules.enabled）")
	}

	// 执行 AI 规则分类
	if cfg.AIClassifyRules.Enabled {
		log.Info().Msg("开始执行 AI 规则分类...")
		// 验证必填参数
		if cfg.GenerateRules.OutputRulesPath == "" {
			log.Fatal().Msg("错误: 缺少必填参数 generate_rules.output_rules_path，请在 config.yaml 中配置规则集输出目录")
		}
		if cfg.AIClassifyRules.AIGeneratedClassifiedRules == "" {
			log.Fatal().Msg("错误: 缺少必填参数 ai_classify_rules.ai_generated_classified_rules，请在 config.yaml 中配置 AI 生成规则分类文件输出路径")
		}
		// 使用 classified_rules_file 加载现有配置，ai_generated_classified_rules 保存新配置
		workflow.HandleAIClassifyRules(*configFile, cfg.AIClassifyRules.ClassifiedRulesFile, cfg.AIClassifyRules.AIGeneratedClassifiedRules)
		log.Info().Msg("AI 规则分类完成")
	}

	// 执行规则集生成
	if cfg.GenerateRules.Enabled {
		log.Info().Msg("开始执行规则集生成...")
		// 验证必填参数
		if cfg.GenerateRules.OutputRulesPath == "" {
			log.Fatal().Msg("错误: 缺少必填参数 generate_rules.output_rules_path，请在 config.yaml 中配置规则集输出目录")
		}
		if cfg.AIClassifyRules.ClassifiedRulesFile == "" {
			log.Fatal().Msg("错误: 缺少必填参数 ai_classify_rules.classified_rules_file，请在 config.yaml 中配置规则分类文件路径")
		}
		// 执行规则集生成处理
		workflow.HandleGenerateRuleSets(*configFile, cfg.AIClassifyRules.ClassifiedRulesFile, cfg.GenerateRules.OutputRulesPath)
		log.Info().Msg("规则集生成完成")
	}

	log.Info().Msg("所有任务执行完成")
}

// initLogger 初始化日志系统
func initLogger(cfg config.LoggingConfig) error {
	// 解析日志级别
	var level zerolog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = zerolog.DebugLevel
	case "info":
		level = zerolog.InfoLevel
	case "warn", "warning":
		level = zerolog.WarnLevel
	case "error":
		level = zerolog.ErrorLevel
	default:
		level = zerolog.InfoLevel
	}

	// 设置全局日志级别
	zerolog.SetGlobalLevel(level)

	// 确保日志目录存在
	if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}

	// 打开日志文件
	logFilePath := filepath.Join(cfg.OutputDir, cfg.OutputFile)
	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	// 配置输出目标
	var writer io.Writer
	if cfg.Format == "json" {
		// JSON 格式
		if cfg.ConsoleOutput {
			writer = io.MultiWriter(file, os.Stdout)
		} else {
			writer = file
		}
		log.Logger = zerolog.New(writer).With().Timestamp().Logger()
	} else {
		// Console 格式（完整日志格式：时间 级别 消息 字段）
		consoleWriter := zerolog.ConsoleWriter{
			Out:        file,
			TimeFormat: "2006/01/02 15:04:05",
			NoColor:    true,
			FormatLevel: func(i interface{}) string {
				if ll, ok := i.(string); ok {
					return fmt.Sprintf("[%s]", strings.ToUpper(ll))
				}
				return ""
			},
			FormatMessage: func(i interface{}) string {
				return fmt.Sprintf("%s", i)
			},
			FormatFieldName: func(i interface{}) string {
				return fmt.Sprintf("%s=", i)
			},
			FormatFieldValue: func(i interface{}) string {
				return fmt.Sprintf("%s", i)
			},
		}

		if cfg.ConsoleOutput {
			// 控制台使用带颜色的格式
			consoleOut := zerolog.ConsoleWriter{
				Out:        os.Stdout,
				TimeFormat: time.Kitchen,
			}
			writer = io.MultiWriter(
				consoleWriter,
				consoleOut,
			)
			log.Logger = zerolog.New(writer).With().Timestamp().Logger()
		} else {
			log.Logger = zerolog.New(consoleWriter).With().Timestamp().Logger()
		}
	}

	return nil
}

// printHelp 显示帮助信息
func printHelp() {
	fmt.Printf("RuleRefinery v%s\n\n", Version)
	fmt.Println("AI-powered proxy rule aggregation, deduplication, classification, and multi-client export.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Printf("  %s [--config <configuration file>] [--help]\n\n", os.Args[0])

	fmt.Println("Options:")
	fmt.Println("  --config <file>         Path to configuration file (default: config.yaml)")
	fmt.Println("  --help                  Show help information")
	fmt.Println()
}
