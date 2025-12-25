package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"rulerefinery/internal/loader"
	"rulerefinery/internal/proxy"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/google/go-github/v58/github"
	"golang.org/x/oauth2"
)

// GlobFilter Glob 过滤器（支持 ** 递归匹配）
type GlobFilter struct {
	pattern string
}

// NewGlobFilter 创建 Glob 过滤器
func NewGlobFilter(pattern string) *GlobFilter {
	return &GlobFilter{pattern: pattern}
}

// Match 匹配路径（支持 ** 递归匹配）
func (f *GlobFilter) Match(path string) bool {
	// 使用 doublestar 库进行匹配，支持 ** 递归匹配
	matched, err := doublestar.Match(f.pattern, path)
	if err != nil {
		log.Warn().Msgf("glob 模式匹配失败: %v (pattern: %s, path: %s)", err, f.pattern, path)
		return false
	}
	return matched
}

// Client GitHub 客户端
type Client struct {
	client          *github.Client
	loader          *loader.Loader
	proxyPool       *proxy.Pool
	downloadPath    string
	organizeByRepo  bool
	downloadThreads int  // 并发下载线程数
	maxRetries      int  // 最大重试次数
	retryDelay      int  // 重试延迟（秒）
	overwriteFiles  bool // 是否覆盖已有文件
}

// FileInfo 文件信息
type FileInfo struct {
	Path        string
	URL         string
	DownloadURL string
	Size        int64
	Owner       string
	Repo        string
	Branch      string
}

// NewClient 创建 GitHub 客户端
func NewClient(token string, proxyPool *proxy.Pool, downloadPath string, organizeByRepo bool, downloadThreads int, overwriteFiles bool) (*Client, error) {
	var httpClient *http.Client
	var err error

	// 先获取代理客户端
	if proxyPool.IsEnabled() {
		httpClient, err = proxyPool.GetHTTPClient(30) // GitHub API 请求使用 30 秒超时
		if err != nil {
			return nil, fmt.Errorf("获取代理客户端失败: %w", err)
		}
	} else {
		httpClient = &http.Client{}
	}

	// 如果有 token，包装 OAuth2 Transport
	if token != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: token},
		)
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
		httpClient = oauth2.NewClient(ctx, ts)
	}

	if downloadThreads <= 0 {
		downloadThreads = 10
	}

	return &Client{
		client:          github.NewClient(httpClient),
		loader:          loader.NewLoader(proxyPool, downloadThreads),
		proxyPool:       proxyPool,
		downloadPath:    downloadPath,
		organizeByRepo:  organizeByRepo,
		downloadThreads: downloadThreads,
		maxRetries:      3, // 默认重试 3 次
		retryDelay:      2, // 默认延迟 2 秒
		overwriteFiles:  overwriteFiles,
	}, nil
}

// FetchRuleFiles 获取规则文件
func (c *Client) FetchRuleFiles(ctx context.Context, owner, repo, branch, path string, filterRules []FilterRule, excludes []string) ([]RuleFile, error) {
	if excludes == nil {
		excludes = []string{}
	}
	return c.fetchRuleFilesWithRepo(ctx, owner, repo, branch, path, filterRules, excludes)
}

// fetchRuleFilesWithRepo 获取规则文件（内部使用，携带仓库信息）
func (c *Client) fetchRuleFilesWithRepo(ctx context.Context, owner, repo, branch, path string, filterRules []FilterRule, excludes []string) ([]RuleFile, error) {
	// 获取目录树
	tree, _, err := c.client.Git.GetTree(ctx, owner, repo, branch, true)
	if err != nil {
		return nil, fmt.Errorf("获取目录树失败: %w", err)
	}

	// 为每个 filter 规则创建过滤器和元数据
	type filterWithMeta struct {
		filter   *GlobFilter
		ruleType string
	}

	var filtersWithMeta []filterWithMeta
	for _, rule := range filterRules {
		if rule.Pattern != "" {
			filtersWithMeta = append(filtersWithMeta, filterWithMeta{
				filter:   NewGlobFilter(rule.Pattern),
				ruleType: rule.Type,
			})
		}
	}

	// 如果没有过滤器，匹配所有文件
	matchAll := len(filtersWithMeta) == 0

	// 过滤文件
	var ruleFiles []RuleFile
	excludedCount := 0

	for _, entry := range tree.Entries {
		if entry.Type == nil || *entry.Type != "blob" {
			continue
		}

		if entry.Path == nil {
			continue
		}

		// 检查是否在指定路径下
		if path != "" && !strings.HasPrefix(*entry.Path, path) {
			continue
		}

		// 应用 glob 过滤（任意一个过滤器匹配即可，并获取对应的 type）
		var matchedType string
		matched := matchAll

		if !matchAll {
			for _, fm := range filtersWithMeta {
				if fm.filter.Match(*entry.Path) {
					matched = true
					matchedType = fm.ruleType
					break
				}
			}
		}

		if !matched {
			continue
		}

		// 检查是否匹配排除模式
		excluded := false
		if len(excludes) > 0 {
			for _, pattern := range excludes {
				if pattern == "" {
					continue
				}
				// 使用 doublestar 库支持 ** 递归匹配完整路径
				matched, err := doublestar.Match(pattern, *entry.Path)
				if err != nil {
					log.Warn().Msgf("排除模式匹配失败: %v (pattern: %s, path: %s)", err, pattern, *entry.Path)
					continue
				}
				if matched {
					excluded = true
					excludedCount++
					log.Debug().Msgf("排除文件: %s (匹配模式: %s)", *entry.Path, pattern)
					break
				}
			}
		}

		if excluded {
			continue
		}

		// 保存仓库信息以便使用 DownloadContents API
		ruleFile := RuleFile{
			Owner:  owner,
			Repo:   repo,
			Branch: branch,
			Path:   *entry.Path,
			Type:   matchedType,
		}

		ruleFiles = append(ruleFiles, ruleFile)
	}

	if excludedCount > 0 {
		log.Info().Msgf("  已跳过 %d 个规则文件（匹配排除规则）", excludedCount)
	}

	return ruleFiles, nil
}

// ProcessRuleFiles 处理规则文件（下载到本地）
func (c *Client) ProcessRuleFiles(ctx context.Context, ruleFiles []RuleFile) ([]RuleFile, error) {
	// 并发下载文件到本地
	totalFiles := len(ruleFiles)
	if totalFiles == 0 {
		log.Info().Msg("没有找到规则文件")
		return ruleFiles, nil
	}
	log.Info().Msgf("开始下载 %d 个规则文件，并发数：%d", totalFiles, c.downloadThreads)

	type downloadTask struct {
		index int
		rf    RuleFile
	}

	type downloadResult struct {
		index int
		rf    RuleFile
		err   error
	}

	// 创建任务队列和结果队列
	tasks := make(chan downloadTask, len(ruleFiles))
	results := make(chan downloadResult, len(ruleFiles))

	// 进度跟踪
	downloading := make(map[int]string) // 正在下载的文件
	var downloadingMutex sync.Mutex
	completed := 0
	var completedMutex sync.Mutex
	failedCount := 0
	var failedMutex sync.Mutex

	// 使用 WaitGroup 等待所有 workers 完成
	var wg sync.WaitGroup

	// 启动并发下载 worker
	for i := 0; i < c.downloadThreads; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for task := range tasks {
				fileName := filepath.Base(task.rf.Path)

				// 记录正在下载
				downloadingMutex.Lock()
				downloading[workerID] = fileName
				downloadingMutex.Unlock()

				// 构建本地文件路径
				filePath := c.buildLocalFilePathFromInfo(task.rf.Owner, task.rf.Repo, task.rf.Branch, task.rf.Path)

				// 检查文件是否已存在（断点续传/跳过已有文件）
				if _, err := os.Stat(filePath); err == nil {
					// 文件已存在
					if !c.overwriteFiles {
						// 不覆盖模式：跳过下载，直接使用已有文件
						task.rf.URL = filePath
						results <- downloadResult{
							index: task.index,
							rf:    task.rf,
						}

						downloadingMutex.Lock()
						delete(downloading, workerID)
						downloadingMutex.Unlock()
						continue
					}
					// 覆盖模式：继续下载，会覆盖已有文件
				}

				// 带重试的下载
				var content []byte
				var err error
				for retry := 0; retry <= c.maxRetries; retry++ {
					if retry > 0 {
						log.Info().Msgf("重试 [%d/%d]: %s", retry, c.maxRetries, fileName)
						// 使用 time.Sleep 实现延迟
						time.Sleep(time.Duration(c.retryDelay) * time.Second)

						// 检查是否被取消
						select {
						case <-ctx.Done():
							err = ctx.Err()
							break
						default:
						}
					}

					// 使用 GitHub API DownloadContents 下载文件（没有大小限制）
					reader, _, err := c.client.Repositories.DownloadContents(
						ctx,
						task.rf.Owner,
						task.rf.Repo,
						task.rf.Path,
						&github.RepositoryContentGetOptions{Ref: task.rf.Branch},
					)

					if err != nil {
						if retry == c.maxRetries {
							break
						}
						continue
					}

					// 读取文件内容
					if reader == nil {
						err = fmt.Errorf("文件内容为空")
						if retry == c.maxRetries {
							break
						}
						continue
					}

					// 使用 io.ReadAll 读取全部内容
					content, err = io.ReadAll(reader)
					reader.Close()

					if err != nil {
						if retry == c.maxRetries {
							break
						}
						continue
					}

					// 下载成功
					break
				}

				if err != nil {
					failedMutex.Lock()
					failedCount++
					failedMutex.Unlock()

					results <- downloadResult{
						index: task.index,
						err: fmt.Errorf("下载文件失败 %s/%s/%s/%s: %w",
							task.rf.Owner, task.rf.Repo, task.rf.Branch, task.rf.Path, err),
					}

					downloadingMutex.Lock()
					delete(downloading, workerID)
					downloadingMutex.Unlock()
					continue
				}

				// 保存文件
				if err := c.saveFile(filePath, []byte(content)); err != nil {
					failedMutex.Lock()
					failedCount++
					failedMutex.Unlock()

					results <- downloadResult{
						index: task.index,
						err:   fmt.Errorf("保存文件失败 %s: %w", filePath, err),
					}

					downloadingMutex.Lock()
					delete(downloading, workerID)
					downloadingMutex.Unlock()
					continue
				}

				// 更新 URL 为本地文件路径
				task.rf.URL = filePath
				results <- downloadResult{
					index: task.index,
					rf:    task.rf,
				}

				// 清除下载记录并更新进度
				downloadingMutex.Lock()
				delete(downloading, workerID)
				downloadingMutex.Unlock()
			}
		}(i)
	}

	// 发送下载任务
	for i, rf := range ruleFiles {
		tasks <- downloadTask{index: i, rf: rf}
	}
	close(tasks)

	// 启动 goroutine 等待所有 workers 完成后关闭 results
	go func() {
		wg.Wait()
		close(results)
	}()

	// 收集结果并显示进度
	downloadedFiles := make([]RuleFile, 0, len(ruleFiles)) // 只保存成功的文件
	var firstError error

	for result := range results {
		completedMutex.Lock()
		completed++
		currentCompleted := completed
		completedMutex.Unlock()

		if result.err != nil {
			if firstError == nil {
				firstError = result.err
			}
			log.Error().Msgf("[%d/%d] 下载失败: %v", currentCompleted, totalFiles, result.err)
			// 不添加失败的文件到结果中
		} else {
			// 只添加成功下载的文件
			downloadedFiles = append(downloadedFiles, result.rf)

			// 获取当前正在下载的文件
			downloadingMutex.Lock()
			var downloadingList []string
			for _, filename := range downloading {
				downloadingList = append(downloadingList, filename)
			}
			downloadingMutex.Unlock()

			if len(downloadingList) > 0 {
				log.Info().Msgf("[%d/%d] 已完成，正在下载: %s",
					currentCompleted, totalFiles, strings.Join(downloadingList, ", "))
			} else {
				log.Info().Msgf("[%d/%d] 已完成", currentCompleted, totalFiles)
			}
		}
	}

	failedMutex.Lock()
	currentFailedCount := failedCount
	failedMutex.Unlock()

	successCount := len(downloadedFiles)
	if currentFailedCount > 0 {
		log.Info().Msgf("下载完成！成功: %d，失败: %d", successCount, currentFailedCount)
	} else {
		log.Info().Msgf("下载完成！成功下载 %d 个文件", successCount)
	}

	// 如果所有文件都失败，返回错误
	if successCount == 0 {
		return nil, fmt.Errorf("所有文件下载失败，第一个错误: %w", firstError)
	}

	// 返回成功下载的文件列表（部分成功也返回）
	return downloadedFiles, nil
}

// FetchMultipleRepos 并发处理多个仓库
func (c *Client) FetchMultipleRepos(ctx context.Context, repos []RepoConfig) (map[string][]RuleFile, error) {
	type repoResult struct {
		key       string
		ruleFiles []RuleFile
		err       error
	}

	results := make(chan repoResult, len(repos))

	for _, repo := range repos {
		go func(r RepoConfig) {
			// 使用仓库的 filters 列表和排除模式列表
			files, err := c.FetchRuleFiles(ctx, r.Owner, r.Repo, r.Branch, r.Path, r.Filters, r.Excludes)
			if err != nil {
				results <- repoResult{
					key: fmt.Sprintf("%s/%s", r.Owner, r.Repo),
					err: err,
				}
				return
			}

			ruleFiles, err := c.ProcessRuleFiles(ctx, files)
			results <- repoResult{
				key:       fmt.Sprintf("%s/%s", r.Owner, r.Repo),
				ruleFiles: ruleFiles,
				err:       err,
			}
		}(repo)
	}

	// 收集结果
	repoResults := make(map[string][]RuleFile)
	var errorCount int
	var lastError error

	for i := 0; i < len(repos); i++ {
		result := <-results
		if result.err != nil {
			log.Info().Msgf("处理仓库 %s 失败: %v", result.key, result.err)
			errorCount++
			lastError = result.err
			// 继续处理其他仓库，不立即返回错误
		} else if len(result.ruleFiles) > 0 {
			// 只添加有文件的仓库结果
			repoResults[result.key] = result.ruleFiles
		}
	}

	// 如果所有仓库都失败，返回错误
	if errorCount == len(repos) {
		return nil, fmt.Errorf("所有仓库处理失败，最后一个错误: %w", lastError)
	}

	// 部分成功也返回成功的结果
	if errorCount > 0 {
		log.Warn().Msgf("%d/%d 个仓库处理失败，继续使用成功的 %d 个仓库", errorCount, len(repos), len(repoResults))
	}

	return repoResults, nil
}

// RepoConfig 仓库配置
type RepoConfig struct {
	Owner    string
	Repo     string
	Branch   string
	Path     string
	Filters  []FilterRule // 过滤规则列表
	Excludes []string     // 排除模式列表（支持 glob 模式）
}

// FilterRule 过滤规则
type FilterRule struct {
	Pattern string // Glob 模式
	Type    string // 规则类型
}

// RuleFile 规则文件信息
type RuleFile struct {
	Owner  string // 仓库所有者
	Repo   string // 仓库名称
	Branch string // 分支名称
	Path   string // 文件路径
	URL    string // 规则文件 URL 或本地路径（下载后为本地路径）
	Type   string // 规则类型
}

// buildLocalFilePathFromInfo 从仓库信息构建本地文件路径
func (c *Client) buildLocalFilePathFromInfo(owner, repo, branch, path string) string {
	if c.organizeByRepo {
		// 按仓库组织：download_path/owner/repo/branch/path/to/file.list
		return fmt.Sprintf("%s/%s/%s/%s/%s", c.downloadPath, owner, repo, branch, path)
	}

	// 扁平化存储：download_path/repo_file.list
	// 添加仓库名作为前缀，避免不同仓库的同名文件冲突
	fileName := filepath.Base(path)
	fileExt := filepath.Ext(fileName)
	fileBaseName := strings.TrimSuffix(fileName, fileExt)

	// 使用格式：仓库名_文件名.扩展名
	// 例如：ACL4SSR_google.list, blackmatrix7_google.list
	return fmt.Sprintf("%s/%s_%s%s", c.downloadPath, repo, fileBaseName, fileExt)
}

// buildLocalFilePath 构建本地文件路径（保留用于兼容性）
func (c *Client) buildLocalFilePath(url string) (string, error) {
	// 从 URL 提取文件名
	// https://raw.githubusercontent.com/owner/repo/branch/path/to/file.list
	parts := strings.Split(url, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid URL: %s", url)
	}

	fileName := parts[len(parts)-1]

	if c.organizeByRepo {
		// 按仓库组织：download_path/owner/repo/branch/path/to/file.list
		if len(parts) < 7 {
			return "", fmt.Errorf("invalid URL format: %s", url)
		}
		owner := parts[3]
		repo := parts[4]
		branch := parts[5]
		relativePath := strings.Join(parts[6:], "/")
		return fmt.Sprintf("%s/%s/%s/%s/%s", c.downloadPath, owner, repo, branch, relativePath), nil
	}

	// 扁平化存储：download_path/file.list
	return fmt.Sprintf("%s/%s", c.downloadPath, fileName), nil
}

// saveFile 保存文件到本地
func (c *Client) saveFile(filePath string, content []byte) error {
	// 创建目录
	dir := filePath[:strings.LastIndex(filePath, "/")]
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 写入文件
	return os.WriteFile(filePath, content, 0644)
}
