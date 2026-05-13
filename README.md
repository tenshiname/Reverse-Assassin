# 反转刺客 · Reverse Assassin

基于知乎开放 API + LLM 的互动叙事重构引擎。自动发现故事、AI 解构枢纽点、生成平行宇宙分支，并发布到知乎圈子与读者实时互动。

## 核心亮点

### 叙事状态引擎 (Narrative State Engine)

每个故事拥有持久化的角色状态、时间线和世界设定。生成分支后，系统自动从新内容中提取角色情绪变化、关系演变和新事件，回写到状态层。同一故事连续多轮生成时，角色记忆保持一致、关系持续演化、时间线不断延伸。

```
Round 1: 分析 → 提取角色(秦始皇/李斯/周继盛) → 生成3条分支 → 提取状态
Round 2: 加载状态 → 用户追问 → 基于历史状态生成 → 角色记忆延续
Round 3: 继续... Timeline 累积9+事件, 4角色状态完整追踪
```

### 世界线树 (Worldline Tree)

分支不是扁平的——每条分支都组织成一棵真正的树。根节点是原始故事，每个生成的分支都是子节点，从分支继续推进会形成更深层级。前端用 SVG 贝塞尔曲线可视化，节点按类型着色，点击即可查看内容或继续推进。

### 任意位置自由分支

不局限于 LLM 预设的枢纽点。你可以：
- 选择任意预设枢纽点作为分叉
- 输入自定义场景作为分支起点（"秦始皇在登月前夜做了个梦..."）
- 输入期望的故事走向
- 从世界线树中任意节点继续延伸

### 全链路 Context/Timeout

所有外部调用（LLM、知乎 API、OAuth）全部透传 `context.Context`，支持超时取消。LLM 180s 硬超时，外 API 15s 超时，OAuth 15s 超时。现场演示不会因一次请求卡死。

### 内容安全双重审查

1. **LLM 分类器** — 分析时将故事分为 `fiction`（虚构）/ `real_history`（古代史）/ `real_modern`（近现代史）
2. **关键词兜底** — 检测强敏感词列表，LLM 误判时作为安全网

近现代史故事自动拦截，不生成分支。

### Demo 模式零配置启动

无需 OAuth、无需知乎 Token、无需 LLM Key 即可启动。界面提示配置方式（环境变量或 Web 设置页），配置后即用。专为路演/评审场景设计。

## 功能模块

| 模块 | 说明 |
|------|------|
| 故事广场 | 从知乎黑客松内容库抓取故事、按需 AI 解析、搜索筛选 |
| 圈子广场 | 实时查看知乎圈子动态 |
| 平行宇宙 | 管理所有已生成的分支线 |
| 世界线 | SVG 树形图可视化分支结构 |
| 沉浸写作 | 原文 + 分支双栏编辑器，枢纽点选择器，自由分支输入 |
| 角色状态 | 查看每个角色的情绪/目标/记忆/关系 |
| 引擎控制 | Agent 启停、手动操作、SSE 实时日志 |
| OAuth 登录 | 知乎 OAuth 2.0 授权码登录 |
| Demo 模式 | 路演沙盒，跳过授权校验 |

## 架构

```
cmd/assassin/main.go          # Go 入口
internal/
  config/                     # 配置（DB > 环境变量 > 默认值）
  zhihu/                      # 知乎 API 客户端（HMAC-SHA256 签名 + 限流）
  llm/                        # 通用 LLM 客户端（OpenAI 兼容协议） + 所有 Prompt
  model/                      # 数据模型 + Narrative State + Worldline 类型
  engine/
    analyzer.go               # 故事发现 → LLM 解构（分类/世界观/角色/枢纽点）
    generator.go              # 平行宇宙生成（两步文风克隆） + 状态感知生成
    state.go                  # 叙事状态引擎（加载/注入/提取/回写）
    monitor.go                # 评论情绪监听 + 关键词触发
    dispatcher.go             # 异步回复队列（1000 容量 + 10 QPS）
    moderate.go               # 内容审查（LLM 分类 + 关键词兜底）
  store/sqlite.go             # SQLite WAL 持久化（故事/分支/设置/状态/世界线）
  server/
    server.go                 # HTTP API（30+ 端点）+ SSE 推送 + Agent 循环
    sse.go                    # SSE Hub 实时日志广播
web/static/index.html         # Vue 3 单文件前端
```

## API

### 故事与分支

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/status` | GET | 系统状态（故事数、分支数等） |
| `/api/stories` | GET | 故事列表（分页、搜索、状态筛选） |
| `/api/stories/:id` | GET | 故事详情 + 分支 |
| `/api/branches` | GET | 分支列表 |
| `/api/ring` | GET | 圈子实时内容 |

### 引擎操作

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/action/discover` | POST | 抓取知乎故事 |
| `/api/action/analyze_one` | POST | 解析单个故事 |
| `/api/action/analyze` | POST | 批量解析待处理故事 |
| `/api/action/generate` | POST | 生成分支（支持 `pivot_index`、`scene`、`custom_prompt`） |
| `/api/action/continue` | POST | 对故事追问，基于历史状态继续生成 |
| `/api/action/continue_branch` | POST | 从指定分支继续推进 |
| `/api/action/trigger` | POST | 触发分支生成 |
| `/api/action/scan` | POST | 扫描互动关键词 |
| `/api/action/agent` | POST | Agent 启停控制 |
| `/api/action/publish_branches` | POST | 发布支线到圈子 |

### 状态与世界线

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/states/:id` | GET | 故事状态（角色状态 + 时间线 + 枢纽点） |
| `/api/worldline/:id` | GET | 世界线树（节点 + 边） |

### 认证与配置

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/oauth/authorize` | GET | 知乎 OAuth 授权 URL |
| `/api/oauth/callback` | GET | OAuth 回调处理 |
| `/api/oauth/user` | GET | 已授权用户信息 |
| `/api/settings` | GET/POST | LLM 和知乎配置读写 |
| `/api/demo` | GET/POST | Demo 模式开关 |
| `/api/events` | GET | SSE 实时推送 |

## 数据库

SQLite WAL 模式，8 张表：

| 表 | 说明 |
|------|------|
| `stories` | 故事（含 JSON 分析结果） |
| `branches` | 平行支线 |
| `settings` | 系统配置 KV |
| `interactions` | 互动记录（去重 + 反刷屏） |
| `story_states` | 叙事状态 |
| `character_states` | 角色状态 |
| `timeline_events` | 事件时间线 |
| `worldline_nodes` | 世界线树节点 |

## 快速开始

```bash
git clone https://github.com/tenshiname/Reverse-Assassin.git
cd Reverse-Assassin

# 配置环境变量（Demo 模式可跳过，启动后在 Web 设置页配置）
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

## Docker 部署

```bash
export ZHIHU_APP_KEY="..."
export ZHIHU_APP_SECRET="..."
export LLM_API_KEY="sk-..."
make docker-up
```

## 技术栈

Go 1.25 / Vue 3 / SQLite (WAL) / 知乎开放平台 API / OpenAI 兼容 LLM

## 设计哲学

- **状态是一切的基础** — 没有状态的生成是随机游走，有状态的生成才是世界演化
- **分支是树不是列表** — 每条世界线都有它的来处和去处
- **用户是共创者** — 不限于预设枢纽点，可以从任意位置开始分支
- **演示优先** — Demo 模式零配置、全链路超时保护、双重内容安全
