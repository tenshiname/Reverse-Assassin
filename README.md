# ZHIFORK

基于知乎开放平台 + LLM 的互动叙事分叉引擎。发现故事、AI 解构枢纽点、生成平行宇宙分支，在知乎圈子与读者互动。

## 核心能力

### 叙事状态引擎
每次生成后自动提取角色情绪、关系演变和新事件，回写到持久化状态层。同故事多轮生成时角色记忆一致，关系持续演化，时间线延伸。

### 世界线树
分支组织成真正的树结构，根节点为原始故事，分支可无限延伸。SVG 自动布局渲染。

### 任意位置自由分支
不限于预设枢纽点，可从任意场景描述、自定义方向或已有分支继续推进。

### 多用户数据隔离
浏览器自动生成唯一命名空间，服务端按命名空间创建独立数据库，数据完全隔离。

### 全链路超时保护
所有外部调用透传 context，LLM 180s / API 15s 超时。3 次指数退避重试。

### Agent 智能调度
链式循环：发现→解析→生成→扫描，30s 一轮，不再空等。

## 技术栈
Go / Vue 3 / SQLite (WAL) / 知乎开放平台 API / OAuth 2.0 / OpenAI 兼容 LLM

## 快速开始

```bash
git clone https://github.com/tenshiname/Reverse-Assassin.git
cd Reverse-Assassin

export ZHIHU_APP_KEY="知乎用户Token"
export ZHIHU_APP_SECRET="应用密钥"
export LLM_API_KEY="sk-..."

make build && make serve
```

打开 `http://localhost:8080`

## 架构

```
cmd/assassin/main.go          # Go 入口
internal/
  config/                     # 配置
  zhihu/                      # 知乎 API 客户端（HMAC-SHA256 + 限流）
  llm/                        # LLM 客户端（OpenAI 兼容 + 重试）
  model/                      # 数据模型 + 叙事状态 + 世界线
  engine/
    analyzer.go               # 故事发现 → LLM 解构
    generator.go               # 并行宇宙生成（文风克隆）
    state.go                  # 叙事状态引擎
    monitor.go                # 评论情绪监听
    dispatcher.go             # 异步回复队列
    moderate.go               # 内容审查
  store/sqlite.go             # SQLite WAL（8 张表）
  server/server.go            # HTTP API（30+ 端点）+ Agent + gzip
  server/sse.go               # 实时日志 + SSE
web/static/                   # Vue 3 前端
```

## API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/status` | GET | 系统状态 |
| `/api/stories` | GET | 故事列表 |
| `/api/stories/:id` | GET | 故事详情 |
| `/api/branches` | GET | 分支列表 |
| `/api/states/:id` | GET | 角色状态 + 时间线 |
| `/api/worldline/:id` | GET | 世界线树 |
| `/api/logs` | GET | 实时日志 |
| `/api/action/discover` | POST | 抓取故事 |
| `/api/action/analyze_one` | POST | 解析故事 |
| `/api/action/generate` | POST | 生成分支 |
| `/api/action/continue` | POST | 追问继续 |
| `/api/action/continue_branch` | POST | 分支推进 |
| `/api/action/agent` | POST | Agent 控制 |
| `/api/settings` | GET/POST | 配置读写 |
| `/api/oauth/authorize` | GET | OAuth 登录 |
| `/api/oauth/callback` | GET | OAuth 回调 |
| `/api/events` | GET | SSE |
