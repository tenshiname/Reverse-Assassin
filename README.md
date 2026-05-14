# ZHIFORK

> 每一个意难平的结局，都是新宇宙的起点。

基于知乎开放平台与 LLM 的互动叙事分叉引擎。自动发现故事，AI 解构关键枢纽点，生成平行宇宙分支，在知乎圈子与读者实时互动。不是一个简单的「AI 写故事」工具——它是一套完整的故事世界生成与演化系统。

---

## 设计哲学

传统的 AI 故事生成每次调用都是无状态的：你给它一段文字，它返回一段文字，然后双方都忘了对方。ZHIFORK 不同——它为每个故事维护一个**持久化的叙事状态**，包括角色情绪、记忆、人际关系、世界事件时间线。每次生成前注入当前状态，生成后自动提取新变化并回写。同一故事连续十轮生成，角色不会「失忆」，世界不会「重启」。

我们相信：**AI 叙事的下一个突破不是更好的文笔，而是让故事世界真正活起来。**

---

## 核心技术架构

### 1. 叙事状态引擎 (Narrative State Engine)

这是整个系统的心脏。它不是一个 prompt 技巧，而是一套完整的数据结构 + 提取/回写流水线。

```
Analyze → 提取角色(3-5个) → 初始化 StoryState
Generate → 注入状态到 Prompt → LLM 生成 → ExtractStateFromText → ApplyChanges → 回写
Round N → 加载累积的 CharacterState + TimelineEvent → 角色记忆一致 → 时间线延伸
```

**核心数据结构**：

| 结构 | 能力 |
|------|------|
| `StoryState` | 世界观、当前轮次、完整时间线、活跃情节线、文风基调 |
| `CharacterState` | 情绪、目标(动态变化)、记忆(最近8条)、关系图(角色间)、位置、存活状态、角色弧线 |
| `TimelineEvent` | 事件类型、场景描述、参与角色、因果链(cause_event)、后果(outcome) |

**提取机制**：每次生成后调用 LLM 从新内容中提取状态变化——不是靠正则，而是靠 LLM 的结构化理解。角色从「愤怒」变为「悔恨」，关系从「敌对」变为「复杂的敬重」，这些变化被精确捕获并持久化。

### 2. 世界线树 (Worldline Tree)

分支不是扁平的列表。在你面前展开的是一棵真正的树。

```
● 秦始皇登月计划 (depth=0, root)
  ├─ 黑化线 · 焚书坑儒2.0 (depth=1)
  │   ├─ 反转线 · 天牢中的星图 (depth=2, 从黑化线继续推进)
  │   └─ 阴谋线 · 地宫深处的回声 (depth=2)
  ├─ 反转线 · 李斯的回马枪 (depth=1)
  └─ 治愈线 · 扶苏归来记 (depth=1)
```

每个节点记录：`parent_id`、`branch_reason`、`timeline_summary`、`depth`。前端以 Reingold-Tilford 算法自动布局，SVG 贝塞尔曲线连接父子节点，支持无限深度和无限分支数。

从任意节点可以继续推进——选中黑化线的第 3 条子分支，输入「如果李斯此时选择背叛」，系统加载该节点之前的完整状态，生成新的延伸世界线。

### 3. 多用户数据隔离

每个浏览器打开时自动生成唯一命名空间(UUID)，存入 localStorage。所有 API 请求携带 `X-Namespace` header，服务端按命名空间创建独立 SQLite 数据库实例。不同用户的故事、分支、API 密钥、设置完全隔离。内存级缓存，首次创建后 O(1) 查找。

### 4. Agent 智能调度

不再是固定 300 秒的呆板轮询。Agent 以链式循环运行：发现 → 解析 → 生成 → 扫描，一轮完成立即进入下一轮，30 秒冷却。LLM 调用失败自动重试 3 次，指数退避(1s/2s/4s)。

### 5. 分支生成的灵活性

| 方式 | 说明 |
|------|------|
| 预设枢纽点 | LLM 自动识别的 2-4 个关键转折点，可选 |
| 自由场景 | 用户输入任意场景描述作为分支起点 |
| 自定义方向 | 用户输入期望的故事走向 |
| 分支推进 | 从任意已生成分支选择，输入方向，继续延伸 |

---

## 技术栈

| 层 | 技术 |
|------|------|
| 语言 | Go 1.25 |
| 前端 | Vue 3 (单文件 132KB, gzip 23KB) |
| 数据库 | SQLite WAL 模式, 8 张表, 命名空间隔离 |
| API 鉴权 | 知乎开放平台 HMAC-SHA256 签名 |
| 用户登录 | 知乎 OAuth 2.0 (Authorization Code Flow) |
| LLM 协议 | OpenAI 兼容 (DeepSeek / 通义千问 / GPT) |
| 实时推送 | SSE → 轮询 fallback |
| 部署 | systemd + Cloudflare Tunnel (HTTPS) |

---

## 数据库设计

```
stories           — 故事记录(含 JSON 分析结果)
branches          — 平行支线(含解锁关键词)
story_states      — 叙事状态(世界观/轮次/摘要/情节线)
character_states  — 角色状态(每个角色独立一行, JSON)
timeline_events   — 事件时间线(含因果链)
worldline_nodes   — 世界线树节点(parent_id/depth)
settings          — 用户配置 KV(API 密钥等)
interactions      — 互动记录(去重 + 反刷屏)
```

---

## API 设计

30+ RESTful 端点，涵盖故事发现、AI 解构、分支生成、继续推进、状态查询、世界线树、OAuth 登录、实时推送。

| 端点 | 说明 |
|------|------|
| `/api/status` | 系统状态(故事数/分支数/Agent 状态) |
| `/api/stories` | 故事列表(分页/搜索/状态筛选) |
| `/api/stories/:id` | 故事详情 + 分支 |
| `/api/branches` | 分支列表 |
| `/api/states/:id` | 叙事状态(角色 + 时间线 + 枢纽点) |
| `/api/worldline/:id` | 世界线树(节点 + 边) |
| `/api/logs` | 实时日志(轮询) |
| `/api/action/discover` | 抓取知乎故事 |
| `/api/action/analyze_one` | AI 解析单个故事 |
| `/api/action/generate` | 生成平行支线(支持 pivot_index/scene/custom_prompt) |
| `/api/action/continue` | 基于状态追问继续 |
| `/api/action/continue_branch` | 指定分支继续推进 |
| `/api/action/agent` | Agent 启停控制 |
| `/api/settings` | LLM/知乎配置读写 |
| `/api/oauth/authorize` | OAuth 授权 |
| `/api/oauth/callback` | OAuth 回调 |
| `/api/events` | SSE 实时推送 |

---

## 野心

这个故事引擎的最终形态不是「AI 写作助手」——它是一个**故事世界的操作系统**。

我们想做的事：
- **多故事世界线交叉**：两个不同故事的角色在某个节点相遇，世界线交汇
- **读者共创**：评论区的情绪投票驱动自动分支生成
- **Agent 自治**：完全由 AI 驱动的 24 小时故事演化，人类只是观众
- **世界线可视化**：不是树，而是一张真正的地图——每一帧都是平行宇宙的快照

如果你对「让故事世界活起来」这件事有兴趣，欢迎来聊。

---

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
