package jetbrains

import (
	"context"
	"fmt"
	"github.com/go-resty/resty/v2"
	"jetbrains-ai-proxy/internal/balancer"
	"jetbrains-ai-proxy/internal/config"
	"jetbrains-ai-proxy/internal/types"
	"jetbrains-ai-proxy/internal/utils"
	"log"
	"sync"
)

var (
	jwtBalancer    balancer.JWTBalancer
	healthChecker  *balancer.HealthChecker
	initOnce       sync.Once
	configManager  *config.Manager
)

// InitializeFromConfig 从配置管理器初始化JWT负载均衡器
func InitializeFromConfig() error {
	var initErr error

	initOnce.Do(func() {
		configManager = config.GetGlobalConfig()

		// 加载配置
		if err := configManager.LoadConfig(); err != nil {
			initErr = fmt.Errorf("failed to load config: %v", err)
			return
		}

		// 获取配置
		cfg := configManager.GetConfig()
		tokens := configManager.GetJWTTokens()

		if len(tokens) == 0 {
			initErr = fmt.Errorf("no JWT tokens configured")
			return
		}

		// 创建负载均衡器
		jwtBalancer = balancer.NewJWTBalancer(tokens, cfg.LoadBalanceStrategy)

		// 创建并启动健康检查器
		healthChecker = balancer.NewHealthChecker(jwtBalancer)
		if cfg.HealthCheckInterval > 0 {
			healthChecker.SetCheckInterval(cfg.HealthCheckInterval)
		}
		// 根据配置设置HTTP代理
		utils.RestySSEClient.SetProxy(cfg.ProxyURL)
		healthChecker.SetProxy(cfg.ProxyURL)

		healthChecker.Start()

		log.Printf("JWT balancer initialized from config:")
		log.Printf("  - Tokens: %d", len(tokens))
		log.Printf("  - Strategy: %s", cfg.LoadBalanceStrategy)
		log.Printf("  - Health check interval: %v", cfg.HealthCheckInterval)
		log.Printf("  - Proxy URL: %s", cfg.ProxyURL)
	})

	return initErr
}

// InitializeBalancer 初始化JWT负载均衡器（向后兼容）
func InitializeBalancer(tokens []string, strategy string) error {
	if len(tokens) == 0 {
		return fmt.Errorf("no JWT tokens provided")
	}

	var balanceStrategy config.LoadBalanceStrategy
	switch strategy {
	case "random":
		balanceStrategy = config.Random
	case "round_robin", "":
		balanceStrategy = config.RoundRobin
	default:
		balanceStrategy = config.RoundRobin
	}

	// 创建负载均衡器
	jwtBalancer = balancer.NewJWTBalancer(tokens, balanceStrategy)

	// 创建并启动健康检查器
	healthChecker = balancer.NewHealthChecker(jwtBalancer)
	healthChecker.Start()

	log.Printf("JWT balancer initialized with %d tokens, strategy: %s", len(tokens), string(balanceStrategy))
	return nil
}

// ReloadConfig 重新加载配置
func ReloadConfig() error {
	if configManager == nil {
		return fmt.Errorf("config manager not initialized")
	}

	// 重新加载配置
	if err := configManager.LoadConfig(); err != nil {
		return fmt.Errorf("failed to reload config: %v", err)
	}

	// 获取新配置
	cfg := configManager.GetConfig()
	tokens := configManager.GetJWTTokens()

	if len(tokens) == 0 {
		return fmt.Errorf("no JWT tokens in reloaded config")
	}

	// 更新负载均衡器
	if jwtBalancer != nil {
		jwtBalancer.RefreshTokens(tokens)
	}

	// 更新健康检查间隔
	if healthChecker != nil && cfg.HealthCheckInterval > 0 {
		healthChecker.SetCheckInterval(cfg.HealthCheckInterval)
	}

	// 更新HTTP代理设置
	utils.RestySSEClient.SetProxy(cfg.ProxyURL)
	if healthChecker != nil {
		healthChecker.SetProxy(cfg.ProxyURL)
	}

	log.Printf("Proxy URL updated to: %s", cfg.ProxyURL)

	log.Printf("Config reloaded successfully:")
	log.Printf("  - Tokens: %d", len(tokens))
	log.Printf("  - Strategy: %s", cfg.LoadBalanceStrategy)

	return nil
}

// StopBalancer 停止负载均衡器
func StopBalancer() {
	if healthChecker != nil {
		healthChecker.Stop()
	}
}

// GetConfigManager 获取配置管理器
func GetConfigManager() *config.Manager {
	return configManager
}

func SendJetbrainsRequest(ctx context.Context, req *types.JetbrainsRequest) (*resty.Response, error) {
	// 获取一个可用的JWT token
	token, err := jwtBalancer.GetToken()
	if err != nil {
		log.Printf("failed to get JWT token: %v", err)
		return nil, fmt.Errorf("no available JWT tokens: %v", err)
	}

	resp, err := utils.RestySSEClient.R().
		SetContext(ctx).
		SetHeader(types.JwtTokenKey, token).
		SetHeader("User-Agent", "ktor-client").
		SetDoNotParseResponse(true).
		SetBody(req).
		Post(types.ChatStreamV7)

	if err != nil {
		log.Printf("jetbrains ai req error: %v", err)
		// 标记token为不健康
		jwtBalancer.MarkTokenUnhealthy(token)
		return nil, err
	}

	// 检查响应状态码
	if resp.StatusCode() == 401 {
		// 401表示token无效，标记为不健康
		jwtBalancer.MarkTokenUnhealthy(token)
		log.Printf("JWT token invalid (401): %s...", token[:min(len(token), 10)])
		return nil, fmt.Errorf("JWT token invalid")
	} else if resp.StatusCode() == 200 {
		// 成功响应，确保token标记为健康
		jwtBalancer.MarkTokenHealthy(token)
	}

	return resp, nil
}

// GetBalancerStats 获取负载均衡器统计信息
func GetBalancerStats() (int, int) {
	if jwtBalancer == nil {
		return 0, 0
	}
	return jwtBalancer.GetHealthyTokenCount(), jwtBalancer.GetTotalTokenCount()
}

// min 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
