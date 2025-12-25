package loader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"rulerefinery/internal/proxy"
)

// Result 加载结果
type Result struct {
	Source  string // URL 或文件路径
	Content []byte
	Error   error
}

// Loader 加载器
type Loader struct {
	proxyPool  *proxy.Pool
	maxWorkers int
}

// isURL 判断字符串是否为 URL
func isURL(s string) bool {
	return len(s) > 7 && (s[:7] == "http://" || (len(s) > 8 && s[:8] == "https://"))
}

// NewLoader 创建加载器
func NewLoader(proxyPool *proxy.Pool, maxWorkers int) *Loader {
	if maxWorkers <= 0 {
		maxWorkers = 10 // 默认并发数
	}
	return &Loader{
		proxyPool:  proxyPool,
		maxWorkers: maxWorkers,
	}
}

// Load 加载单个资源（自动判断 URL 或文件）
func (l *Loader) Load(ctx context.Context, source string) ([]byte, error) {
	if isURL(source) {
		return l.LoadURLWithUA(ctx, source, "")
	}
	return l.loadFile(source)
}

// LoadURLWithUA 加载 URL 并支持自定义 User-Agent
func (l *Loader) LoadURLWithUA(ctx context.Context, urlStr string, userAgent string) ([]byte, error) {
	client, err := l.proxyPool.GetHTTPClient(30) // 文件下载使用 30 秒超时
	if err != nil {
		return nil, fmt.Errorf("获取 HTTP 客户端失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 使用自定义 User-Agent 或默认值
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP 状态码错误: %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return content, nil
}

// LoadURLs 并发加载多个 URL
func (l *Loader) LoadURLs(ctx context.Context, urls []string) []Result {
	results := make([]Result, len(urls))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, l.maxWorkers)

	for i, url := range urls {
		wg.Add(1)
		go func(index int, urlStr string) {
			defer wg.Done()

			// 限制并发数
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			content, err := l.loadURL(ctx, urlStr)
			results[index] = Result{
				Source:  urlStr,
				Content: content,
				Error:   err,
			}
		}(i, url)
	}

	wg.Wait()
	return results
}

// loadURL 加载单个 URL（内部使用，默认 User-Agent）
func (l *Loader) loadURL(ctx context.Context, urlStr string) ([]byte, error) {
	return l.LoadURLWithUA(ctx, urlStr, "")
}

// LoadFiles 并发加载多个本地文件
func (l *Loader) LoadFiles(ctx context.Context, paths []string) []Result {
	results := make([]Result, len(paths))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, l.maxWorkers)

	for i, path := range paths {
		wg.Add(1)
		go func(index int, filePath string) {
			defer wg.Done()

			// 限制并发数
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			content, err := l.loadFile(filePath)
			results[index] = Result{
				Source:  filePath,
				Content: content,
				Error:   err,
			}
		}(i, path)
	}

	wg.Wait()
	return results
}

// loadFile 加载单个本地文件
func (l *Loader) loadFile(path string) ([]byte, error) {
	// 处理相对路径
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("转换为绝对路径失败: %w", err)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	return content, nil
}

// LoadMixed 混合加载 URL 和本地文件
func (l *Loader) LoadMixed(ctx context.Context, sources []string) []Result {
	results := make([]Result, len(sources))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, l.maxWorkers)

	for i, source := range sources {
		wg.Add(1)
		go func(index int, src string) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			var content []byte
			var err error

			// 判断是 URL 还是文件路径
			if isURL(src) {
				content, err = l.loadURL(ctx, src)
			} else {
				content, err = l.loadFile(src)
			}

			results[index] = Result{
				Source:  src,
				Content: content,
				Error:   err,
			}
		}(i, source)
	}

	wg.Wait()
	return results
}

// SaveToFile 保存内容到文件
func (l *Loader) SaveToFile(path string, content []byte) error {
	// 确保目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	if err := os.WriteFile(path, content, 0644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	return nil
}
