package engine

import (
	"log"
	"strings"

	"reverse-assassin/internal/config"
	"reverse-assassin/internal/llm"
	"reverse-assassin/internal/model"
	"reverse-assassin/internal/store"
	"reverse-assassin/internal/zhihu"
)

// Monitor 负责监听评论情绪，计算遗憾指数，触发分支生成
type Monitor struct {
	zhihuClient *zhihu.Client
	llmClient   *llm.Client
	store       *store.Store
}

func NewMonitor(zc *zhihu.Client, lc *llm.Client, s *store.Store) *Monitor {
	return &Monitor{
		zhihuClient: zc,
		llmClient:   lc,
		store:       s,
	}
}

// RingPins 从圈子详情中获取最新想法及其评论，存入故事的评论池
func (m *Monitor) RingPins() ([]model.RingContent, error) {
	detail, err := m.zhihuClient.GetDefaultRing(20)
	if err != nil {
		return nil, err
	}
	return detail.Contents, nil
}

// AnalyzeComments 分析一组评论的情绪
func (m *Monitor) AnalyzeComments(comments []model.Comment) (*SentimentResult, error) {
	if len(comments) == 0 {
		return &SentimentResult{SentimentScore: 0, ShouldBranch: false}, nil
	}

	prompt := llm.BuildSentimentPrompt(comments)

	var result SentimentResult
	if err := m.llmClient.ChatJSON("", prompt, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SentimentResult 情绪分析结果
type SentimentResult struct {
	SentimentScore float64  `json:"sentiment_score"`
	RegretThemes   []string `json:"regret_themes"`
	ShouldBranch   bool     `json:"should_branch"`
}

// ShouldTriggerBranch 判断是否应该触发分支生成
// 使用公式: P_branch = (sentiment * regret_weight) / logic_difficulty
func ShouldTriggerBranch(sentiment *SentimentResult, pivot model.PivotPoint) bool {
	if !sentiment.ShouldBranch {
		return false
	}

	// 综合评分: LLM 情绪分数 × 枢纽点遗憾权重 / 逻辑难度
	score := (sentiment.SentimentScore * pivot.RegretWeight) / max(pivot.LogicDifficulty, 0.1)
	return score >= config.BranchTriggerThreshold
}

// ============================================================
// 互动监听 (Step 4: 动态解锁闭环)
// ============================================================

// ScanPinComments 扫描想法的评论区
// 如果 keywords 不为空，只返回包含关键词的评论；如果为空，返回所有未处理的评论
func (m *Monitor) ScanPinComments(pinID string, keywords []string) ([]model.Comment, error) {
	comments, err := m.zhihuClient.GetCommentList(pinID, "pin", 1, 20)
	if err != nil {
		return nil, err
	}

	var matched []model.Comment
	for _, c := range comments.Comments {
		if m.store.InteractionExists(c.CommentID) {
			continue
		}

		// 无关键词过滤时返回所有未处理评论
		if len(keywords) == 0 {
			matched = append(matched, c)
			continue
		}

		// 检查是否包含关键词
		for _, kw := range keywords {
			if strings.Contains(c.Content, kw) {
				matched = append(matched, c)
				break
			}
		}
	}

	return matched, nil
}

// ScanAllActivePins 扫描所有活跃的想法，寻找关键词匹配
func (m *Monitor) ScanAllActivePins(keywordsMap map[string][]string) (map[string][]model.Comment, error) {
	pinIDs, err := m.store.ListActivePinIDs()
	if err != nil {
		return nil, err
	}

	results := make(map[string][]model.Comment)
	for _, pinID := range pinIDs {
		keywords, ok := keywordsMap[pinID]
		if !ok {
			continue
		}
		matched, err := m.ScanPinComments(pinID, keywords)
		if err != nil {
			log.Printf("[Monitor] 扫描想法 %s 评论失败: %v", pinID, err)
			continue
		}
		if len(matched) > 0 {
			results[pinID] = matched
		}
	}

	return results, nil
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
