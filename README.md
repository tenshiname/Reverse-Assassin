# ZHIFORK

基于知乎开放平台 + LLM 的互动叙事分叉引擎。自动发现故事、AI 解构枢纽点、生成平行宇宙分支，发布到知乎圈子与读者实时互动。

## 核心能力

### 叙事状态引擎

每次生成后自动提取角色情绪变化、关系演变和新事件，回写到持久化状态层。同一故事连续多轮生成时，角色记忆保持一致，关系持续演化，时间线不断延伸。

```
Round 1: 分析 → 提取角色(3-5个) → 生成3条分支 → 提取状态 → 回写
Round 2: 加载状态 → 用户追问 → 基于历史状态生成 → 角色记忆延续
Round 3: 继续... Timeline累积事件, 角色状态完整追踪
```

### 世界线树

分支组织成一棵真正的树。根节点是原始故事，每个生成的分支都是子节点，从分支继续推进会形成更深层级。前端以自动布局算法渲染 SVG 树形图，无限扩展。

### 任意位置自由分支

不局限于 LLM 预设的枢纽点。可从任意场景描述、自定义方向、或已有分支继续推进。

### 多用户数据隔离

每个浏览器自动生成唯一命名空间，服务端按命名空间创建独立 SQLite 数据库。不同用户的配置和数据完全隔离。

### 全链路超时保护

所有外部调用透传 context，LLM 180s / API 15s / OAuth 15s 超时。现场演示不会卡死。

### 内容安全

LLM 分类器 + 关键词兜底双重审查，近现代史内容自动拦截。

### Demo 模式

零凭证启动，Web 设置页提供完整配置界面。Gzip 压缩 + 缓存控制优化加载。

## 功能模块

| 模块 | 说明 |
|------|------|
| 故事广场 | 从知乎黑客松内容库抓取故事、AI 解构、搜索筛选 |
| 圈子广场 | 实时查看知乎圈子动态 |
| 平行宇宙 | 管理所有已生成的分支线 |
| 世界线 | SVG 树形图可视化分支结构 |
| 沉浸写作 | 双栏编辑器、枢纽点选择器、自由分支输入、角色状态面板 |
| 引擎控制 | Agent 启停、手动操作、SSE 实时日志 |
| OAuth 登录 | 知乎 OAuth 2.0 授权码登录 |

## 快速开始

```bash
git clone https://github.com/tenshiname/Reverse-Assassin.git
cd Reverse-Assassin

# 环境变量（Demo 模式可跳过，在 Web 设置页配置）
export ZHIHU_APP_KEY="知乎用户Token"
export ZHIHU_APP_SECRET="应用密钥"
export LLM_API_KEY="sk-..."
export LLM_BASE_URL="https://api.deepseek.com"
export LLM_MODEL="deepseek-chat"

make build
make serve
```

打开 `http://localhost:8080`

## 技术栈

Go / Vue 3 / SQLite (WAL) / 知乎开放平台 API (HMAC-SHA256) / OAuth 2.0 / OpenAI 兼容 LLM / SSE

## 架构

```
cmd/assassin/main.go       # Go 入口
internal/
  config/                  # 配置（环境变量 + 数据库）
  zhihu/                   # 知乎 API 客户端（签名 + 限流）
  llm/                     # LLM 客户端（OpenAI 兼容）
  model/                   # 数据模型 + 叙事状态 + 世界线
  engine/
    analyzer.go            # 故事发现 → LLM 解构
    generator.go           # 平行宇宙生成（两步文风克隆）
    state.go               # 叙事状态引擎
    monitor.go             # 评论情绪监听
    dispatcher.go          # 异步回复队列
    moderate.go            # 内容审查
  store/sqlite.go          # SQLite WAL 持久化（8张表）
  server/server.go         # HTTP API（30+端点）+ SSE + Agent
web/static/                # Vue 3 单文件前端
```

## API

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/status` | GET | 系统状态 |
| `/api/stories` | GET | 故事列表（分页、搜索、筛选） |
| `/api/stories/:id` | GET | 故事详情 + 分支 |
| `/api/branches` | GET | 分支列表 |
| `/api/states/:id` | GET | 叙事状态（角色 + 时间线） |
| `/api/worldline/:id` | GET | 世界线树 |
| `/api/ring` | GET | 圈子内容 |
| `/api/action/discover` | POST | 抓取故事 |
| `/api/action/analyze_one` | POST | 解析单个故事 |
| `/api/action/analyze` | POST | 批量解析 |
| `/api/action/generate` | POST | 生成分支（支持 pivot_index, scene, custom_prompt） |
| `/api/action/continue` | POST | 追问继续 |
| `/api/action/continue_branch` | POST | 分支继续推进 |
| `/api/action/agent` | POST | Agent 启停 |
| `/api/settings` | GET/POST | 配置读写 |
| `/api/oauth/authorize` | GET | OAuth 授权 |
| `/api/oauth/callback` | GET/POST | OAuth 回调 |
| `/api/oauth/user` | GET | 用户信息 |
| `/api/events` | GET | SSE 实时推送 |
