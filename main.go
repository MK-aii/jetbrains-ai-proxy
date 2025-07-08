package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"jetbrains-ai-proxy/internal/apiserver"
	"jetbrains-ai-proxy/internal/config"
	"jetbrains-ai-proxy/internal/jetbrains"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// 定义命令行参数
	configFile := flag.String("config", "", "配置文件路径")
	port := flag.Int("p", 0, "服务器监听端口 (覆盖配置文件)")
	host := flag.String("h", "", "服务器监听地址 (覆盖配置文件)")
	jwtTokens := flag.String("c", "", "JWT Tokens值，多个token用逗号分隔 (覆盖配置文件)")
	bearerToken := flag.String("k", "", "Bearer Token值 (覆盖配置文件)")
	loadBalanceStrategy := flag.String("s", "", "负载均衡策略: round_robin 或 random (覆盖配置文件)")
	generateConfig := flag.Bool("generate-config", false, "生成示例配置文件")
	printConfig := flag.Bool("print-config", false, "打印当前配置信息")

	flag.Usage = func() {
		fmt.Printf("用法: %s [选项]\n\n", flag.CommandLine.Name())
		fmt.Println("选项:")
		flag.PrintDefaults()
		fmt.Println("\n配置优先级 (从高到低):")
		fmt.Println("  1. 命令行参数")
		fmt.Println("  2. 环境变量")
		fmt.Println("  3. 配置文件")
		fmt.Println("  4. 默认值")
		fmt.Println("\n配置方式:")
		fmt.Println("  方式1 - 使用配置文件:")
		fmt.Println("    ./jetbrains-ai-proxy --generate-config  # 生成示例配置")
		fmt.Println("    # 编辑 config/config.json")
		fmt.Println("    ./jetbrains-ai-proxy")
		fmt.Println("")
		fmt.Println("  方式2 - 使用环境变量:")
		fmt.Println("    export JWT_TOKENS=\"jwt1,jwt2,jwt3\"")
		fmt.Println("    export BEARER_TOKEN=\"your_token\"")
		fmt.Println("    ./jetbrains-ai-proxy")
		fmt.Println("")
		fmt.Println("  方式3 - 使用命令行参数:")
		fmt.Println("    ./jetbrains-ai-proxy -c \"jwt1,jwt2,jwt3\" -k \"bearer_token\"")
		fmt.Println("")
		fmt.Println("负载均衡策略:")
		fmt.Println("  round_robin: 轮询策略（默认）")
		fmt.Println("  random: 随机策略")
	}

	flag.Parse()

	// 处理特殊命令
	if *generateConfig {
		if err := generateExampleConfig(); err != nil {
			log.Fatalf("Failed to generate config: %v", err)
		}
		return
	}

	// 获取配置管理器
	configManager := config.GetGlobalConfig()

	// 如果指定了配置文件，设置环境变量
	if *configFile != "" {
		os.Setenv("CONFIG_FILE", *configFile)
	}

	// 加载配置
	if err := configManager.LoadConfig(); err != nil {
		log.Printf("Warning: %v", err)
		log.Println("Continuing with command line arguments and environment variables...")
	}

	// 应用命令行参数覆盖
	applyCommandLineOverrides(configManager, port, host, jwtTokens, bearerToken, loadBalanceStrategy)

	// 打印配置信息
	if *printConfig {
		configManager.PrintConfig()
		return
	}

	// 验证配置
	if !configManager.HasJWTTokens() {
		log.Fatal("No JWT tokens configured. Use --generate-config to create example configuration.")
	}

	cfg := configManager.GetConfig()
	if cfg.BearerToken == "" {
		log.Fatal("Bearer token is required. Please configure it in config file, environment variable, or command line.")
	}

	// 初始化JWT负载均衡器
	if err := jetbrains.InitializeFromConfig(); err != nil {
		log.Fatalf("Failed to initialize JWT balancer: %v", err)
	}

	// 设置优雅关闭
	setupGracefulShutdown()

	// 启动配置文件监控
	discovery := config.NewConfigDiscovery(configManager)
	discovery.WatchConfig()

	// 创建Echo实例
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	// 添加管理端点
	setupManagementEndpoints(e, configManager)

	// 注册API路由
	apiserver.RegisterRoutes(e)

	// 启动服务器
	addr := fmt.Sprintf("%s:%d", cfg.ServerHost, cfg.ServerPort)
	log.Printf("Server starting on %s", addr)
	configManager.PrintConfig()

	if err := e.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("start server error: %v", err)
	}
}

// generateExampleConfig 生成示例配置
func generateExampleConfig() error {
	manager := config.NewManager()

	// 生成JSON配置文件
	if err := manager.GenerateExampleConfig("config/config.json"); err != nil {
		return fmt.Errorf("failed to generate JSON config: %v", err)
	}

	// 生成.env示例文件
	config.NewConfigDiscovery(manager)
	envContent := `# JetBrains AI Proxy Configuration
# Copy this file to .env and fill in your actual values

# Multiple JWT tokens (comma-separated)
JWT_TOKENS=eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9...,eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9...

# Bearer token for API authentication
BEARER_TOKEN=your_bearer_token_here

# Load balancing strategy: round_robin or random
LOAD_BALANCE_STRATEGY=round_robin

# HTTP proxy
PROXY_URL=http://127.0.0.1:7890

# Server configuration
SERVER_HOST=0.0.0.0
SERVER_PORT=8080
`

	if err := os.WriteFile(".env.example", []byte(envContent), 0644); err != nil {
		return fmt.Errorf("failed to generate .env example: %v", err)
	}

	fmt.Println("✅ Example configuration files generated:")
	fmt.Println("   📄 config/config.json - JSON configuration file")
	fmt.Println("   📄 .env.example - Environment variables example")
	fmt.Println("")
	fmt.Println("📝 Next steps:")
	fmt.Println("   1. Edit config/config.json with your JWT tokens")
	fmt.Println("   2. Or copy .env.example to .env and edit it")
	fmt.Println("   3. Run: ./jetbrains-ai-proxy")

	return nil
}

// applyCommandLineOverrides 应用命令行参数覆盖
func applyCommandLineOverrides(manager *config.Manager, port *int, host, jwtTokens, bearerToken, strategy *string) {
	if *jwtTokens != "" {
		manager.SetJWTTokens(*jwtTokens)
		log.Printf("JWT tokens overridden by command line")
	}

	if *bearerToken != "" {
		manager.SetBearerToken(*bearerToken)
		log.Printf("Bearer token overridden by command line")
	}

	if *strategy != "" {
		manager.SetLoadBalanceStrategy(*strategy)
		log.Printf("Load balance strategy overridden by command line: %s", *strategy)
	}

	// 覆盖服务器配置
	cfg := manager.GetConfig()
	if *port > 0 {
		cfg.ServerPort = *port
		log.Printf("Server port overridden by command line: %d", *port)
	}

	if *host != "" {
		cfg.ServerHost = *host
		log.Printf("Server host overridden by command line: %s", *host)
	}
}

// setupManagementEndpoints 设置管理端点
func setupManagementEndpoints(e *echo.Echo, manager *config.Manager) {
	// 健康检查端点
	e.GET("/health", func(c echo.Context) error {
		healthy, total := jetbrains.GetBalancerStats()
		cfg := manager.GetConfig()

		return c.JSON(http.StatusOK, map[string]interface{}{
			"status":         "ok",
			"healthy_tokens": healthy,
			"total_tokens":   total,
			"strategy":       cfg.LoadBalanceStrategy,
			"server_info": map[string]interface{}{
				"host": cfg.ServerHost,
				"port": cfg.ServerPort,
			},
		})
	})

	// 配置信息端点
	e.GET("/config", func(c echo.Context) error {
		discovery := config.NewConfigDiscovery(manager)
		summary := discovery.GetConfigSummary()
		return c.JSON(http.StatusOK, summary)
	})

	// 重载配置端点
	e.POST("/reload", func(c echo.Context) error {
		if err := jetbrains.ReloadConfig(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"message": "Configuration reloaded successfully",
		})
	})

	// 负载均衡器统计端点
	e.GET("/stats", func(c echo.Context) error {
		healthy, total := jetbrains.GetBalancerStats()
		cfg := manager.GetConfig()

		return c.JSON(http.StatusOK, map[string]interface{}{
			"balancer": map[string]interface{}{
				"healthy_tokens": healthy,
				"total_tokens":   total,
				"strategy":       cfg.LoadBalanceStrategy,
			},
			"config": map[string]interface{}{
				"health_check_interval": cfg.HealthCheckInterval.String(),
				"server_host":           cfg.ServerHost,
				"server_port":           cfg.ServerPort,
			},
		})
	})
}

// setupGracefulShutdown 设置优雅关闭
func setupGracefulShutdown() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		log.Println("Shutting down gracefully...")
		jetbrains.StopBalancer()
		os.Exit(0)
	}()
}
