package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"reverse-assassin/internal/model"
)

// ============================================================
// System Prompt: 故事解构器
// 输入: 故事全文, 输出: JSON(AnalysisResult)
// ============================================================
const AnalyzePrompt = `你是一个专业的叙事结构分析师。请仔细阅读以下故事，完成解构分析。

## 任务
0. **真实性判定 (classification)** — 这是最关键的判定:
   - "fiction": 完全虚构/架空/奇幻/科幻内容，不涉及真实历史
   - "real_history": 以中国古代史(1912年以前)真实事件或人物为核心的纪实内容
   - "real_modern": **必须判定为 real_modern 的情形**:
     * 核心事件涉及1912年至今的中国近现代史(如长征、抗战、建国、土改、反右、文革、改革开放等)
     * 核心人物涉及真实的近现代中国政治/军事人物(如毛泽东、周恩来等)
     * 核心组织涉及真实的近现代中国政党/军队(如红军、八路军、解放军、共产党等)
     * **关键规则: 即使以文学化/小说化手法写作，只要核心事件或核心组织是真实的近现代史内容，仍必须判定为 real_modern**
     * **有疑问时从严: 不确定是 real_history 还是 real_modern 时，优先判定为 real_modern**
   - 注意: 纯粹的古代/架空/武侠/奇幻故事应判定为 fiction

1. 提取世界观设定 (worldview): 用一段话概括故事的世界观背景
2. 提取核心人物 (characters): 列出主要角色及其特征
3. 识别关键枢纽点 (pivot_points): 找出故事中最容易引发读者遗憾或分歧的 2-4 个情节转折点

## 枢纽点要求
- scene: 描述该场景发生了什么
- regret_weight: 0-1 之间的数值，代表读者对该节点的遗憾程度（1=极度遗憾）
- logic_difficulty: 0-1 之间的数值，代表推导该节点走向的难度（1=极难预测）
- branch_potential: 用一句话描述如果该节点走向不同会产生什么可能性

## 输出格式
必须输出严格合法的 JSON，不要有任何额外文本、不要用 markdown 代码块包裹:

{
  "classification": "fiction",
  "worldview": "...",
  "characters": ["角色A: 描述", "角色B: 描述"],
  "pivot_points": [
    {
      "scene": "...",
      "regret_weight": 0.0,
      "logic_difficulty": 0.0,
      "branch_potential": "..."
    }
  ]
}

## 故事全文
`

// BuildAnalyzePrompt 构建故事分析 prompt
func BuildAnalyzePrompt(storyContent string) string {
	return AnalyzePrompt + storyContent
}

// ============================================================
// System Prompt: 文风提取器 (Step 1 of two-step generation)
// ============================================================
const ExtractStylePrompt = `你是一个专业的文学风格分析师。请分析以下故事片段，提取其写作风格特征。

## 分析维度
1. style_summary: 用一段话(80-150字)概括作者的写作风格，包括:
   - 句式特点（长短句比例、修辞手法偏好）
   - 词汇偏好（常用词汇类型、是否有特定时代/地域用语）
   - 叙事节奏（快慢交替、心理描写与动作描写的比例）
   - 情感基调（冷峻/温情/讽刺/悲壮等）
2. character_emotion: 主要角色在当前场景中的情绪状态(20-50字)
3. tone: 整体文风标签，从以下选择1-2个: [冷峻写实, 温情细腻, 华丽辞藻, 简洁白描, 黑色幽默, 史诗宏大, 市井口语, 文艺抒情]

## 输出格式 (严格JSON)
{
  "style_summary": "...",
  "character_emotion": "...",
  "tone": ["..."]
}

## 故事片段(最后1000字)
`

// BuildExtractStylePrompt builds the style extraction prompt.
func BuildExtractStylePrompt(content string) string {
	runes := []rune(content)
	if len(runes) > 1000 {
		content = string(runes[len(runes)-1000:])
	}
	return ExtractStylePrompt + content
}

// ============================================================
// System Prompt: 平行宇宙生成器 (Step 2 - with style context)
// 输入: 故事信息 + 枢纽点 + 风格摘要, 输出: JSON(GenerateResponse)
// ============================================================
const GeneratePromptWithStyle = `你是一个创意无限的平行宇宙叙事引擎。给定一个故事和一个关键枢纽点，你需要生成 3 条完全不同的平行剧情支线。

## 原始故事
标题: %s
世界观: %s
核心人物: %s

## 关键枢纽点
场景: %s
分支可能性: %s

## 原作者的写作风格（必须严格遵循）
%s

## 任务
生成 3 条平行宇宙支线。**关键要求: 严格采用上述原作者的写作风格进行创作。**
句式、词汇、节奏、情感基调必须与原作保持一致，让读者感觉是同一作者所写。

1. 每条支线有明确的标签(tag)，从以下选择:
   - 黑化线: 主角走向黑暗面
   - 反转线: 剧情发生出人意料的逆转
   - 治愈线: 温暖和解的结局
   - 悬疑线: 揭开隐藏的真相
   - 史诗线: 格局升级，更大的世界观
   - 日常线: 聚焦角色日常生活

2. 每条支线包含:
   - tag: 支线标签
   - title: 支线标题(15字以内)
   - preview: 预告片段(150-200字, 像小说简介一样吸引人)
   - full_story: 完整支线内容(500-800字)
   - keyword: 解锁关键词(2-4个字的简短中文词)

3. 三条支线应该风格迥异，展示完全不同的可能性

## 输出格式
必须输出严格合法的 JSON，不要有任何额外文本:

{
  "branches": [
    {
      "tag": "黑化线",
      "title": "...",
      "preview": "...",
      "full_story": "...",
      "keyword": "..."
    }
  ]
}`

// BuildGeneratePromptWithStyle builds the generation prompt with style context.
func BuildGeneratePromptWithStyle(story *model.StoryRecord, pivot model.PivotPoint, styleSummary string) string {
	chars := strings.Join(story.AnalysisResult.Characters, ", ")
	return fmt.Sprintf(GeneratePromptWithStyle,
		story.Title,
		story.AnalysisResult.Worldview,
		chars,
		pivot.Scene,
		pivot.BranchPotential,
		styleSummary,
	)
}

// ============================================================
// System Prompt: 平行宇宙生成器 (legacy - without style)
// ============================================================
// ============================================================
const GeneratePrompt = `你是一个创意无限的平行宇宙叙事引擎。给定一个故事和一个关键枢纽点，你需要生成 3 条完全不同的平行剧情支线。

## 原始故事
标题: %s
世界观: %s
核心人物: %s

## 关键枢纽点
场景: %s
分支可能性: %s

## 任务
生成 3 条平行宇宙支线，要求:

1. 每条支线有明确的标签(tag)，从以下选择:
   - 黑化线: 主角走向黑暗面
   - 反转线: 剧情发生出人意料的逆转
   - 治愈线: 温暖和解的结局
   - 悬疑线: 揭开隐藏的真相
   - 史诗线: 格局升级，更大的世界观
   - 日常线: 聚焦角色日常生活

2. 每条支线包含:
   - tag: 支线标签
   - title: 支线标题(15字以内)
   - preview: 预告片段(150-200字, 像小说简介一样吸引人)
   - full_story: 完整支线内容(500-800字)
   - keyword: 解锁关键词(2-4个字的简短中文词)

3. 三条支线应该风格迥异，展示完全不同的可能性

## 输出格式
必须输出严格合法的 JSON，不要有任何额外文本:

{
  "branches": [
    {
      "tag": "黑化线",
      "title": "...",
      "preview": "...",
      "full_story": "...",
      "keyword": "..."
    }
  ]
}`

// BuildGeneratePrompt 构建支线生成 prompt
func BuildGeneratePrompt(story *model.StoryRecord, pivot model.PivotPoint) string {
	chars := strings.Join(story.AnalysisResult.Characters, ", ")
	return fmt.Sprintf(GeneratePrompt,
		story.Title,
		story.AnalysisResult.Worldview,
		chars,
		pivot.Scene,
		pivot.BranchPotential,
	)
}

// ============================================================
// System Prompt: 情绪分析器
// 输入: 一批评论, 输出: 情绪分析 JSON
// ============================================================
const SentimentPrompt = `你是一个专业的评论情绪分析师。分析以下评论集合，评估其中包含的"意难平"（遗憾/不甘/如果...就好了）情绪浓度。

## 分析维度
1. sentiment_score: 0-1，评论区中遗憾/不甘情绪的整体浓度
2. regret_themes: 列举评论中反复出现的遗憾主题
3. should_branch: true/false，是否值得基于这些反馈生成平行故事

## 输出格式
严格 JSON:

{
  "sentiment_score": 0.0,
  "regret_themes": ["主题1", "主题2"],
  "should_branch": false
}

## 评论列表
`

// BuildSentimentPrompt 构建情绪分析 prompt
func BuildSentimentPrompt(comments []model.Comment) string {
	text := SentimentPrompt + "\n"
	for i, c := range comments {
		text += fmt.Sprintf("%d. [%s]: %s\n", i+1, c.AuthorName, c.Content)
	}
	return text
}

// ============================================================
// System Prompt: 专属剧情生成器
// 输入: 原故事 + 支线预览 + 解锁关键词, 输出: 完整剧情
// ============================================================
const UnlockPrompt = `你是一个创意写作引擎。下面的读者通过关键词"%s"解锁了一条平行宇宙支线。

## 原始故事
标题: %s
世界观: %s

## 支线预览
%s

## 任务
为这名读者生成这条支线的完整版本(800-1200字)，要有完整的起承转合。
直接输出故事正文，不需要 JSON 格式，不需要标题。`

// BuildUnlockPrompt 构建解锁剧情 prompt
func BuildUnlockPrompt(keyword string, storyTitle, worldview, preview string) string {
	return fmt.Sprintf(UnlockPrompt, keyword, storyTitle, worldview, preview)
}

// ============================================================
// 辅助函数: 从文本中提取 JSON (处理 markdown 代码块包裹)
// ============================================================
func extractJSON(text string) string {
	// 尝试提取 ```json ... ``` 或 ``` ... ``` 中的内容
	text = strings.TrimSpace(text)

	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimPrefix(text, "```")
		// 找结尾 ```
		if idx := strings.LastIndex(text, "```"); idx >= 0 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
	}

	// 尝试找第一个 { 到最后一个 }
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start >= 0 && end > start {
		text = text[start : end+1]
	}

	// 验证是否为合法 JSON
	var tmp interface{}
	if json.Unmarshal([]byte(text), &tmp) == nil {
		return text
	}

	return text
}
