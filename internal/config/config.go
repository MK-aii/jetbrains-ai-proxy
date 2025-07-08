package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

var (
	GlobalConfig *Manager
	once         sync.Once
)

// LoadBalanceStrategy 负载均衡策略
type LoadBalanceStrategy string

const (
	RoundRobin LoadBalanceStrategy = "round_robin"
	Random     LoadBalanceStrategy = "random"
)

// JWTTokenConfig JWT token配置
type JWTTokenConfig struct {
	Token       string            `json:"token"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Priority    int               `json:"priority,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Config 应用配置
type Config struct {
	JetbrainsTokens     []JWTTokenConfig    `json:"jetbrains_tokens"`
	BearerToken         string              `json:"bearer_token"`
	LoadBalanceStrategy LoadBalanceStrategy `json:"load_balance_strategy"`
	HealthCheckInterval time.Duration       `json:"health_check_interval"`
	ServerPort          int                 `json:"server_port"`
	ServerHost          string              `json:"server_host"`
	ProxyURL            string              `json:"proxy_url,omitempty"`
}

// Manager 配置管理器
type Manager struct {
	config     *Config
	configPath string
	mutex      sync.RWMutex
}

// GetGlobalConfig 获取全局配置管理器（单例）
func GetGlobalConfig() *Manager {
	once.Do(func() {
		GlobalConfig = NewManager()
	})
	return GlobalConfig
}

// NewManager 创建新的配置管理器
func NewManager() *Manager {
	return &Manager{
		config: &Config{
			LoadBalanceStrategy: RoundRobin,
			HealthCheckInterval: 30 * time.Second,
			ServerPort:          8080,
			ServerHost:          "0.0.0.0",
		},
	}
}

// LoadConfig 加载配置
func (m *Manager) LoadConfig() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// 1. 首先尝试加载 .env 文件
	_ = godotenv.Load()

	// 2. 自动发现并加载配置文件
	if err := m.loadConfigFile(); err != nil {
		log.Printf("Warning: Failed to load config file: %v", err)
	}

	// 3. 从环境变量加载配置
	m.loadFromEnv()

	// 4. 验证配置
	return m.validateConfig()
}

// loadConfigFile 自动发现并加载配置文件
func (m *Manager) loadConfigFile() error {
	// 配置文件搜索路径
	searchPaths := []string{
		"config.json",
		"config/config.json",
		"configs/config.json",
		".config/jetbrains-ai-proxy.json",
		os.ExpandEnv("$HOME/.config/jetbrains-ai-proxy/config.json"),
	}

	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			log.Printf("Found config file: %s", path)
			return m.loadFromFile(path)
		}
	}

	return fmt.Errorf("no config file found in search paths")
}

// loadFromFile 从文件加载配置
func (m *Manager) loadFromFile(path string) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config file %s: %v", path, err)
	}

	var fileConfig Config
	if err := json.Unmarshal(data, &fileConfig); err != nil {
		return fmt.Errorf("failed to parse config file %s: %v", path, err)
	}

	// 合并配置
	m.mergeConfig(&fileConfig)
	m.configPath = path
	log.Printf("Loaded config from file: %s", path)
	return nil
}

// loadFromEnv 从环境变量加载配置
func (m *Manager) loadFromEnv() {
	// JWT Tokens
	jwtTokensStr := os.Getenv("JWT_TOKENS")
	if jwtTokensStr == "" {
		// 兼容旧的单token配置
		jwtTokensStr = os.Getenv("JWT_TOKEN")
	}

	if jwtTokensStr != "" {
		tokens := m.parseJWTTokens(jwtTokensStr)
		if len(tokens) > 0 {
			m.config.JetbrainsTokens = tokens
		}
	}

	// Bearer Token
	if bearerToken := os.Getenv("BEARER_TOKEN"); bearerToken != "" {
		m.config.BearerToken = bearerToken
	}

	// Load Balance Strategy
	if strategy := os.Getenv("LOAD_BALANCE_STRATEGY"); strategy != "" {
		if strategy == string(RoundRobin) || strategy == string(Random) {
			m.config.LoadBalanceStrategy = LoadBalanceStrategy(strategy)
		}
	}

	// Server configuration
	if port := os.Getenv("SERVER_PORT"); port != "" {
		if p, err := parsePort(port); err == nil {
			m.config.ServerPort = p
		}
	}

	if host := os.Getenv("SERVER_HOST"); host != "" {
		m.config.ServerHost = host
	}

	// Proxy URL
	if proxyURL := os.Getenv("PROXY_URL"); proxyURL != "" {
		m.config.ProxyURL = proxyURL
	}
}

// parseJWTTokens 解析JWT tokens字符串
func (m *Manager) parseJWTTokens(tokensStr string) []JWTTokenConfig {
	var tokens []JWTTokenConfig
	tokenList := strings.Split(tokensStr, ",")

	for i, token := range tokenList {
		token = strings.TrimSpace(token)
		if token != "" {
			tokens = append(tokens, JWTTokenConfig{
				Token:    token,
				Name:     fmt.Sprintf("JWT_%d", i+1),
				Priority: 1,
			})
		}
	}

	return tokens
}

// mergeConfig 合并配置
func (m *Manager) mergeConfig(other *Config) {
	if len(other.JetbrainsTokens) > 0 {
		m.config.JetbrainsTokens = other.JetbrainsTokens
	}
	if other.BearerToken != "" {
		m.config.BearerToken = other.BearerToken
	}
	if other.LoadBalanceStrategy != "" {
		m.config.LoadBalanceStrategy = other.LoadBalanceStrategy
	}
	if other.HealthCheckInterval > 0 {
		m.config.HealthCheckInterval = other.HealthCheckInterval
	}
	if other.ServerPort > 0 {
		m.config.ServerPort = other.ServerPort
	}
	if other.ServerHost != "" {
		m.config.ServerHost = other.ServerHost
	}
	if other.ProxyURL != "" {
		m.config.ProxyURL = other.ProxyURL
	}
}

// validateConfig 验证配置
func (m *Manager) validateConfig() error {
	if len(m.config.JetbrainsTokens) == 0 {
		return fmt.Errorf("no JWT tokens configured")
	}

	if m.config.BearerToken == "" {
		return fmt.Errorf("bearer token is required")
	}

	if m.config.ServerPort <= 0 || m.config.ServerPort > 65535 {
		return fmt.Errorf("invalid server port: %d", m.config.ServerPort)
	}

	return nil
}

// GetConfig 获取当前配置
func (m *Manager) GetConfig() *Config {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// 返回配置的副本
	configCopy := *m.config
	return &configCopy
}

// GetJWTTokens 获取JWT tokens字符串列表
func (m *Manager) GetJWTTokens() []string {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	tokens := make([]string, len(m.config.JetbrainsTokens))
	for i, tokenConfig := range m.config.JetbrainsTokens {
		tokens[i] = tokenConfig.Token
	}
	return tokens
}

// GetJWTTokenConfigs 获取JWT token配置列表
func (m *Manager) GetJWTTokenConfigs() []JWTTokenConfig {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	configs := make([]JWTTokenConfig, len(m.config.JetbrainsTokens))
	copy(configs, m.config.JetbrainsTokens)
	return configs
}

// SetJWTTokens 设置JWT tokens（用于命令行参数）
func (m *Manager) SetJWTTokens(tokensStr string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if tokensStr != "" {
		m.config.JetbrainsTokens = m.parseJWTTokens(tokensStr)
	}
}

// SetBearerToken 设置Bearer token
func (m *Manager) SetBearerToken(token string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.config.BearerToken = token
}

// SetLoadBalanceStrategy 设置负载均衡策略
func (m *Manager) SetLoadBalanceStrategy(strategy string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if strategy == string(RoundRobin) || strategy == string(Random) {
		m.config.LoadBalanceStrategy = LoadBalanceStrategy(strategy)
	}
}

// HasJWTTokens 检查是否有可用的JWT tokens
func (m *Manager) HasJWTTokens() bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return len(m.config.JetbrainsTokens) > 0
}

// SaveConfig 保存配置到文件
func (m *Manager) SaveConfig() error {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	if m.configPath == "" {
		m.configPath = "config.json"
	}

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	if err := ioutil.WriteFile(m.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	log.Printf("Config saved to: %s", m.configPath)
	return nil
}

// GenerateExampleConfig 生成示例配置文件
func (m *Manager) GenerateExampleConfig(path string) error {
	exampleConfig := &Config{
		JetbrainsTokens: []JWTTokenConfig{
			{
				Token:       "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9...",
				Name:        "Primary_JWT",
				Description: "Primary JWT token for JetBrains AI",
				Priority:    1,
				Metadata: map[string]string{
					"environment": "production",
					"region":      "us-east-1",
				},
			},
			{
				Token:       "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9...",
				Name:        "Secondary_JWT",
				Description: "Secondary JWT token for load balancing",
				Priority:    2,
				Metadata: map[string]string{
					"environment": "production",
					"region":      "us-west-2",
				},
			},
		},
		BearerToken:         "your_bearer_token_here",
		LoadBalanceStrategy: RoundRobin,
		HealthCheckInterval: 30 * time.Second,
		ServerPort:          8080,
		ServerHost:          "0.0.0.0",
		ProxyURL:            "",
	}

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	data, err := json.MarshalIndent(exampleConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal example config: %v", err)
	}

	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write example config: %v", err)
	}

	log.Printf("Example config generated: %s", path)
	return nil
}

// PrintConfig 打印当前配置信息
func (m *Manager) PrintConfig() {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	fmt.Println("=== Current Configuration ===")
	fmt.Printf("JWT Tokens: %d configured\n", len(m.config.JetbrainsTokens))
	for i, token := range m.config.JetbrainsTokens {
		fmt.Printf("  %d. %s (%s...)\n", i+1, token.Name, token.Token[:min(len(token.Token), 20)])
	}
	fmt.Printf("Bearer Token: %s...\n", m.config.BearerToken[:min(len(m.config.BearerToken), 20)])
	fmt.Printf("Load Balance Strategy: %s\n", m.config.LoadBalanceStrategy)
	fmt.Printf("Health Check Interval: %v\n", m.config.HealthCheckInterval)
	fmt.Printf("Server: %s:%d\n", m.config.ServerHost, m.config.ServerPort)
	fmt.Printf("Proxy URL: %s\n", m.config.ProxyURL)
	if m.configPath != "" {
		fmt.Printf("Config File: %s\n", m.configPath)
	}
	fmt.Println("=============================")
}

// 辅助函数
func parsePort(portStr string) (int, error) {
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return 0, err
	}
	if port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid port: %d", port)
	}
	return port, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// 向后兼容的全局变量和函数
var JetbrainsAiConfig *Config

// LoadConfig 向后兼容的配置加载函数
func LoadConfig() *Config {
	manager := GetGlobalConfig()
	if err := manager.LoadConfig(); err != nil {
		log.Printf("Warning: %v", err)
	}

	JetbrainsAiConfig = manager.GetConfig()
	return JetbrainsAiConfig
}

// SetProxyURL 设置HTTP代理地址
func (m *Manager) SetProxyURL(proxy string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	m.config.ProxyURL = proxy
}
