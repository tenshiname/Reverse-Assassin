# 反转刺客 · Reverse Assassin

基于知乎开放 API + LLM 的互动叙事重构引擎。自动发现故事、AI 解构枢纽点、生成平行宇宙分支，发布到知乎圈子与读者互动。

## 快速开始

```bash
git clone https://github.com/tenshiname/Reverse-Assassin.git
cd Reverse-Assassin

# 配置环境变量
export ZHIHU_APP_KEY="你的知乎用户Token"
export ZHIHU_APP_SECRET="你的应用密钥"
export LLM_API_KEY="sk-..."
export LLM_BASE_URL="https://api.deepseek.com"   # 可选
export LLM_MODEL="deepseek-chat"                  # 可选

# 编译并启动
make build
make serve
```

打开 `http://localhost:8080`

## 功能

| 模块 | 功能 |
|------|------|
| 故事广场 | 抓取知乎黑客松故事、按需 AI 解析、生成平行支线 |
| 圈子广场 | 查看「黑客松脑洞补给站」圈子实时动态 |
| 平行宇宙 | 管理所有已生成的分支线 |
| 沉浸写作 | 双栏编辑器，左侧原文右侧分支，支持手动编辑 |
| 引擎控制 | Agent 启停、手动操作、SSE 实时日志 |
| OAuth 登录 | 知乎 OAuth 2.0 授权码登录 |
| Demo 模式 | 路演沙盒，跳过授权校验 |

## 架构

```
cmd/assassin/main.go       # Go 入口
internal/
  config/                  # 配置（环境变量 + 数据库持久化）
  zhihu/                   # 知乎 API 客户端（HMAC 签名 + 限流）
  llm/                     # 通用 LLM 客户端（OpenAI 兼容）
  engine/                  # 核心引擎
    analyzer.go            # 故事发现 → LLM 解构
    generator.go           # 平行宇宙生成（两步文风克隆）
    monitor.go             # 评论情绪监听 + 关键词触发
    dispatcher.go          # 异步回复队列（1000容量 + 10QPS）
    moderate.go            # 内容审查（LLM分类 + 关键词兜底）
  model/                   # 数据模型
  store/sqlite.go          # SQLite WAL 模式持久化
  server/                  # HTTP API + SSE 推送
web/static/                # Vue 3 前端
```

## API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/status` | GET | 系统状态（故事数、分支数等） |
| `/api/stories` | GET | 故事列表（分页、搜索、状态筛选） |
| `/api/stories/:id` | GET | 故事详情 + 分支 |
| `/api/branches` | GET | 分支列表 |
| `/api/ring` | GET | 圈子实时内容 |
| `/api/action/discover` | POST | 抓取知乎故事 |
| `/api/action/analyze_one` | POST | 解析单个故事 |
| `/api/action/analyze` | POST | 批量解析待处理故事 |
| `/api/action/generate` | POST | 为指定故事生成平行支线 |
| `/api/action/publish_branches` | POST | 发布支线到圈子 |
| `/api/action/agent` | POST | Agent 启停控制 |
| `/api/oauth/authorize` | GET | 获取知乎 OAuth 授权 URL |
| `/api/oauth/callback` | GET | OAuth 回调处理 |
| `/api/oauth/user` | GET | 获取已授权用户信息 |
| `/api/settings` | GET/POST | LLM 配置读写 |
| `/api/demo` | GET/POST | Demo 模式开关 |
| `/api/events` | GET | SSE 实时推送 |

## 审查机制

双重拦截确保近现代史内容不被生成：

1. **LLM 分类** — 分析时将故事分为 `fiction`（虚构）、`real_history`（古代史）、`real_modern`（近现代史）
2. **关键词兜底** — 检测长征、红军、毛泽东等强关键词，LLM 误判时作为安全网

## Docker 部署

```bash
export ZHIHU_APP_KEY="..."
export ZHIHU_APP_SECRET="..."
export LLM_API_KEY="sk-..."
make docker-up
```

## 技术栈

Go 1.22 / Vue 3 / SQLite (WAL) / 知乎开放 API / OpenAI 兼容 LLM
