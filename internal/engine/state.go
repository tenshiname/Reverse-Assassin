package engine

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"reverse-assassin/internal/llm"
	"reverse-assassin/internal/model"
	"reverse-assassin/internal/store"
)

// StateManager manages narrative state: loading, injecting into prompts, extracting from LLM output, saving.
type StateManager struct {
	store     *store.Store
	llmClient *llm.Client
}

func NewStateManager(s *store.Store, lc *llm.Client) *StateManager {
	return &StateManager{store: s, llmClient: lc}
}

// LoadOrCreate loads existing state or initializes a fresh StoryState from analysis results.
func (sm *StateManager) LoadOrCreate(workID, title string, ar *model.AnalysisResult) (*model.StoryState, error) {
	st, err := sm.store.LoadStoryState(workID)
	if err == nil {
		// Load characters and timeline
		chars, err := sm.store.LoadCharacterStates(workID)
		if err != nil {
			log.Printf("[StateManager] load chars: %v", err)
		}
		timeline, err := sm.store.LoadTimeline(workID)
		if err != nil {
			log.Printf("[StateManager] load timeline: %v", err)
		}
		st.Characters = make(map[string]*model.CharacterState)
		for _, c := range chars {
			st.Characters[c.Name] = c
		}
		st.Timeline = make([]model.TimelineEvent, len(timeline))
		for i, ev := range timeline {
			st.Timeline[i] = *ev
		}
		return st, nil
	}

	// First time: initialize from analysis
	st = &model.StoryState{
		StoryWorkID:  workID,
		StoryTitle:   title,
		Worldview:    ar.Worldview,
		CurrentRound: 0,
		Characters:   make(map[string]*model.CharacterState),
		Timeline:     []model.TimelineEvent{},
		PlotThreads:  []string{},
		Tone:         []string{},
	}

	// Parse characters from analysis: format is "Name: description"
	for _, raw := range ar.Characters {
		cs := parseCharacter(raw)
		st.Characters[cs.Name] = cs
	}

	// Record initial pivot points as timeline events
	for i, p := range ar.Pivots {
		ev := &model.TimelineEvent{
			StoryWorkID: workID,
			Round:       0,
			Sequence:    i + 1,
			Type:        "pivot",
			Scene:       p.Scene,
			Characters:  extractCharNames(ar.Characters),
		}
		st.Timeline = append(st.Timeline, *ev)
	}

	st.Summary = fmt.Sprintf("世界观: %s。关键枢纽点: %d个。角色: %s。",
		truncateStr(ar.Worldview, 100), len(ar.Pivots), strings.Join(extractCharNames(ar.Characters), "、"))

	return st, nil
}

// BuildStateContext formats the current state as a string to inject into generation prompts.
func (sm *StateManager) BuildStateContext(st *model.StoryState) string {
	if st == nil || st.CurrentRound == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 当前故事状态 (第%d轮)\n", st.CurrentRound))
	sb.WriteString(fmt.Sprintf("摘要: %s\n\n", st.Summary))

	// Character states
	sb.WriteString("### 角色状态\n")
	for _, cs := range st.Characters {
		sb.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", cs.Name, cs.Role, cs.Emotion))
		sb.WriteString(fmt.Sprintf("  目标: %s | 位置: %s | 状态: %s\n", cs.Goal, cs.Location, cs.Status))
		if len(cs.Relations) > 0 {
			sb.WriteString("  关系: ")
			first := true
			for name, rel := range cs.Relations {
				if !first {
					sb.WriteString("; ")
				}
				sb.WriteString(fmt.Sprintf("对%s: %s", name, rel))
				first = false
			}
			sb.WriteString("\n")
		}
		if len(cs.Memories) > 0 {
			sb.WriteString(fmt.Sprintf("  关键记忆: %s\n", strings.Join(cs.Memories, " | ")))
		}
	}
	sb.WriteString("\n")

	// Timeline
	if len(st.Timeline) > 0 {
		sb.WriteString("### 已发生事件\n")
		for _, ev := range st.Timeline {
			sb.WriteString(fmt.Sprintf("- [%s] %s", ev.Type, ev.Scene))
			if ev.Outcome != "" {
				sb.WriteString(fmt.Sprintf(" → %s", ev.Outcome))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// Plot threads
	if len(st.PlotThreads) > 0 {
		sb.WriteString(fmt.Sprintf("### 活跃情节线\n%s\n\n", strings.Join(st.PlotThreads, "\n")))
	}

	return sb.String()
}

// ExtractStateFromText uses LLM to extract state changes from generated branch content.
func (sm *StateManager) ExtractStateFromText(ctx context.Context, st *model.StoryState, generatedText string) (*StateChanges, error) {
	prompt := buildExtractStatePrompt(st, generatedText)

	var result StateChanges
	if err := sm.llmClient.ChatJSON(ctx, "", prompt, &result); err != nil {
		log.Printf("[StateManager] state extraction failed: %v", err)
		// Non-fatal: return empty changes
		return &StateChanges{}, nil
	}
	return &result, nil
}

// ApplyChanges writes state changes back to the StoryState and persists to DB.
func (sm *StateManager) ApplyChanges(st *model.StoryState, changes *StateChanges, branchID int64) error {
	round := st.CurrentRound + 1
	st.CurrentRound = round

	// Update character states
	for _, cu := range changes.CharacterUpdates {
		cs, ok := st.Characters[cu.Name]
		if !ok {
			// New character
			cs = &model.CharacterState{
				Name:    cu.Name,
				Role:    cu.Role,
				Traits:  cu.Traits,
				Goal:    cu.Goal,
				Status:  "alive",
				Memories: []string{},
				Relations: make(map[string]string),
			}
			st.Characters[cu.Name] = cs
		}
		if cu.Emotion != "" {
			cs.Emotion = cu.Emotion
		}
		if cu.Goal != "" {
			cs.Goal = cu.Goal
		}
		if cu.Status != "" {
			cs.Status = cu.Status
		}
		if cu.Location != "" {
			cs.Location = cu.Location
		}
		for name, rel := range cu.Relations {
			cs.Relations[name] = rel
		}
		if cu.NewMemory != "" {
			cs.Memories = append(cs.Memories, cu.NewMemory)
			if len(cs.Memories) > 8 {
				cs.Memories = cs.Memories[len(cs.Memories)-8:]
			}
		}
		if cu.ArcSummary != "" {
			cs.ArcSummary = cu.ArcSummary
		}

		if err := sm.store.SaveCharacterStateForStory(st.StoryWorkID, cs); err != nil {
			log.Printf("[StateManager] save character %s: %v", cu.Name, err)
		}
	}

	// Add timeline events
	for i, ev := range changes.NewEvents {
		te := &model.TimelineEvent{
			StoryWorkID: st.StoryWorkID,
			BranchID:    branchID,
			Round:       round,
			Sequence:    len(st.Timeline) + i + 1,
			Type:        ev.Type,
			Scene:       ev.Scene,
			Characters:  ev.Characters,
			Outcome:     ev.Outcome,
		}
		id, err := sm.store.AddTimelineEvent(te)
		if err != nil {
			log.Printf("[StateManager] add event: %v", err)
			continue
		}
		te.ID = id
		st.Timeline = append(st.Timeline, *te)
	}

	// Update plot threads
	if len(changes.NewPlotThreads) > 0 {
		st.PlotThreads = append(st.PlotThreads, changes.NewPlotThreads...)
	}

	// Update summary and tone
	if changes.Summary != "" {
		st.Summary = changes.Summary
	}
	if len(changes.Tone) > 0 {
		st.Tone = changes.Tone
	}

	return sm.store.SaveStoryState(st)
}

// StateChanges represents extracted state mutations from generated content.
type StateChanges struct {
	Summary           string             `json:"summary"`
	Tone              []string           `json:"tone"`
	CharacterUpdates  []CharacterUpdate  `json:"character_updates"`
	NewEvents         []NewEventEntry    `json:"new_events"`
	NewPlotThreads    []string           `json:"new_plot_threads"`
}

type CharacterUpdate struct {
	Name       string            `json:"name"`
	Role       string            `json:"role"`
	Traits     []string          `json:"traits"`
	Emotion    string            `json:"emotion"`
	Goal       string            `json:"goal"`
	Status     string            `json:"status"`
	Location   string            `json:"location"`
	Relations  map[string]string `json:"relations"`
	NewMemory  string            `json:"new_memory"`
	ArcSummary string            `json:"arc_summary"`
}

type NewEventEntry struct {
	Type       string   `json:"type"`
	Scene      string   `json:"scene"`
	Characters []string `json:"characters"`
	Outcome    string   `json:"outcome"`
}

// ============================================================
// Helpers
// ============================================================

func parseCharacter(raw string) *model.CharacterState {
	parts := strings.SplitN(raw, ":", 2)
	name := strings.TrimSpace(parts[0])
	desc := ""
	if len(parts) > 1 {
		desc = strings.TrimSpace(parts[1])
	}
	return &model.CharacterState{
		Name:        name,
		Role:        "supporting",
		Traits:      []string{},
		Emotion:     "neutral",
		Goal:        "",
		Status:      "alive",
		Location:    "",
		Relations:   make(map[string]string),
		Memories:    []string{},
		ArcSummary:  desc,
		LastUpdated: time.Now().Unix(),
	}
}

func extractCharNames(chars []string) []string {
	names := make([]string, 0, len(chars))
	for _, c := range chars {
		if name := strings.SplitN(c, ":", 2)[0]; strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}
	return names
}

func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// buildExtractStatePrompt builds the LLM prompt for extracting state changes.
func buildExtractStatePrompt(st *model.StoryState, generatedText string) string {
	// Summarize current state for context
	charSummaries := make([]string, 0, len(st.Characters))
	charNames := make([]string, 0, len(st.Characters))
	for _, cs := range st.Characters {
		charNames = append(charNames, cs.Name)
		charSummaries = append(charSummaries,
			fmt.Sprintf("- %s: 情绪=%s, 目标=%s, 位置=%s, 状态=%s",
				cs.Name, cs.Emotion, cs.Goal, cs.Location, cs.Status))
	}
	sort.Strings(charNames)

	timelineSummary := ""
	if len(st.Timeline) > 0 {
		events := make([]string, 0, len(st.Timeline))
		for _, ev := range st.Timeline {
			events = append(events, fmt.Sprintf("- [%s] %s", ev.Type, ev.Scene))
		}
		timelineSummary = strings.Join(events, "\n")
	}

	return fmt.Sprintf(`你是一个叙事状态提取器。给定当前故事状态和刚生成的新内容，提取状态变化。

## 当前状态 (第%d轮)
角色:
%s

已发生事件:
%s

## 新生成的内容
%s

## 任务
提取新内容带来的状态变化。严格输出JSON:

{
  "summary": "当前故事整体状态的一句话总结(100字内)",
  "tone": ["文风标签"],
  "character_updates": [
    {
      "name": "角色名",
      "role": "protagonist/antagonist/supporting",
      "traits": ["性格特征"],
      "emotion": "当前情绪",
      "goal": "当前目标",
      "status": "alive/dead/missing/transformed",
      "location": "当前位置",
      "relations": {"角色名": "关系描述"},
      "new_memory": "新增的关键记忆事件(一句话)",
      "arc_summary": "角色发展轨迹(一句话)"
    }
  ],
  "new_events": [
    {
      "type": "branch/character_action/consequence",
      "scene": "事件描述",
      "characters": ["参与角色"],
      "outcome": "事件的后果"
    }
  ],
  "new_plot_threads": ["新出现的情节线"]
}

只输出有变化的角色和事件。`, st.CurrentRound, strings.Join(charSummaries, "\n"), timelineSummary, generatedText)
}
