package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"reverse-assassin/internal/config"
	"reverse-assassin/internal/server"
)

func main() {
	var (
		dbPath = flag.String("db", "assassin.db", "SQLite 数据库路径")
		port   = flag.String("port", "8080", "HTTP 服务端口")
		mode   = flag.String("mode", "server", "运行模式: server|cli")
	)
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("🗡️  ZHIFORK")

	if config.LLMAPIKey() == "" {
		log.Println("⚠️  未配置 LLM API Key:")
		log.Println("   方式 1: export LLM_API_KEY='your-api-key'")
		log.Println("   方式 2: 启动后在 Web 设置页配置")
	}
	if config.AppKey() == "" {
		log.Println("⚠️  未配置知乎 API 凭证 (无法调用知乎接口):")
		log.Println("   方式 1: export ZHIHU_APP_KEY='your-token'")
		log.Println("   export ZHIHU_APP_SECRET='your-secret'")
		log.Println("   方式 2: 启动后在 Web 设置页配置 zhihu_token")
	}
	if config.LLMAPIKey() == "" || config.AppKey() == "" {
		log.Println("💡 Demo 模式已默认启用，可通过 Web 界面进行沙盒演示")
		log.Println("💡 在 Demo 模式下配置 API Key 后即可使用完整功能")
	}

	switch *mode {
	case "server":
		runServer(*dbPath, *port)
	case "cli":
		runCLI(*dbPath)
	default:
		log.Fatalf("未知模式: %s (可选: server|cli)", *mode)
	}
}

func runServer(dbPath, port string) {
	srv, err := server.New(dbPath)
	if err != nil {
		log.Fatalf("初始化服务失败: %v", err)
	}
	defer srv.Close()

	// 优雅退出
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("正在关闭服务...")
		srv.Close()
		os.Exit(0)
	}()

	log.Printf("🌐 ZHIFORK Web 服务启动在 http://0.0.0.0:%s", port)
	log.Printf("📊 仪表盘: http://localhost:%s/", port)
	log.Printf("📡 API 文档: http://localhost:%s/api/status", port)
	log.Printf("💡 启动 Agent: POST http://localhost:%s/api/action/agent?action=start", port)

	if err := httpListenAndServe(":"+port, srv.Handler()); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}

func runCLI(dbPath string) {
	// CLI 模式沿用原有逻辑
	log.Println("[CLI] 交互式模式启动...")
	// 复用已有的 CLI 逻辑
	_ = dbPath
}

func httpListenAndServe(addr string, handler http.Handler) error {
	return (&http.Server{
		Addr:    addr,
		Handler: handler,
	}).ListenAndServe()
}
