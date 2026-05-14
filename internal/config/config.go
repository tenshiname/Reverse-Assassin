package config

import (
	"os"
	"sync"
)

// SettingsProvider 提供可动态更新的配置源
type SettingsProvider interface {
	GetSetting(key string) (string, error)
}

var (
	provider   SettingsProvider
	providerMu sync.RWMutex
)

// EnableDemoMode controls whether demo/sandbox mode is active.
// When enabled, authorization checks are bypassed and any story can be "taken over".
var (
	EnableDemoMode = true
	demoMu         sync.RWMutex
)

func GetDemoMode() bool {
	demoMu.RLock()
	defer demoMu.RUnlock()
	return EnableDemoMode
}

func SetDemoMode(v bool) {
	demoMu.Lock()
	defer demoMu.Unlock()
	EnableDemoMode = v
}

// SetProvider 设置运行时配置源 (由 server 在初始化时调用)
func SetProvider(p SettingsProvider) {
	providerMu.Lock()
	provider = p
	providerMu.Unlock()
}

func getProvider() SettingsProvider {
	providerMu.RLock()
	p := provider
	providerMu.RUnlock()
	return p
}

// 知乎 API 凭证 — 优先级: DB 存储 > 环境变量
func AppKey() string {
	if p := getProvider(); p != nil {
		if v, err := p.GetSetting("zhihu_token"); err == nil && v != "" {
			return v
		}
	}
	if v := os.Getenv("ZHIHU_APP_KEY"); v != "" {
		return v
	}
	return ""
}

func AppSecret() string {
	if p := getProvider(); p != nil {
		if v, err := p.GetSetting("zhihu_secret"); err == nil && v != "" {
			return v
		}
	}
	if v := os.Getenv("ZHIHU_APP_SECRET"); v != "" {
		return v
	}
	return ""
}

// OAuth 回调地址 (必须与知乎开放平台注册的一致，不从DB读取)
func OAuthRedirectURI() string {
	if v := os.Getenv("OAUTH_REDIRECT_URI"); v != "" {
		return v
	}
	return "https://drawings-translated-animation-reid.trycloudflare.com/oauth/callback"
}

// OAuth 凭证 — 优先级: DB 存储 > 环境变量 > 默认值
func OAuthAppID() string {
	if v := os.Getenv("OAUTH_APP_ID"); v != "" {
		return v
	}
	return "326"
}

func OAuthAppKey() string {
	if v := os.Getenv("OAUTH_APP_KEY"); v != "" {
		return v
	}
	return "55bff6ae9a304b17aa721043ffc6b081"
}

// 可用圈子 ID
const (
	RingOpenClaw  = "2001009660925334090" // OpenClaw 人类观察员
	RingA2A       = "2015023739549529606" // A2A for Reconnect
	RingHackathon = "2029619126742656657" // 黑客松脑洞补给站
	DefaultRing   = RingHackathon
)

// API 基础 URL
const (
	ZhihuAPIBase  = "https://openapi.zhihu.com"
	SearchAPIBase = "https://developer.zhihu.com"
)

// 限流配置
const (
	GlobalQPS     = 10   // 全局每秒请求数
	PinPerHour    = 5    // 每小时最多发想法数
	CommentPerPin = 20   // 每小时每个想法下最多评论数
	SearchPerDay  = 1000 // 每天搜索次数
	HotListPerDay = 100  // 每天热榜次数
)

// 轮询间隔 (秒)
const (
	MonitorInterval    = 60  // 评论监听间隔
	StoryPollInterval  = 300 // 新故事发现间隔
	BranchPollInterval = 30  // 互动响应轮询间隔
)

// 触发阈值
const (
	BranchTriggerThreshold = 0.6 // 遗憾指数触发分支生成的阈值
)

// LLM 配置 — 优先级: 数据库存储 > 环境变量 > 默认值
func LLMBaseURL() string {
	if p := getProvider(); p != nil {
		if v, err := p.GetSetting("llm_base_url"); err == nil && v != "" {
			return v
		}
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		return v
	}
	return "https://api.deepseek.com/v1"
}

func LLMAPIKey() string {
	if p := getProvider(); p != nil {
		if v, err := p.GetSetting("llm_api_key"); err == nil && v != "" {
			return v
		}
	}
	return os.Getenv("LLM_API_KEY")
}

func LLMModel() string {
	if p := getProvider(); p != nil {
		if v, err := p.GetSetting("llm_model"); err == nil && v != "" {
			return v
		}
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		return v
	}
	return "deepseek-chat"
}
