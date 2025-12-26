package rules

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/bmatcuk/doublestar/v4"
)

// RuleType 规则类型（基于 Mihomo）
type RuleType string

const (
	// Domain 类型规则（性能从快到慢）
	RuleTypeDomain         RuleType = "DOMAIN"          // O(1) 精确匹配
	RuleTypeDomainSuffix   RuleType = "DOMAIN-SUFFIX"   // O(n) 后缀匹配
	RuleTypeDomainKeyword  RuleType = "DOMAIN-KEYWORD"  // O(nm) 子串搜索
	RuleTypeDomainWildcard RuleType = "DOMAIN-WILDCARD" // O(nm) 通配符匹配
	RuleTypeDomainRegex    RuleType = "DOMAIN-REGEX"    // O(2^n) 正则表达式

	// IP 类型规则
	RuleTypeIPCIDR      RuleType = "IP-CIDR"
	RuleTypeIPCIDR6     RuleType = "IP-CIDR6"
	RuleTypeSrcIPCIDR   RuleType = "SRC-IP-CIDR"
	RuleTypeSrcIPCIDR6  RuleType = "SRC-IP-CIDR6"
	RuleTypeIPSuffix    RuleType = "IP-SUFFIX"
	RuleTypeSrcIPSuffix RuleType = "SRC-IP-SUFFIX"
	RuleTypeGeoIP       RuleType = "GEOIP"
	RuleTypeSrcGeoIP    RuleType = "SRC-GEOIP"
	RuleTypeIPASN       RuleType = "IP-ASN"
	RuleTypeSrcIPASN    RuleType = "SRC-IP-ASN"

	// 进程相关规则
	RuleTypeProcessName      RuleType = "PROCESS-NAME"
	RuleTypeProcessPath      RuleType = "PROCESS-PATH"
	RuleTypeProcessNameRegex RuleType = "PROCESS-NAME-REGEX"
	RuleTypeProcessPathRegex RuleType = "PROCESS-PATH-REGEX"

	// 端口规则
	RuleTypeDstPort RuleType = "DST-PORT"
	RuleTypeSrcPort RuleType = "SRC-PORT"
	RuleTypeInPort  RuleType = "IN-PORT"

	// 其他规则
	RuleTypeGeoSite  RuleType = "GEOSITE"
	RuleTypeNetwork  RuleType = "NETWORK"
	RuleTypeUid      RuleType = "UID"
	RuleTypeInType   RuleType = "IN-TYPE"
	RuleTypeInUser   RuleType = "IN-USER"
	RuleTypeInName   RuleType = "IN-NAME"
	RuleTypeDSCP     RuleType = "DSCP"
	RuleTypeRuleSet  RuleType = "RULE-SET"
	RuleTypeSubRules RuleType = "SUB-RULE"
	RuleTypeMatch    RuleType = "MATCH"
	RuleTypeFinal    RuleType = "FINAL"
)

// Rule 规则结构
type Rule struct {
	Type    RuleType
	Payload string
	Options string // 可选参数，如 no-resolve
}

// RuleSet 规则集
type RuleSet struct {
	Name     string                // 规则集名称（如 facebook）
	Rules    map[RuleType][]string // 按类型分类的规则
	Filters  []string              // 规则内容过滤器（glob 模式，白名单）
	Excludes []string              // 排除的规则内容（glob 模式，黑名单）
}

// Optimizer 规则优化器
type Optimizer struct {
	ruleSets map[string]*RuleSet
}

// NewOptimizer 创建优化器
func NewOptimizer() *Optimizer {
	return &Optimizer{
		ruleSets: make(map[string]*RuleSet),
	}
}

// ParseRule 解析单条规则
func ParseRule(line string) (*Rule, error) {
	line = strings.TrimSpace(line)

	// 跳过空行
	if line == "" {
		return nil, nil
	}

	// 跳过各种格式的注释
	if strings.HasPrefix(line, "#") ||
		strings.HasPrefix(line, ";") ||
		strings.HasPrefix(line, "//") ||
		strings.HasPrefix(line, "---") {
		return nil, nil
	}

	// 处理 YAML 列表格式（如 "- DOMAIN,example.com"）
	if strings.HasPrefix(line, "-") {
		line = strings.TrimSpace(line[1:]) // 移除前导 "-"
		if line == "" {
			return nil, nil
		}
	}

	// 跳过 YAML 格式的字段（如 payload:, name:, behavior: 等）
	if strings.Contains(line, ":") && !strings.Contains(line, ",") {
		// 可能是 YAML 字段，检查是否是 key: value 格式
		colonIdx := strings.Index(line, ":")
		beforeColon := strings.TrimSpace(line[:colonIdx])
		// 如果冒号前面只有单个单词，且没有逗号，很可能是 YAML 字段
		if !strings.Contains(beforeColon, " ") && !strings.Contains(beforeColon, ",") {
			return nil, nil
		}
	}

	// 跳过只包含特殊字符或 emoji 的标题行
	// 检查行中是否包含逗号（规则必须有逗号分隔）
	if !strings.Contains(line, ",") {
		return nil, nil
	}

	// 跳过以 .list 结尾的文件名行
	if strings.HasSuffix(line, ".list") || strings.HasSuffix(line, ".yaml") ||
		strings.HasSuffix(line, ".txt") || strings.HasSuffix(line, ".conf") {
		return nil, nil
	}

	parts := strings.Split(line, ",")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid rule format: %s", line)
	}

	rule := &Rule{
		Type:    RuleType(strings.ToUpper(strings.TrimSpace(parts[0]))),
		Payload: strings.TrimSpace(parts[1]),
	}

	// 处理可选参数（如 no-resolve）
	if len(parts) > 2 {
		rule.Options = strings.TrimSpace(parts[2])
	}

	return rule, nil
}

// LoadRuleFile 加载规则文件
func (o *Optimizer) LoadRuleFile(filePath string, ruleSetName string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// 确保规则集存在
	if o.ruleSets[ruleSetName] == nil {
		o.ruleSets[ruleSetName] = &RuleSet{
			Name:  ruleSetName,
			Rules: make(map[RuleType][]string),
		}
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		rule, err := ParseRule(scanner.Text())
		if err != nil {
			// 记录错误但继续处理
			log.Warn().Msgf("%v (文件: %s)", err, filePath)
			continue
		}
		if rule == nil {
			continue
		}

		// 添加规则到对应类型
		ruleSet := o.ruleSets[ruleSetName]
		payload := rule.Payload
		if rule.Options != "" {
			payload = fmt.Sprintf("%s,%s", rule.Payload, rule.Options)
		}
		ruleSet.Rules[rule.Type] = append(ruleSet.Rules[rule.Type], payload)
	}

	return scanner.Err()
}

// SetRulesetFilters 设置规则集的过滤器和排除规则
func (o *Optimizer) SetRulesetFilters(ruleSetName string, filters []string, excludes []string) error {
	ruleSet, exists := o.ruleSets[ruleSetName]
	if !exists {
		return fmt.Errorf("规则集 '%s' 不存在", ruleSetName)
	}

	ruleSet.Filters = filters
	ruleSet.Excludes = excludes

	if len(filters) > 0 {
		log.Info().Msgf("规则集 '%s': 已配置 %d 个过滤器", ruleSetName, len(filters))
	}
	if len(excludes) > 0 {
		log.Info().Msgf("规则集 '%s': 已配置 %d 个排除规则", ruleSetName, len(excludes))
	}

	return nil
}

// Deduplicate 去重并排序
func (o *Optimizer) Deduplicate() {
	for _, ruleSet := range o.ruleSets {
		for ruleType, rules := range ruleSet.Rules {
			// 使用 map 去重
			uniqueRules := make(map[string]bool)
			for _, rule := range rules {
				uniqueRules[rule] = true
			}

			// 转回切片
			deduped := make([]string, 0, len(uniqueRules))
			for rule := range uniqueRules {
				deduped = append(deduped, rule)
			}

			// 按类型智能排序
			o.sortRulesByType(ruleType, deduped)

			ruleSet.Rules[ruleType] = deduped
		}
	}
}

// sortRulesByType 根据规则类型进行智能排序
func (o *Optimizer) sortRulesByType(ruleType RuleType, rules []string) {
	switch ruleType {
	case RuleTypeDomain:
		// DOMAIN: 按长度排序（短域名优先，通常是主域名，访问频率高）
		sort.Slice(rules, func(i, j int) bool {
			// 先按长度
			if len(rules[i]) != len(rules[j]) {
				return len(rules[i]) < len(rules[j])
			}
			// 长度相同，按字典序
			return rules[i] < rules[j]
		})

	case RuleTypeDomainSuffix:
		// DOMAIN-SUFFIX: 按长度排序（短后缀优先，匹配速度快）
		sort.Slice(rules, func(i, j int) bool {
			lenI := len(rules[i])
			lenJ := len(rules[j])

			// 优先级1: 短域名（2-5字符）最优先（如 .io, .cn, .com）
			shortI := lenI >= 2 && lenI <= 5
			shortJ := lenJ >= 2 && lenJ <= 5
			if shortI != shortJ {
				return shortI
			}

			// 优先级2: 按长度排序
			if lenI != lenJ {
				return lenI < lenJ
			}

			// 优先级3: 字典序
			return rules[i] < rules[j]
		})

	case RuleTypeDomainKeyword, RuleTypeDomainWildcard:
		// DOMAIN-KEYWORD/WILDCARD: 按关键词长度排序（短关键词通常更通用，如 "google"）
		sort.Slice(rules, func(i, j int) bool {
			if len(rules[i]) != len(rules[j]) {
				return len(rules[i]) < len(rules[j])
			}
			return rules[i] < rules[j]
		})

	case RuleTypeDomainRegex, RuleTypeProcessNameRegex, RuleTypeProcessPathRegex:
		// 正则表达式：按复杂度排序（简单的优先）
		// 使用正则长度作为复杂度的近似
		sort.Slice(rules, func(i, j int) bool {
			if len(rules[i]) != len(rules[j]) {
				return len(rules[i]) < len(rules[j])
			}
			return rules[i] < rules[j]
		})

	case RuleTypeIPCIDR, RuleTypeIPCIDR6, RuleTypeSrcIPCIDR, RuleTypeSrcIPCIDR6, RuleTypeIPSuffix, RuleTypeSrcIPSuffix:
		// IP-CIDR: 规范化后按 CIDR 块大小排序（小块优先，更精确）
		// 先规范化所有规则（添加缺失的掩码）
		for i := range rules {
			rules[i] = normalizeCIDR(rules[i])
		}

		sort.Slice(rules, func(i, j int) bool {
			maskI := extractCIDRMask(rules[i])
			maskJ := extractCIDRMask(rules[j])

			// 掩码大的优先（/32 > /24 > /16，更精确）
			if maskI != maskJ {
				return maskI > maskJ
			}

			// 掩码相同，按字典序
			return rules[i] < rules[j]
		})

	case RuleTypeProcessName, RuleTypeProcessPath:
		// 进程名/路径：按长度排序（精确匹配优先）
		sort.Slice(rules, func(i, j int) bool {
			if len(rules[i]) != len(rules[j]) {
				return len(rules[i]) < len(rules[j])
			}
			return rules[i] < rules[j]
		})

	case RuleTypeDstPort, RuleTypeSrcPort, RuleTypeInPort:
		// 端口规则：按端口号排序
		sort.Slice(rules, func(i, j int) bool {
			// 提取端口号（可能是范围如 "80-443"）
			portI := strings.Split(rules[i], "-")[0]
			portJ := strings.Split(rules[j], "-")[0]
			return portI < portJ
		})

	case RuleTypeGeoIP, RuleTypeSrcGeoIP, RuleTypeGeoSite:
		// GEOIP/GEOSITE: 按代码字母序
		sort.Strings(rules)

	case RuleTypeIPASN, RuleTypeSrcIPASN:
		// IP-ASN: 按 ASN 编号排序
		sort.Slice(rules, func(i, j int) bool {
			// 提取 ASN 编号（格式如 "AS12345" 或 "12345"）
			asnI := strings.TrimPrefix(rules[i], "AS")
			asnJ := strings.TrimPrefix(rules[j], "AS")
			return asnI < asnJ
		})

	case RuleTypeNetwork:
		// NETWORK: TCP/UDP 等，按类型排序（TCP 通常更常见）
		sort.Slice(rules, func(i, j int) bool {
			priority := map[string]int{"tcp": 1, "udp": 2, "icmp": 3}
			pi, okI := priority[strings.ToLower(rules[i])]
			pj, okJ := priority[strings.ToLower(rules[j])]
			if okI && okJ && pi != pj {
				return pi < pj
			}
			return rules[i] < rules[j]
		})

	case RuleTypeInType:
		// IN-TYPE: HTTP/HTTPS/SOCKS5 等，按常用程度排序
		sort.Slice(rules, func(i, j int) bool {
			priority := map[string]int{"http": 1, "https": 2, "socks5": 3}
			pi, okI := priority[strings.ToLower(rules[i])]
			pj, okJ := priority[strings.ToLower(rules[j])]
			if okI && okJ && pi != pj {
				return pi < pj
			}
			return rules[i] < rules[j]
		})

	case RuleTypeUid, RuleTypeDSCP, RuleTypeInUser, RuleTypeInName:
		// UID/DSCP/用户名/接口名：按字符串排序
		sort.Strings(rules)

	default:
		// 其他类型：字典序排序
		sort.Strings(rules)
	}
}

// extractCIDRMask 提取 CIDR 掩码长度
func extractCIDRMask(cidr string) int {
	// 处理可能的参数（如 "192.168.0.0/16,no-resolve"）
	parts := strings.Split(cidr, ",")
	cidrPart := parts[0]

	// 提取掩码
	if idx := strings.Index(cidrPart, "/"); idx != -1 {
		mask := cidrPart[idx+1:]
		var maskLen int
		fmt.Sscanf(mask, "%d", &maskLen)
		return maskLen
	}

	// 没有掩码，IPv4 默认 /32，IPv6 默认 /128
	if strings.Contains(cidrPart, ":") {
		return 128
	}
	return 32
}

// normalizeCIDR 规范化 CIDR 格式，为没有掩码的 IP 地址添加默认掩码
// 保留原有的参数（如 no-resolve）
func normalizeCIDR(rule string) string {
	// 分离 CIDR 和其他参数（如 "192.168.0.1,no-resolve"）
	parts := strings.Split(rule, ",")
	cidrPart := parts[0]

	// 检查是否已经有掩码
	if strings.Contains(cidrPart, "/") {
		return rule // 已有掩码，直接返回
	}

	// 判断是 IPv4 还是 IPv6
	var defaultMask string
	if strings.Contains(cidrPart, ":") {
		defaultMask = "/128" // IPv6 默认掩码
	} else {
		defaultMask = "/32" // IPv4 默认掩码
	}

	// 添加掩码
	parts[0] = cidrPart + defaultMask

	// 重新组合（保留其他参数）
	return strings.Join(parts, ",")
}

// Export 导出规则到文件
// Mihomo 只支持三种 behavior: domain, ipcidr, classical
// 文件命名格式：{ruleset_name}_{type}.{ext}
// 始终输出两种格式：.yaml (YAML格式) 和 .list (纯文本格式)
func (o *Optimizer) Export(outputDir string) error {
	for _, ruleSet := range o.ruleSets {
		ruleSetDir := filepath.Join(outputDir, ruleSet.Name)
		if err := os.MkdirAll(ruleSetDir, 0755); err != nil {
			return err
		}
		// 始终输出两种格式
		if err := o.exportDomain(ruleSet, ruleSetDir); err != nil {
			return err
		}
		if err := o.exportIPCIDR(ruleSet, ruleSetDir); err != nil {
			return err
		}
		// classical (非 domain/ipcidr，无 no-resolve)
		if err := o.exportClassical(ruleSet, ruleSetDir, false, false); err != nil {
			return err
		}
		// classical_no_resolve (非 domain/ipcidr，有 no-resolve)
		if err := o.exportClassical(ruleSet, ruleSetDir, false, true); err != nil {
			return err
		}
		// classical_all (所有规则，无 no-resolve)
		if err := o.exportClassical(ruleSet, ruleSetDir, true, false); err != nil {
			return err
		}
		// classical_all_no_resolve (所有规则，有 no-resolve)
		if err := o.exportClassical(ruleSet, ruleSetDir, true, true); err != nil {
			return err
		}
	}
	return nil
}

// exportDomain 导出 {name}_domain 文件（包含所有 Domain 类型规则）
// Domain behavior 只接受纯域名，支持的格式：
// - example.com (精确匹配完整域名)
// - .example.com (只匹配子域名，不匹配主域名，如匹配 www.example.com 但不匹配 example.com)
// - +.example.com (匹配主域名和所有子域名，如匹配 example.com、www.example.com、a.b.example.com)
func (o *Optimizer) exportDomain(ruleSet *RuleSet, ruleSetDir string) error {
	// 输出 yaml
	yamlPath := filepath.Join(ruleSetDir, fmt.Sprintf("%s_domain.yaml", ruleSet.Name))
	yamlFile, err := os.Create(yamlPath)
	if err != nil {
		return err
	}
	defer yamlFile.Close()

	// 输出 list
	listPath := filepath.Join(ruleSetDir, fmt.Sprintf("%s_domain.list", ruleSet.Name))
	listFile, err := os.Create(listPath)
	if err != nil {
		return err
	}
	defer listFile.Close()

	// 收集所有域名规则
	var domainRules []string

	// DOMAIN: 直接添加
	if rules, exists := ruleSet.Rules[RuleTypeDomain]; exists {
		log.Debug().Msgf("exportDomain - 处理 DOMAIN 规则，规则集='%s', excludes=%v", ruleSet.Name, ruleSet.Excludes)
		filtered := o.applyRuleFilters(rules, RuleTypeDomain, ruleSet.Filters, ruleSet.Excludes)
		domainRules = append(domainRules, filtered...)
	}

	// DOMAIN-SUFFIX: 转换为 +.domain 格式（匹配主域名和所有子域名）
	// 注意：
	//   +.baidu.com 匹配 baidu.com、tieba.baidu.com、123.tieba.baidu.com
	//   .baidu.com  匹配 tieba.baidu.com、123.tieba.baidu.com，但不匹配 baidu.com
	// DOMAIN-SUFFIX 类型必须使用 +. 前缀
	if rules, exists := ruleSet.Rules[RuleTypeDomainSuffix]; exists {
		log.Debug().Msgf("exportDomain - 处理 DOMAIN-SUFFIX 规则，规则集='%s', excludes=%v", ruleSet.Name, ruleSet.Excludes)
		filtered := o.applyRuleFilters(rules, RuleTypeDomainSuffix, ruleSet.Filters, ruleSet.Excludes)
		for _, rule := range filtered {
			// 如果已经有 +. 前缀，保持原样
			if strings.HasPrefix(rule, "+.") {
				domainRules = append(domainRules, rule)
			} else if strings.HasPrefix(rule, ".") {
				// 如果只有 . 前缀，需要改为 +. 前缀
				domainRules = append(domainRules, "+"+rule)
			} else {
				// 没有前缀，添加 +. 前缀
				domainRules = append(domainRules, "+."+rule)
			}
		}
	}

	// DOMAIN-KEYWORD: Domain behavior 不支持 keyword，跳过
	// DOMAIN-WILDCARD: Domain behavior 不支持通配符，跳过
	// DOMAIN-REGEX: Domain behavior 不支持正则，跳过

	totalRules := len(domainRules)

	if totalRules == 0 {
		fmt.Fprintf(yamlFile, "# 无规则内容，自动生成占位\npayload: []\n")
		fmt.Fprintf(listFile, "# 无规则内容，自动生成占位\n")
		log.Info().Msgf("生成空文件: %s, %s (仅注释)", yamlPath, listPath)
		return nil
	}

	// YAML 格式
	fmt.Fprintf(yamlFile, "payload:\n")
	for _, rule := range domainRules {
		fmt.Fprintf(yamlFile, "  - '%s'\n", rule)
	}

	// Text 格式
	for _, rule := range domainRules {
		fmt.Fprintf(listFile, "%s\n", rule)
	}

	log.Info().Msgf("生成文件: %s, %s (%d 条规则)", yamlPath, listPath, totalRules)
	return nil
}

// exportIPCIDR 导出 {name}_ipcidr 文件（包含所有 IP 类型规则，移除 no-resolve 参数）
// IPCIDR behavior 只接受纯 CIDR 格式，如：192.168.0.0/16 或 2001:db8::/32
// 注意：移除 no-resolve 参数，只保留纯 CIDR 地址
// 只支持 IP-CIDR 和 IP-CIDR6
// 其他类型（SRC-IP-CIDR, IP-ASN 等）不被 ipcidr behavior 支持，需要使用 classical
func (o *Optimizer) exportIPCIDR(ruleSet *RuleSet, ruleSetDir string) error {
	// 输出 yaml
	yamlPath := filepath.Join(ruleSetDir, fmt.Sprintf("%s_ipcidr.yaml", ruleSet.Name))
	yamlFile, err := os.Create(yamlPath)
	if err != nil {
		return err
	}
	defer yamlFile.Close()

	// 输出 list
	listPath := filepath.Join(ruleSetDir, fmt.Sprintf("%s_ipcidr.list", ruleSet.Name))
	listFile, err := os.Create(listPath)
	if err != nil {
		return err
	}
	defer listFile.Close()

	// 收集所有 IP CIDR 规则并移除 no-resolve 参数
	var ipcidrRules []string
	ipTypes := []RuleType{
		RuleTypeIPCIDR,
		RuleTypeIPCIDR6,
	}
	for _, ruleType := range ipTypes {
		rules, exists := ruleSet.Rules[ruleType]
		if !exists || len(rules) == 0 {
			continue
		}

		// 先应用过滤器
		filtered := o.applyRuleFilters(rules, ruleType, ruleSet.Filters, ruleSet.Excludes)

		for _, rule := range filtered {
			// 移除 no-resolve 参数
			parts := strings.Split(rule, ",")
			cleanParts := []string{}
			for _, part := range parts {
				if strings.TrimSpace(part) != "no-resolve" {
					cleanParts = append(cleanParts, part)
				}
			}
			ipcidrRules = append(ipcidrRules, strings.Join(cleanParts, ","))
		}
	}
	totalRules := len(ipcidrRules)

	if totalRules == 0 {
		fmt.Fprintf(yamlFile, "# 无规则内容，自动生成占位\npayload: []\n")
		fmt.Fprintf(listFile, "# 无规则内容，自动生成占位\n")
		log.Info().Msgf("生成空文件: %s, %s (仅注释)", yamlPath, listPath)
		return nil
	}
	fmt.Fprintf(yamlFile, "payload:\n")
	for _, rule := range ipcidrRules {
		fmt.Fprintf(yamlFile, "  - '%s'\n", rule)
	}
	for _, rule := range ipcidrRules {
		fmt.Fprintf(listFile, "%s\n", rule)
	}
	log.Info().Msgf("生成文件: %s, %s (%d 条规则)", yamlPath, listPath, totalRules)
	return nil
}

// exportClassical 导出 classical 格式
// includeAll: true 导出所有规则（{name}_classical_all），false 只导出非 domain 和非 ipcidr 的规则（{name}_classical）
// withNoResolve: true IP-CIDR 规则保留/添加 no-resolve 参数，false 移除 no-resolve 参数
// Classical behavior 支持所有规则类型，包括：
// - Domain 类型: DOMAIN, DOMAIN-SUFFIX, DOMAIN-KEYWORD, DOMAIN-WILDCARD, DOMAIN-REGEX
// - IP 类型: IP-CIDR, IP-CIDR6, SRC-IP-CIDR, IP-ASN 等
// - 进程类型: PROCESS-NAME, PROCESS-PATH 等
// - 其他: GEOIP, GEOSITE, DST-PORT, RULE-SET 等
func (o *Optimizer) exportClassical(ruleSet *RuleSet, ruleSetDir string, includeAll bool, withNoResolve bool) error {
	// 输出 yaml
	var yamlPath, listPath string
	if includeAll {
		if withNoResolve {
			yamlPath = filepath.Join(ruleSetDir, fmt.Sprintf("%s_classical_all_no_resolve.yaml", ruleSet.Name))
			listPath = filepath.Join(ruleSetDir, fmt.Sprintf("%s_classical_all_no_resolve.list", ruleSet.Name))
		} else {
			yamlPath = filepath.Join(ruleSetDir, fmt.Sprintf("%s_classical_all.yaml", ruleSet.Name))
			listPath = filepath.Join(ruleSetDir, fmt.Sprintf("%s_classical_all.list", ruleSet.Name))
		}
	} else {
		if withNoResolve {
			yamlPath = filepath.Join(ruleSetDir, fmt.Sprintf("%s_classical_no_resolve.yaml", ruleSet.Name))
			listPath = filepath.Join(ruleSetDir, fmt.Sprintf("%s_classical_no_resolve.list", ruleSet.Name))
		} else {
			yamlPath = filepath.Join(ruleSetDir, fmt.Sprintf("%s_classical.yaml", ruleSet.Name))
			listPath = filepath.Join(ruleSetDir, fmt.Sprintf("%s_classical.list", ruleSet.Name))
		}
	}
	yamlFile, err := os.Create(yamlPath)
	if err != nil {
		return err
	}
	defer yamlFile.Close()
	listFile, err := os.Create(listPath)
	if err != nil {
		return err
	}
	defer listFile.Close()

	// 写入文件头注释
	if includeAll {
		fmt.Fprintf(yamlFile, "# %s - Classical Format (All Rules)\n", ruleSet.Name)
		fmt.Fprintf(yamlFile, "# Includes all rule types\n")
		fmt.Fprintf(listFile, "# %s - Classical Format (All Rules)\n", ruleSet.Name)
		fmt.Fprintf(listFile, "# Includes all rule types\n")
	} else {
		fmt.Fprintf(yamlFile, "# %s - Classical Format (Other Rules)\n", ruleSet.Name)
		fmt.Fprintf(yamlFile, "# Excludes rules that can use domain.list (DOMAIN/DOMAIN-SUFFIX)\n")
		fmt.Fprintf(yamlFile, "# and ipcidr.list (IP-CIDR/IP-CIDR6)\n")
		fmt.Fprintf(listFile, "# %s - Classical Format (Other Rules)\n", ruleSet.Name)
		fmt.Fprintf(listFile, "# Excludes rules that can use domain.list (DOMAIN/DOMAIN-SUFFIX)\n")
		fmt.Fprintf(listFile, "# and ipcidr.list (IP-CIDR/IP-CIDR6)\n")
	}
	if withNoResolve {
		fmt.Fprintf(yamlFile, "# IP-CIDR rules include 'no-resolve' parameter\n")
		fmt.Fprintf(listFile, "# IP-CIDR rules include 'no-resolve' parameter\n")
	} else {
		fmt.Fprintf(yamlFile, "# IP-CIDR rules exclude 'no-resolve' parameter\n")
		fmt.Fprintf(listFile, "# IP-CIDR rules exclude 'no-resolve' parameter\n")
	}
	fmt.Fprintf(yamlFile, "# Rules are optimized and sorted for best performance\n")
	fmt.Fprintf(listFile, "# Rules are optimized and sorted for best performance\n")

	// 输出 payload 头
	fmt.Fprintf(yamlFile, "payload:\n")

	// 定义可以被 domain.list 和 ipcidr.list 处理的规则类型
	domainListTypes := map[RuleType]bool{
		RuleTypeDomain:       true,
		RuleTypeDomainSuffix: true,
	}
	ipcidrListTypes := map[RuleType]bool{
		RuleTypeIPCIDR:  true,
		RuleTypeIPCIDR6: true,
	}
	orderedTypes := []RuleType{
		RuleTypeDomain, RuleTypeDomainSuffix, RuleTypeDomainKeyword, RuleTypeDomainWildcard, RuleTypeDomainRegex,
		RuleTypeIPCIDR, RuleTypeIPCIDR6, RuleTypeSrcIPCIDR, RuleTypeSrcIPCIDR6, RuleTypeIPSuffix, RuleTypeSrcIPSuffix, RuleTypeIPASN, RuleTypeSrcIPASN,
		RuleTypeGeoIP, RuleTypeSrcGeoIP, RuleTypeGeoSite,
		RuleTypeProcessName, RuleTypeProcessPath, RuleTypeProcessNameRegex, RuleTypeProcessPathRegex,
		RuleTypeDstPort, RuleTypeSrcPort, RuleTypeInPort,
		RuleTypeNetwork, RuleTypeUid, RuleTypeInType, RuleTypeInUser, RuleTypeInName, RuleTypeDSCP,
		RuleTypeRuleSet, RuleTypeSubRules,
	}
	totalRules := 0
	for _, ruleType := range orderedTypes {
		rules, exists := ruleSet.Rules[ruleType]
		if !exists || len(rules) == 0 {
			continue
		}
		if !includeAll {
			// 对于 classical 和 classical_no_resolve，处理规则排除逻辑
			// - 始终排除 domain 类型（已单独导出到 domain.list）
			// - 对于不带 no-resolve 的版本，也排除 ipcidr 类型（已单独导出到 ipcidr.list）
			// - 对于带 no-resolve 的版本，包含 ipcidr 类型（因为 ipcidr.list 不带 no-resolve）
			if domainListTypes[ruleType] {
				continue
			}
			if ipcidrListTypes[ruleType] && !withNoResolve {
				continue
			}
		}

		// 先应用过滤器
		filtered := o.applyRuleFilters(rules, ruleType, ruleSet.Filters, ruleSet.Excludes)
		if len(filtered) == 0 {
			continue
		}

		// YAML 输出
		fmt.Fprintf(yamlFile, "\n  # %s (%d rules)\n", ruleType, len(filtered))
		for _, rule := range filtered {
			// 对于 IP-CIDR 和 IP-CIDR6 类型，根据 withNoResolve 参数处理 no-resolve
			processedRule := rule
			if ruleType == RuleTypeIPCIDR || ruleType == RuleTypeIPCIDR6 {
				if withNoResolve {
					// 确保有 no-resolve 参数
					if !strings.Contains(rule, "no-resolve") {
						processedRule = rule + ",no-resolve"
					}
				} else {
					// 移除 no-resolve 参数
					parts := strings.Split(rule, ",")
					cleanParts := []string{}
					for _, part := range parts {
						if strings.TrimSpace(part) != "no-resolve" {
							cleanParts = append(cleanParts, part)
						}
					}
					processedRule = strings.Join(cleanParts, ",")
				}
			}
			fmt.Fprintf(yamlFile, "  - '%s,%s'\n", ruleType, processedRule)
			totalRules++
		}
		// list 输出
		fmt.Fprintf(listFile, "\n# %s (%d rules)\n", ruleType, len(filtered))
		for _, rule := range filtered {
			// 对于 IP-CIDR 和 IP-CIDR6 类型，根据 withNoResolve 参数处理 no-resolve
			processedRule := rule
			if ruleType == RuleTypeIPCIDR || ruleType == RuleTypeIPCIDR6 {
				if withNoResolve {
					// 确保有 no-resolve 参数
					if !strings.Contains(rule, "no-resolve") {
						processedRule = rule + ",no-resolve"
					}
				} else {
					// 移除 no-resolve 参数
					parts := strings.Split(rule, ",")
					cleanParts := []string{}
					for _, part := range parts {
						if strings.TrimSpace(part) != "no-resolve" {
							cleanParts = append(cleanParts, part)
						}
					}
					processedRule = strings.Join(cleanParts, ",")
				}
			}
			fmt.Fprintf(listFile, "%s,%s\n", ruleType, processedRule)
		}
	}
	if totalRules > 0 {
		log.Info().Msgf("生成文件: %s, %s (%d 条规则)", yamlPath, listPath, totalRules)
	}
	if totalRules == 0 {
		fmt.Fprintf(yamlFile, "  # 无规则内容，自动生成占位\n")
		fmt.Fprintf(listFile, "# 无规则内容，自动生成占位\n")
		log.Info().Msgf("生成空文件: %s, %s (仅注释)", yamlPath, listPath)
	}
	return nil
}

// GetStatistics 获取统计信息
func (o *Optimizer) GetStatistics() map[string]map[RuleType]int {
	stats := make(map[string]map[RuleType]int)

	for name, ruleSet := range o.ruleSets {
		stats[name] = make(map[RuleType]int)
		for ruleType, rules := range ruleSet.Rules {
			stats[name][ruleType] = len(rules)
		}
	}

	return stats
}

// applyRuleFilters 应用规则过滤器和排除规则
// filters: 白名单模式，只保留匹配的规则（为空则保留所有）
// excludes: 黑名单模式，排除匹配的规则
// 处理顺序: 先应用 filters，再应用 excludes
func (o *Optimizer) applyRuleFilters(rules []string, ruleType RuleType, filters []string, excludes []string) []string {
	if len(rules) == 0 {
		return rules
	}

	originalCount := len(rules)

	// 打印调试信息
	log.Debug().Msgf("规则过滤 ruleType=%s, filters=%v, excludes=%v, 输入规则数=%d",
		ruleType, filters, excludes, len(rules))

	// 打印前3条规则示例
	if len(rules) > 0 {
		sampleCount := 3
		if len(rules) < sampleCount {
			sampleCount = len(rules)
		}
		for i := 0; i < sampleCount; i++ {
			fullRule := fmt.Sprintf("%s,%s", ruleType, rules[i])
			log.Debug().Msgf("  规则示例[%d]: %s", i, fullRule)
		}
	}

	result := rules // 初始赋值

	// 第一步: 应用 filters (白名单)
	if len(filters) > 0 {
		filtered := make([]string, 0, len(result))
		matchedCount := 0
		for _, rule := range result {
			// 构造完整规则用于匹配 (格式: RULE-TYPE,payload)
			fullRule := fmt.Sprintf("%s,%s", ruleType, rule)

			matched := false
			for _, filter := range filters {
				if filter == "" {
					continue
				}
				// 使用 glob 模式匹配
				if m, err := doublestar.Match(filter, fullRule); err == nil && m {
					matched = true
					matchedCount++
					// 打印前几条匹配的规则
					if matchedCount <= 3 {
						log.Debug().Msgf("  匹配成功: filter='%s', fullRule='%s'", filter, fullRule)
					}
					break
				} else if err != nil {
					log.Info().Msgf("过滤器匹配错误: filter='%s', fullRule='%s', err=%v", filter, fullRule, err)
				} else {
					// 打印前几条未匹配的规则
					if len(filtered) == 0 && matchedCount == 0 {
						log.Debug().Msgf("  匹配失败: filter='%s', fullRule='%s'", filter, fullRule)
					}
				}
			}

			if matched {
				filtered = append(filtered, rule)
			}
		}
		log.Info().Msgf("  过滤器匹配统计: 总规则数=%d, 匹配成功=%d", originalCount, matchedCount)
		result = filtered
	}

	// 第二步: 应用 excludes (黑名单)
	if len(excludes) > 0 {
		filtered := make([]string, 0, len(result))
		excludedCount := 0
		for _, rule := range result {
			// 构造完整规则用于匹配
			fullRule := fmt.Sprintf("%s,%s", ruleType, rule)

			excluded := false
			for _, exclude := range excludes {
				if exclude == "" {
					continue
				}
				// 使用 glob 模式匹配
				if m, err := doublestar.Match(exclude, fullRule); err == nil && m {
					excluded = true
					excludedCount++
					// 打印前几条被排除的规则
					if excludedCount <= 3 {
						log.Debug().Msgf("  规则被排除: exclude='%s', fullRule='%s'", exclude, fullRule)
					}
					break
				}
			}

			if !excluded {
				filtered = append(filtered, rule)
			}
		}
		if excludedCount > 0 {
			log.Info().Msgf("  排除规则统计: 总规则数=%d, 被排除=%d, 保留=%d", len(result), excludedCount, len(filtered))
		}
		result = filtered
	}

	filteredCount := len(result)
	if filteredCount != originalCount {
		log.Info().Msgf("规则过滤: %s - 原始 %d 条，过滤后 %d 条", ruleType, originalCount, filteredCount)
	}

	return result
}
