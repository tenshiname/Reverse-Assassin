.PHONY: build run clean vet deps docker-build docker-up docker-down

# 编译二进制
build:
	go build -o bin/assassin ./cmd/assassin

# 启动 Web 服务 (开发模式)
serve: build
	./bin/assassin -mode server -port 8080

# 启动全自动 Agent (CLI 模式)
run: build
	./bin/assassin -mode cli

# 代码检查
vet:
	go vet ./...

# 依赖管理
deps:
	go mod tidy

# 清理
clean:
	rm -rf bin/
	rm -f assassin.db

# Docker
docker-build:
	docker build -t reverse-assassin .

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f
