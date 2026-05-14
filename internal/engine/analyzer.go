package engine

import (
	"context"
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
	return &Analyzer{zhihuClient: zc, llmClient: lc, store: s}
}

// resolveStore picks the namespace store from context if available, otherwise uses the default.
func (a *Analyzer) resolveStore(ctx context.Context) *store.Store {
	if st := store.StoreFromContext(ctx); st != nil {
		return st
	}
	return a.store
}

// DiscoverAndAnalyze 获取新故事并分析
func (a *Analyzer) DiscoverAndAnalyze(ctx context.Context) (int, error) {
	summaries, err := a.zhihuClient.GetStoryList(ctx)
	if err != nil {
		return 0, fmt.Errorf("get story list: %w", err)
	}

	newCount := 0
	for _, summary := range summaries {
		if a.resolveStore(ctx).StoryExists(summary.WorkID) {
			continue
		}

		log.Printf("[Analyzer] 发现新故事: %s (%s)", summary.Title, summary.WorkID)

		detail, err := a.zhihuClient.GetStoryDetail(ctx, summary.WorkID)
		if err != nil {
			log.Printf("[Analyzer] 获取故事详情失败 %s: %v", summary.WorkID, err)
			continue
		}

		record := &model.StoryRecord{
			WorkID:  summary.WorkID,
			Title:   summary.Title,
			Author:  detail.AuthorName,
			Content: detail.Content,
			Status:  model.StatusPending,
		}
		if err := a.resolveStore(ctx).InsertStory(record); err != nil {
			log.Printf("[Analyzer] 保存故事失败: %v", err)
			continue
		}

		newCount++
	}

	log.Printf("[Analyzer] 本次发现 %d 个新故事", newCount)
	return newCount, nil
}

// AnalyzePendingStories 分析所有待处理的故事
func (a *Analyzer) AnalyzePendingStories(ctx context.Context) (int, error) {
	pending, err := a.resolveStore(ctx).ListStoriesByStatus(model.StatusPending)
	if err != nil {
		return 0, fmt.Errorf("list pending: %w", err)
	}

	analyzed := 0
	for _, story := range pending {
		log.Printf("[Analyzer] 正在分析: %s", story.Title)

		analysis, err := a.analyzeStory(ctx, story.Content)
		if err != nil {
			log.Printf("[Analyzer] 分析失败 %s: %v", story.Title, err)
			continue
		}

		if analysis.Classification == "real_modern" {
			log.Printf("[Analyzer] 拦截近现代史: %s", story.Title)
			if err := a.resolveStore(ctx).UpdateStoryAnalysis(story.WorkID, analysis); err != nil {
				log.Printf("[Analyzer] 保存分析结果失败: %v", err)
			}
			a.resolveStore(ctx).UpdateStoryStatus(story.WorkID, "blocked")
			continue
		}

		if err := a.resolveStore(ctx).UpdateStoryAnalysis(story.WorkID, analysis); err != nil {
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
func (a *Analyzer) AnalyzeOneStory(ctx context.Context, workID string) error {
	story, err := a.resolveStore(ctx).GetStory(workID)
	if err != nil {
		return fmt.Errorf("get story: %w", err)
	}
	analysis, err := a.analyzeStory(ctx, story.Content)
	if err != nil {
		return err
	}
	if analysis.Classification == "real_modern" {
		a.resolveStore(ctx).UpdateStoryAnalysis(workID, analysis)
		a.resolveStore(ctx).UpdateStoryStatus(workID, "blocked")
		return fmt.Errorf("内容涉及近现代真实历史，已拦截")
	}
	return a.resolveStore(ctx).UpdateStoryAnalysis(workID, analysis)
}

func (a *Analyzer) analyzeStory(ctx context.Context, content string) (*model.AnalysisResult, error) {
	prompt := llm.BuildAnalyzePrompt(content)

	var result model.AnalysisResult
	if err := a.llmClient.ChatJSONWithRetry(ctx, "", prompt, &result, 3); err != nil {
		return nil, err
	}

	if len(result.Pivots) == 0 {
		return nil, fmt.Errorf("LLM 未提取到枢纽点")
	}

	return &result, nil
}
