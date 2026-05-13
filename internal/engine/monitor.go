package engine

import (
	"context"
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
	return &Monitor{zhihuClient: zc, llmClient: lc, store: s}
}

// RingPins 从圈子详情中获取最新想法及其评论
func (m *Monitor) RingPins(ctx context.Context) ([]model.RingContent, error) {
	detail, err := m.zhihuClient.GetDefaultRing(ctx, 20)
	if err != nil {
		return nil, err
	}
	return detail.Contents, nil
}

// AnalyzeComments 分析一组评论的情绪
func (m *Monitor) AnalyzeComments(ctx context.Context, comments []model.Comment) (*SentimentResult, error) {
	if len(comments) == 0 {
		return &SentimentResult{SentimentScore: 0, ShouldBranch: false}, nil
	}

	prompt := llm.BuildSentimentPrompt(comments)

	var result SentimentResult
	if err := m.llmClient.ChatJSON(ctx, "", prompt, &result); err != nil {
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
func ShouldTriggerBranch(sentiment *SentimentResult, pivot model.PivotPoint) bool {
	if !sentiment.ShouldBranch {
		return false
	}

	score := (sentiment.SentimentScore * pivot.RegretWeight) / max(pivot.LogicDifficulty, 0.1)
	return score >= config.BranchTriggerThreshold
}

// ScanPinComments 扫描想法的评论区
func (m *Monitor) ScanPinComments(ctx context.Context, pinID string, keywords []string) ([]model.Comment, error) {
	comments, err := m.zhihuClient.GetCommentList(ctx, pinID, "pin", 1, 20)
	if err != nil {
		return nil, err
	}

	var matched []model.Comment
	for _, c := range comments.Comments {
		if m.store.InteractionExists(c.CommentID) {
			continue
		}

		if len(keywords) == 0 {
			matched = append(matched, c)
			continue
		}

		for _, kw := range keywords {
			if strings.Contains(c.Content, kw) {
				matched = append(matched, c)
				break
			}
		}
	}

	return matched, nil
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
