# 反转刺客 · Reverse Assassin — 技术架构

基于知乎开放平台 + LLM 的互动叙事重构引擎。自动发现故事、AI 解构枢纽点、生成平行宇宙分支，在知乎圈子与读者实时互动。

## 技术栈

Go · Vue 3 · SQLite (WAL) · 知乎开放 API (HMAC-SHA256) · OAuth 2.0 · OpenAI 兼容 LLM · SSE

---

## 一、叙事状态引擎

传统 AI 故事生成每次调用独立无状态，角色记忆和世界设定在下一次生成时完全丢失。我们设计了一套完整的叙事状态系统：

```
Analyze → 提取角色(3-5个) → 初始化 StoryState
Generate → 注入状态到 Prompt → LLM 生成 → 提取新事件 → 回写
Round N → 加载累积状态 → 角色记忆/关系持续演化 → 时间线不断延伸
```

### 核心数据结构

```go
StoryState       // 世界观、当前轮次、时间线、活跃情节线
CharacterState   // 情绪、目标、记忆(最近8条)、关系图、位置、状态
TimelineEvent    // 类型、场景、参与角色、因果链、后果
```

每次生成后自动调用 LLM 从新内容中提取状态变化，回写到持久层。同一故事连续三轮生成，角色保持一致的记忆和关系。

## 二、世界线树

分支不是扁平的——每条分支组织成一棵真正的树。根节点是原始故事，每个生成的分支都是子节点，从分支继续推进会形成更深层级。

```
● 秦始皇登月计划 (depth=0)
  ├─ 黑化线 · 焚书坑儒 (depth=1, bid=1)
  │   ├─ 反转线 · 天牢中的星图 (depth=2, continue from bid=1)
  │   └─ 阴谋线 · 地宫回声 (depth=2)
  ├─ 反转线 · 李斯的回马枪 (depth=1)
  └─ 治愈线 · 扶苏归来 (depth=1)
```

前端以 Reingold-Tilford 风格算法自动布局，SVG 贝塞尔曲线连线，节点以父子关系分层排列，深度和分支数无限扩展。

## 三、多用户数据隔离

每个浏览器自动生成唯一命名空间（UUID），存储于 localStorage。所有 API 请求携带 `X-Namespace` header，服务端按命名空间创建独立 SQLite 数据库文件。

```
浏览器 A → ns_a → /data/assassin_ns_a.db
浏览器 B → ns_b → /data/assassin_ns_b.db
```

零配置、零开销——首次请求时自动创建，后续请求走内存缓存。不同用户的故事、分支、设置、API 密钥完全隔离。

## 四、全链路 Context/Timeout

所有外部调用透传 `context.Context`：

| 组件 | Timeout | 机制 |
|------|---------|------|
| LLM 生成 | 180s | http.Client Timeout |
| 知乎 API | 15s | http.Client Timeout |
| OAuth Token 交换 | 15s | context.WithTimeout |
| SSE | 心跳 30s | 自动重连 |

现场演示不会因一次请求卡死。所有 `_, _ =` 错误忽略已消除。

## 五、内容安全双重审查

1. **LLM 分类器** — 分析时将故事分为 `fiction`（虚构）/ `real_history`（古代史）/ `real_modern`（近现代史）。近现代史自动拦截，不生成分支。
2. **关键词兜底** — 14 个强敏感词检测，LLM 误判时作为安全网。

## 六、分支生成的灵活性

不局限于 LLM 预设的枢纽点：

| 方式 | 说明 |
|------|------|
| 预设枢纽点 | 选择 LLM 分析的 2-4 个关键转折点 |
| 自由场景分支 | 用户输入任意场景描述作为分支起点 |
| 自定义方向 | 用户描述期望的故事走向 |
| 分支继续推进 | 从任意已生成分支延伸新世界线 |

## 七、Demo 模式零配置

无需 OAuth、无需知乎 Token、无需 LLM API Key 即可启动。Web 设置页提供完整配置界面，配置后即用。专为路演评审设计。

## 八、Gzip + 缓存控制

所有静态文件启用 gzip 压缩，HTML 从 98KB 压缩至 23KB（76%）。全站强制 no-cache 策略。

## 数据库设计

SQLite WAL 模式，8 张表：

```
stories           — 故事（含 JSON 分析结果）
branches          — 平行支线
settings          — 用户配置 KV（API 密钥等）
interactions      — 互动记录（去重 + 反刷屏）
story_states      — 叙事状态持久化
character_states  — 角色状态持久化
timeline_events   — 事件时间线
worldline_nodes   — 世界线树节点
```

## API 设计

30+ RESTful 端点，涵盖故事发现、AI 解构、分支生成、继续推进、状态查询、世界线树、OAuth 登录、实时 SSE 推送。
