package engine

import (
	"fmt"
	"log"

	"reverse-assassin/internal/llm"
	"reverse-assassin/internal/model"
	"reverse-assassin/internal/store"
	"reverse-assassin/internal/zhihu"
)

// Analyzer 负责获取故事、调用 LLM 解构、提取枢纽点
type Analyzer struct {
	zhihuClient *zhihu.Client
	llmClient   *llm.Client
	store       *store.Store
}

func NewAnalyzer(zc *zhihu.Client, lc *llm.Client, s *store.Store) *Analyzer {
	return &Analyzer{
		zhihuClient: zc,
		llmClient:   lc,
		store:       s,
	}
}

// DiscoverAndAnalyze 获取新故事并分析
// 返回新发现的故事数量
func (a *Analyzer) DiscoverAndAnalyze() (int, error) {
	summaries, err := a.zhihuClient.GetStoryList()
	if err != nil {
		return 0, fmt.Errorf("get story list: %w", err)
	}

	newCount := 0
	for _, summary := range summaries {
		if a.store.StoryExists(summary.WorkID) {
			continue
		}

		log.Printf("[Analyzer] 发现新故事: %s (%s)", summary.Title, summary.WorkID)

		// 获取详情
		detail, err := a.zhihuClient.GetStoryDetail(summary.WorkID)
		if err != nil {
			log.Printf("[Analyzer] 获取故事详情失败 %s: %v", summary.WorkID, err)
			continue
		}

		// 存入数据库 (pending 状态)
		record := &model.StoryRecord{
			WorkID:  summary.WorkID,
			Title:   summary.Title,
			Author:  detail.AuthorName,
			Content: detail.Content,
			Status:  model.StatusPending,
		}
		if err := a.store.InsertStory(record); err != nil {
			log.Printf("[Analyzer] 保存故事失败: %v", err)
			continue
		}

		newCount++
	}

	log.Printf("[Analyzer] 本次发现 %d 个新故事", newCount)
	return newCount, nil
}

// AnalyzePendingStories 分析所有待处理的故事
func (a *Analyzer) AnalyzePendingStories() (int, error) {
	pending, err := a.store.ListStoriesByStatus(model.StatusPending)
	if err != nil {
		return 0, fmt.Errorf("list pending: %w", err)
	}

	analyzed := 0
	for _, story := range pending {
		log.Printf("[Analyzer] 正在分析: %s", story.Title)

		analysis, err := a.analyzeStory(story.Content)
		if err != nil {
			log.Printf("[Analyzer] 分析失败 %s: %v", story.Title, err)
			continue
		}

		// Check LLM classification for modern history
		if analysis.Classification == "real_modern" {
			log.Printf("[Analyzer] 拦截近现代史: %s", story.Title)
			a.store.UpdateStoryStatus(story.WorkID, "blocked")
			// Still save the analysis for display purposes
			story.AnalysisResult = analysis
			a.store.UpdateStoryAnalysis(story.WorkID, analysis)
			a.store.UpdateStoryStatus(story.WorkID, "blocked")
			continue
		}

		story.AnalysisResult = analysis
		if err := a.store.UpdateStoryAnalysis(story.WorkID, analysis); err != nil {
			log.Printf("[Analyzer] 保存分析结果失败: %v", err)
			continue
		}

		log.Printf("[Analyzer] 分析完成: %s [%s], 发现 %d 个枢纽点",
			story.Title, analysis.Classification, len(analysis.Pivots))
		analyzed++
	}

	return analyzed, nil
}

// AnalyzeOneStory analyzes a single story by workID.
func (a *Analyzer) AnalyzeOneStory(workID string) error {
	story, err := a.store.GetStory(workID)
	if err != nil {
		return fmt.Errorf("get story: %w", err)
	}
	analysis, err := a.analyzeStory(story.Content)
	if err != nil {
		return err
	}
	if analysis.Classification == "real_modern" {
		a.store.UpdateStoryAnalysis(workID, analysis)
		a.store.UpdateStoryStatus(workID, "blocked")
		return fmt.Errorf("内容涉及近现代真实历史，已拦截")
	}
	return a.store.UpdateStoryAnalysis(workID, analysis)
}

// analyzeStory 调用 LLM 解构故事
func (a *Analyzer) analyzeStory(content string) (*model.AnalysisResult, error) {
	prompt := llm.BuildAnalyzePrompt(content)

	var result model.AnalysisResult
	if err := a.llmClient.ChatJSON("", prompt, &result); err != nil {
		return nil, err
	}

	if len(result.Pivots) == 0 {
		return nil, fmt.Errorf("LLM 未提取到枢纽点")
	}

	return &result, nil
}
