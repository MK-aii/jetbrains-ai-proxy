package balancer

import (
	"context"
	"github.com/go-resty/resty/v2"
	"jetbrains-ai-proxy/internal/types"
	"log"
	"sync"
	"time"
)

// HealthChecker JWT健康检查器
type HealthChecker struct {
	balancer      JWTBalancer
	client        *resty.Client
	checkInterval time.Duration
	timeout       time.Duration
	maxRetries    int
	stopChan      chan struct{}
	wg            sync.WaitGroup
	running       bool
	mutex         sync.RWMutex
}

// NewHealthChecker 创建健康检查器
func NewHealthChecker(balancer JWTBalancer) *HealthChecker {
	client := resty.New().
		SetTimeout(10 * time.Second).
		SetHeaders(map[string]string{
			"Content-Type": "application/json",
		})

	return &HealthChecker{
		balancer:      balancer,
		client:        client,
		checkInterval: 30 * time.Second, // 每30秒检查一次
		timeout:       10 * time.Second,
		maxRetries:    3,
		stopChan:      make(chan struct{}),
	}
}

// Start 启动健康检查
func (hc *HealthChecker) Start() {
	hc.mutex.Lock()
	defer hc.mutex.Unlock()

	if hc.running {
		return
	}

	hc.running = true
	hc.wg.Add(1)

	go hc.healthCheckLoop()
	log.Println("JWT health checker started")
}

// Stop 停止健康检查
func (hc *HealthChecker) Stop() {
	hc.mutex.Lock()
	defer hc.mutex.Unlock()

	if !hc.running {
		return
	}

	hc.running = false
	close(hc.stopChan)
	hc.wg.Wait()
	log.Println("JWT health checker stopped")
}

// healthCheckLoop 健康检查循环
func (hc *HealthChecker) healthCheckLoop() {
	defer hc.wg.Done()

	ticker := time.NewTicker(hc.checkInterval)
	defer ticker.Stop()

	// 启动时立即执行一次检查
	hc.performHealthCheck()

	for {
		select {
		case <-ticker.C:
			hc.performHealthCheck()
		case <-hc.stopChan:
			return
		}
	}
}

// performHealthCheck 执行健康检查
func (hc *HealthChecker) performHealthCheck() {
	log.Println("Performing JWT health check...")

	// 获取所有tokens进行检查
	baseBalancer, ok := hc.balancer.(*BaseBalancer)
	if !ok {
		log.Println("Warning: Cannot access tokens for health check")
		return
	}

	baseBalancer.mutex.RLock()
	tokens := make([]string, 0, len(baseBalancer.tokens))
	for token := range baseBalancer.tokens {
		tokens = append(tokens, token)
	}
	baseBalancer.mutex.RUnlock()

	// 并发检查所有tokens
	var wg sync.WaitGroup
	for _, token := range tokens {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			hc.checkTokenHealth(t)
		}(token)
	}
	wg.Wait()

	healthyCount := hc.balancer.GetHealthyTokenCount()
	totalCount := hc.balancer.GetTotalTokenCount()
	log.Printf("Health check completed: %d/%d tokens healthy", healthyCount, totalCount)
}

// checkTokenHealth 检查单个token的健康状态
func (hc *HealthChecker) checkTokenHealth(token string) {
	ctx, cancel := context.WithTimeout(context.Background(), hc.timeout)
	defer cancel()

	// 创建一个简单的测试请求
	testRequest := &types.JetbrainsRequest{
		Prompt:  types.PROMPT,
		Profile: "openai-gpt-4o", // 使用一个通用的profile进行测试
		Chat: types.ChatField{
			MessageField: []types.MessageField{
				{
					Type:    "user_message",
					Content: "test", // 简单的测试消息
				},
			},
		},
	}

	success := false
	for retry := 0; retry < hc.maxRetries; retry++ {
		if hc.testTokenRequest(ctx, token, testRequest) {
			success = true
			break
		}

		// 重试前等待一小段时间
		if retry < hc.maxRetries-1 {
			time.Sleep(time.Second)
		}
	}

	if success {
		hc.balancer.MarkTokenHealthy(token)
	} else {
		hc.balancer.MarkTokenUnhealthy(token)
		log.Printf("JWT token health check failed: %s...", token[:min(len(token), 10)])
	}
}

// testTokenRequest 测试token请求
func (hc *HealthChecker) testTokenRequest(ctx context.Context, token string, req *types.JetbrainsRequest) bool {
	resp, err := hc.client.R().
		SetContext(ctx).
		SetHeader(types.JwtTokenKey, token).
		SetHeader("User-Agent", "ktor-client").
		SetBody(req).
		Post(types.ChatStreamV7)

	if err != nil {
		log.Printf("Health check request error for token %s...: %v", token[:min(len(token), 10)], err)
		return false
	}

	// 检查响应状态码
	if resp.StatusCode() == 200 {
		return true
	}

	// 401表示token无效，403可能表示配额用完但token有效
	if resp.StatusCode() == 403 {
		// 配额用完但token有效，仍然标记为健康
		return true
	}

	log.Printf("Health check failed for token %s...: status %d, body: %s",
		token[:min(len(token), 10)], resp.StatusCode(), resp.String())
	return false
}

// SetCheckInterval 设置检查间隔
func (hc *HealthChecker) SetCheckInterval(interval time.Duration) {
	hc.mutex.Lock()
	defer hc.mutex.Unlock()
	hc.checkInterval = interval
}

// SetTimeout 设置请求超时
func (hc *HealthChecker) SetTimeout(timeout time.Duration) {
	hc.mutex.Lock()
	defer hc.mutex.Unlock()
	hc.timeout = timeout
}

// SetMaxRetries 设置最大重试次数
func (hc *HealthChecker) SetMaxRetries(retries int) {
	hc.mutex.Lock()
	defer hc.mutex.Unlock()
	hc.maxRetries = retries
}

// SetProxy 设置HTTP代理
func (hc *HealthChecker) SetProxy(proxyURL string) {
	hc.mutex.Lock()
	defer hc.mutex.Unlock()

	if proxyURL != "" {
		hc.client.SetProxy(proxyURL)
	} else {
		// 传入空字符串可以清除代理设置
		hc.client.SetProxy("")
	}
}
