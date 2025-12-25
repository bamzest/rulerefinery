package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// ProxyType 代理类型
type ProxyType int

const (
	ProxyTypeSocks5 ProxyType = iota
	ProxyTypeSocks4
	ProxyTypeHTTPS
	ProxyTypeHTTP
)

// ProxyInfo 代理信息
type ProxyInfo struct {
	URL  string
	Type ProxyType
}

// Pool 代理池
type Pool struct {
	proxies []ProxyInfo
	enabled bool
	current int
	mu      sync.RWMutex
}

// NewPool 创建代理池
func NewPool(proxyURLs []string, enabled bool) (*Pool, error) {
	pool := &Pool{
		enabled: enabled,
		proxies: make([]ProxyInfo, 0, len(proxyURLs)),
	}

	if !enabled {
		return pool, nil
	}

	// 按优先级排序: socks5 > socks4 > https > http
	for _, urlStr := range proxyURLs {
		u, err := url.Parse(urlStr)
		if err != nil {
			return nil, fmt.Errorf("解析代理 URL 失败 %s: %w", urlStr, err)
		}

		var proxyType ProxyType
		switch strings.ToLower(u.Scheme) {
		case "socks5":
			proxyType = ProxyTypeSocks5
		case "socks4":
			proxyType = ProxyTypeSocks4
		case "https":
			proxyType = ProxyTypeHTTPS
		case "http":
			proxyType = ProxyTypeHTTP
		default:
			return nil, fmt.Errorf("不支持的代理协议: %s", u.Scheme)
		}

		pool.proxies = append(pool.proxies, ProxyInfo{
			URL:  urlStr,
			Type: proxyType,
		})
	}

	// 按优先级排序
	pool.sortProxiesByPriority()

	return pool, nil
}

// sortProxiesByPriority 按优先级排序代理
func (p *Pool) sortProxiesByPriority() {
	// 简单的冒泡排序，按 ProxyType 值排序
	for i := 0; i < len(p.proxies)-1; i++ {
		for j := 0; j < len(p.proxies)-i-1; j++ {
			if p.proxies[j].Type > p.proxies[j+1].Type {
				p.proxies[j], p.proxies[j+1] = p.proxies[j+1], p.proxies[j]
			}
		}
	}
}

// GetHTTPClient 获取配置了代理的 HTTP 客户端
// timeout: 超时时间（秒），如果为 0 则使用默认值 30 秒
func (p *Pool) GetHTTPClient(timeout int) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 30
	}
	timeoutDuration := time.Duration(timeout) * time.Second

	if !p.enabled || len(p.proxies) == 0 {
		return &http.Client{
			Timeout: timeoutDuration,
		}, nil
	}

	p.mu.RLock()
	proxyInfo := p.proxies[p.current%len(p.proxies)]
	p.mu.RUnlock()

	proxyURL, err := url.Parse(proxyInfo.URL)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	switch proxyInfo.Type {
	case ProxyTypeSocks5, ProxyTypeSocks4:
		// 使用 SOCKS 代理
		dialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("创建 SOCKS 代理失败: %w", err)
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	case ProxyTypeHTTP, ProxyTypeHTTPS:
		// 使用 HTTP/HTTPS 代理
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeoutDuration,
	}, nil
}

// NextProxy 切换到下一个代理
func (p *Pool) NextProxy() {
	if !p.enabled || len(p.proxies) == 0 {
		return
	}

	p.mu.Lock()
	p.current = (p.current + 1) % len(p.proxies)
	p.mu.Unlock()
}

// GetCurrentProxy 获取当前代理信息
func (p *Pool) GetCurrentProxy() string {
	if !p.enabled || len(p.proxies) == 0 {
		return "直连"
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.proxies[p.current%len(p.proxies)].URL
}

// IsEnabled 是否启用代理
func (p *Pool) IsEnabled() bool {
	return p.enabled
}

// Count 代理数量
func (p *Pool) Count() int {
	return len(p.proxies)
}
