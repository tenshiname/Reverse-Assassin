package engine

import (
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

// GenerateBranches uses a two-step process: extract style first, then generate with style context.
func (g *Generator) GenerateBranches(story *model.StoryRecord, pivot model.PivotPoint) ([]*model.Branch, error) {
	// Step 1: Extract writing style from original story
	log.Printf("[Generator] Step 1/2: extracting style for '%s'...", story.Title)
	stylePrompt := llm.BuildExtractStylePrompt(story.Content)
	var style struct {
		StyleSummary     string   `json:"style_summary"`
		CharacterEmotion string   `json:"character_emotion"`
		Tone            []string `json:"tone"`
	}
	if err := g.llmClient.ChatJSON("", stylePrompt, &style); err != nil {
		log.Printf("[Generator] style extraction failed, falling back to default: %v", err)
		style.StyleSummary = "保持原文风格"
	}
	log.Printf("[Generator] style extracted: %s (tones: %v)", truncate(style.StyleSummary, 60), style.Tone)

	// Step 2: Generate branches with style context
	log.Printf("[Generator] Step 2/2: generating branches for '%s'...", story.Title)
	prompt := llm.BuildGeneratePromptWithStyle(story, pivot, style.StyleSummary)

	var resp model.GenerateResponse
	if err := g.llmClient.ChatJSON("", prompt, &resp); err != nil {
		return nil, fmt.Errorf("generate branches: %w", err)
	}

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
	}

	log.Printf("[Generator] generated %d branches (with style cloning)", len(branches))
	return branches, nil
}

// GenerateUnlockStory 为解锁关键词生成完整专属剧情
func (g *Generator) GenerateUnlockStory(keyword string, story *model.StoryRecord, branch *model.Branch) (string, error) {
	worldview := ""
	if story.AnalysisResult != nil {
		worldview = story.AnalysisResult.Worldview
	}
	prompt := llm.BuildUnlockPrompt(keyword, story.Title, worldview, branch.Preview)
	log.Printf("[Generator] 正在为关键词 '%s' 生成解锁剧情...", keyword)

	text, err := g.llmClient.Chat("", prompt)
	if err != nil {
		return "", fmt.Errorf("generate unlock story: %w", err)
	}
	return text, nil
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
