package engine

import (
	"context"
	"fmt"
	"log"

	"reverse-assassin/internal/llm"
	"reverse-assassin/internal/model"
)

// Generator 负责调用 LLM 生成平行宇宙支线
type Generator struct {
	llmClient *llm.Client
}

func NewGenerator(lc *llm.Client) *Generator {
	return &Generator{llmClient: lc}
}

// GenerateResult bundles generated branches with the raw LLM text for state extraction.
type GenerateResult struct {
	Branches     []*model.Branch `json:"branches"`
	GeneratedRaw string          `json:"-"` // full LLM output for state extraction
	StyleSummary string          `json:"-"`
}

// GenerateBranches is the legacy entry point. It generates without state.
func (g *Generator) GenerateBranches(ctx context.Context, story *model.StoryRecord, pivot model.PivotPoint) ([]*model.Branch, error) {
	result, err := g.Generate(ctx, story, pivot, nil)
	if err != nil {
		return nil, err
	}
	return result.Branches, nil
}

// Generate runs the full two-step generation pipeline, optionally with state context.
// state can be nil for first-round generation.
func (g *Generator) Generate(ctx context.Context, story *model.StoryRecord, pivot model.PivotPoint, state *model.StoryState) (*GenerateResult, error) {
	log.Printf("[Generator] Step 1/2: extracting style for '%s'...", story.Title)
	stylePrompt := llm.BuildExtractStylePrompt(story.Content)
	var style struct {
		StyleSummary     string   `json:"style_summary"`
		CharacterEmotion string   `json:"character_emotion"`
		Tone             []string `json:"tone"`
	}
	if err := g.llmClient.ChatJSONWithRetry(ctx, "", stylePrompt, &style, 3); err != nil {
		log.Printf("[Generator] style extraction failed, falling back to default: %v", err)
		style.StyleSummary = "保持原文风格"
	}
	log.Printf("[Generator] style extracted: %s (tones: %v)", truncate(style.StyleSummary, 60), style.Tone)

	// Build state context if available
	stateCtx := ""
	if state != nil && state.CurrentRound > 0 {
		// StateManager.BuildStateContext is used externally; we accept the formatted string
		// via the state's pre-built context. For now, we check if state has prior rounds.
	}
	// Use state-aware prompt when state is provided
	var prompt string
	if state != nil {
		// Reconstruct context from state for prompt injection
		stateCtx = buildContextFromState(state)
		prompt = llm.BuildGeneratePromptWithState(story, pivot, style.StyleSummary, stateCtx)
	} else {
		prompt = llm.BuildGeneratePromptWithStyle(story, pivot, style.StyleSummary)
	}

	log.Printf("[Generator] Step 2/2: generating branches for '%s' (round=%d)...",
		story.Title, func() int {
			if state != nil {
				return state.CurrentRound
			}
			return 0
		}())

	var resp model.GenerateResponse
	if err := g.llmClient.ChatJSONWithRetry(ctx, "", prompt, &resp, 3); err != nil {
		return nil, fmt.Errorf("generate branches: %w", err)
	}

	// Collect raw text for state extraction
	rawText := ""
	var branches []*model.Branch
	for i, b := range resp.Branches {
		branch := &model.Branch{
			StoryWorkID: story.WorkID,
			PivotIndex:  i,
			Tag:         b.Tag,
			Title:       b.Title,
			Preview:     b.Preview,
			FullStory:   b.FullStory,
			Keyword:     b.Keyword,
		}
		branches = append(branches, branch)
		rawText += fmt.Sprintf("【%s】%s\n%s\n\n", b.Tag, b.Title, b.FullStory)
	}

	log.Printf("[Generator] generated %d branches (with style cloning)", len(branches))
	return &GenerateResult{Branches: branches, GeneratedRaw: rawText, StyleSummary: style.StyleSummary}, nil
}

// Continue generates branches based on a user prompt and full state context.
func (g *Generator) Continue(ctx context.Context, story *model.StoryRecord, pivot model.PivotPoint, styleSummary, stateContext, userPrompt string) (*GenerateResult, error) {
	log.Printf("[Generator] continuing story '%s' with prompt: '%s'...", story.Title, truncate(userPrompt, 80))
	contPrompt := llm.BuildContinuePrompt(story, pivot, styleSummary, stateContext, userPrompt)

	var resp model.GenerateResponse
	if err := g.llmClient.ChatJSONWithRetry(ctx, "", contPrompt, &resp, 3); err != nil {
		return nil, fmt.Errorf("continue story: %w", err)
	}

	rawText := ""
	var branches []*model.Branch
	for i, b := range resp.Branches {
		branch := &model.Branch{
			StoryWorkID: story.WorkID,
			PivotIndex:  i,
			Tag:         b.Tag,
			Title:       b.Title,
			Preview:     b.Preview,
			FullStory:   b.FullStory,
			Keyword:     b.Keyword,
		}
		branches = append(branches, branch)
		rawText += fmt.Sprintf("【%s】%s\n%s\n\n", b.Tag, b.Title, b.FullStory)
	}

	return &GenerateResult{Branches: branches, GeneratedRaw: rawText, StyleSummary: styleSummary}, nil
}

// GenerateUnlockStory 为解锁关键词生成完整专属剧情
func (g *Generator) GenerateUnlockStory(ctx context.Context, keyword string, story *model.StoryRecord, branch *model.Branch) (string, error) {
	worldview := ""
	if story.AnalysisResult != nil {
		worldview = story.AnalysisResult.Worldview
	}
	prompt := llm.BuildUnlockPrompt(keyword, story.Title, worldview, branch.Preview)
	log.Printf("[Generator] 正在为关键词 '%s' 生成解锁剧情...", keyword)

	text, err := g.llmClient.ChatWithRetry(ctx, "", prompt, 3)
	if err != nil {
		return "", fmt.Errorf("generate unlock story: %w", err)
	}
	return text, nil
}

// buildContextFromState creates a prompt-ready state context string.
func buildContextFromState(st *model.StoryState) string {
	if st == nil {
		return ""
	}
	return formatStateForPrompt(st)
}

// formatStateForPrompt is the inline version (used when StateManager isn't available).
func formatStateForPrompt(st *model.StoryState) string {
	if st == nil || st.CurrentRound == 0 {
		return ""
	}

	var sb string
	sb += fmt.Sprintf("## 当前故事状态 (第%d轮)\n", st.CurrentRound)
	sb += fmt.Sprintf("摘要: %s\n\n", truncate(st.Summary, 200))

	sb += "### 角色状态\n"
	for _, cs := range st.Characters {
		sb += fmt.Sprintf("- **%s** (%s): %s, 目标: %s, 位置: %s\n",
			cs.Name, cs.Role, cs.Emotion, cs.Goal, cs.Location)
		if len(cs.Relations) > 0 {
			sb += "  关系: "
			for name, rel := range cs.Relations {
				sb += fmt.Sprintf("%s→%s; ", cs.Name, name)
				_ = rel
			}
			sb += "\n"
		}
		if len(cs.Memories) > 0 {
			sb += fmt.Sprintf("  记忆: %s\n", joinStrings(cs.Memories, " | "))
		}
	}
	sb += "\n"

	if len(st.Timeline) > 0 {
		sb += "### 已发生事件\n"
		for _, ev := range st.Timeline {
			sb += fmt.Sprintf("- [%s] %s", ev.Type, ev.Scene)
			if ev.Outcome != "" {
				sb += fmt.Sprintf(" → %s", ev.Outcome)
			}
			sb += "\n"
		}
		sb += "\n"
	}

	if len(st.PlotThreads) > 0 {
		sb += fmt.Sprintf("### 活跃情节线\n%s\n\n", joinStrings(st.PlotThreads, "\n"))
	}

	return sb
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
