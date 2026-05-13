package model

// ============================================================
// Narrative State Engine — 让故事世界持续延伸
// ============================================================

// ============================================================
// Worldline System — 世界线树
// ============================================================

// WorldlineNode is a node in the worldline tree, representing a branching point.
type WorldlineNode struct {
	ID              int64  `json:"id"`
	StoryWorkID     string `json:"story_work_id"`
	ParentID        int64  `json:"parent_id"`        // 0 = root (original story)
	BranchID        int64  `json:"branch_id"`        // the generated branch that forks here
	BranchReason    string `json:"branch_reason"`    // why this worldline was created
	TimelineSummary string `json:"timeline_summary"` // cumulative timeline summary
	NodeTitle       string `json:"node_title"`       // short display title
	Tag             string `json:"tag"`              // branch tag (黑化线/反转线/...)
	Depth           int    `json:"depth"`            // depth in tree
	CreatedAt       int64  `json:"created_at"`
}

// WorldlineTree represents the full tree for a story.
type WorldlineTree struct {
	StoryWorkID string            `json:"story_work_id"`
	StoryTitle  string            `json:"story_title"`
	Nodes       []*WorldlineNode  `json:"nodes"`
	Edges       []WorldlineEdge   `json:"edges"`
}

type WorldlineEdge struct {
	From int64 `json:"from"` // parent node id
	To   int64 `json:"to"`   // child node id
}

// ============================================================
// Narrative State Engine — 让故事世界持续延伸
// ============================================================

// CharacterState represents a character's evolving state across generational rounds.
type CharacterState struct {
	Name        string            `json:"name"`
	Alias       string            `json:"alias"`       // 别名/称号
	Role        string            `json:"role"`        // protagonist/antagonist/supporting
	Traits      []string          `json:"traits"`      // ["机智", "多疑", "忠诚"]
	Emotion     string            `json:"emotion"`     // 当前情绪状态
	Goal        string            `json:"goal"`        // 当前目标/动机
	Relations   map[string]string `json:"relations"`   // character_name → 关系描述
	Memories    []string          `json:"memories"`    // 关键记忆事件 (保留最近8条)
	Status      string            `json:"status"`      // alive/dead/missing/transformed
	Location    string            `json:"location"`    // 当前位置
	Inventory   []string          `json:"inventory"`   // 持有物品/能力
	ArcSummary  string            `json:"arc_summary"` // 一句话描述角色发展轨迹
	LastUpdated int64             `json:"last_updated"`
}

// TimelineEvent is a single event in the story's causal timeline.
type TimelineEvent struct {
	ID          int64    `json:"id"`
	StoryWorkID string   `json:"story_work_id"`
	BranchID    int64    `json:"branch_id,omitempty"` // 0 = main timeline
	Round       int      `json:"round"`
	Sequence    int      `json:"sequence"`
	Type        string   `json:"type"` // pivot | branch | character_action | consequence | unlock
	Scene       string   `json:"scene"`
	Characters  []string `json:"characters"`
	CauseEvent  int64    `json:"cause_event,omitempty"` // ID of causal predecessor
	Outcome     string   `json:"outcome"`               // what changed because of this
	CreatedAt   int64    `json:"created_at"`
}

// StoryState is the master state for a story, tracking all characters and timeline.
type StoryState struct {
	StoryWorkID  string                     `json:"story_work_id"`
	StoryTitle   string                     `json:"story_title"`
	Worldview    string                     `json:"worldview"`
	CurrentRound int                        `json:"current_round"`
	Summary      string                     `json:"summary"`        // 当前状态摘要(≤200字)
	Timeline     []TimelineEvent            `json:"timeline"`
	Characters   map[string]*CharacterState `json:"characters"`
	PlotThreads  []string                   `json:"plot_threads"`   // 活跃的情节线
	Tone         []string                   `json:"tone"`           // 当前文风基调
	UpdatedAt    int64                      `json:"updated_at"`
}
