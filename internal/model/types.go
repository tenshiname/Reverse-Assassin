package model

// ============================================================
// 知乎 API 响应模型
// ============================================================

// 通用响应
type APIResponse struct {
	Status int         `json:"status"`
	Msg    string      `json:"msg"`
	Data   interface{} `json:"data"`
}

// 故事概要
type StorySummary struct {
	WorkID      string   `json:"work_id"`
	Title       string   `json:"title"`
	Artwork     string   `json:"artwork"`
	TabArtwork  string   `json:"tab_artwork"`
	Description string   `json:"description"`
	Labels      []string `json:"labels"`
}

// 故事详情
type StoryDetail struct {
	WorkID      string   `json:"work_id"`
	ChapterName string   `json:"chapter_name"`
	AuthorName  string   `json:"author_name"`
	AuthorAvatar string  `json:"author_avatar"`
	Labels      []string `json:"labels"`
	Introduction string  `json:"introduction"`
	Content     string   `json:"content"`
}

// 圈子信息
type RingInfo struct {
	RingID        string `json:"ring_id"`
	RingName      string `json:"ring_name"`
	RingDesc      string `json:"ring_desc"`
	RingAvatar    string `json:"ring_avatar"`
	MembershipNum int    `json:"membership_num"`
	DiscussionNum int    `json:"discussion_num"`
}

// 圈子内容 (想法)
type RingContent struct {
	PinID       int64     `json:"pin_id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	AuthorName  string    `json:"author_name"`
	Images      []string  `json:"images"`
	PublishTime int64     `json:"publish_time"`
	LikeNum     int       `json:"like_num"`
	CommentNum  int       `json:"comment_num"`
	ShareNum    int       `json:"share_num"`
	FavNum      int       `json:"fav_num"`
	Comments    []Comment `json:"comments"`
}

// 评论
type Comment struct {
	CommentID   string `json:"comment_id"`
	Content     string `json:"content"`
	AuthorName  string `json:"author_name"`
	AuthorToken string `json:"author_token"`
	LikeCount   int    `json:"like_count"`
	ReplyCount  int    `json:"reply_count"`
	ReplyTo     string `json:"reply_to,omitempty"`
	PublishTime int    `json:"publish_time"`
}

// 评论列表响应 data
type CommentListData struct {
	Comments []Comment `json:"comments"`
	HasMore  bool      `json:"has_more"`
}

// 圈子详情响应 data
type RingDetailData struct {
	RingInfo RingInfo      `json:"ring_info"`
	Contents []RingContent `json:"contents"`
}

// 发布想法成功响应
type PublishPinData struct {
	ContentToken string `json:"content_token"`
}

// 搜索条目
type SearchItem struct {
	Title           string        `json:"Title"`
	ContentType     string        `json:"ContentType"`
	ContentID       string        `json:"ContentID"`
	ContentText     string        `json:"ContentText"`
	URL             string        `json:"Url"`
	CommentCount    int           `json:"CommentCount"`
	VoteUpCount     int           `json:"VoteUpCount"`
	AuthorName      string        `json:"AuthorName"`
	AuthorAvatar    string        `json:"AuthorAvatar"`
	EditTime        int64         `json:"EditTime"`
	CommentInfoList []CommentInfo `json:"CommentInfoList"`
}

type CommentInfo struct {
	Content string `json:"Content"`
}

// ============================================================
// 内部业务模型
// ============================================================

// 故事处理状态
type StoryStatus string

const (
	StatusPending    StoryStatus = "pending"
	StatusAnalyzed   StoryStatus = "analyzed"
	StatusMonitored  StoryStatus = "monitored"
	StatusBranched   StoryStatus = "branched"
	StatusDispatched StoryStatus = "dispatched"
)

// 枢纽点 — LLM 从故事中提取的关键抉择场景
type PivotPoint struct {
	Scene           string  `json:"scene"`            // 场景描述
	RegretWeight    float64 `json:"regret_weight"`    // 遗憾权重 [0-1]
	LogicDifficulty float64 `json:"logic_difficulty"` // 逻辑推演难度 [0-1]
	BranchPotential string  `json:"branch_potential"` // 分支可能性描述
}

// 故事分析结果 — LLM 返回的结构化 JSON
type AnalysisResult struct {
	Classification string       `json:"classification"`
	Worldview      string       `json:"worldview"`
	Characters    []string      `json:"characters"`
	Pivots        []PivotPoint  `json:"pivot_points"`
}

// 分支线 — 一个平行宇宙剧情
type Branch struct {
	ID          int64  `json:"id"`
	StoryWorkID string `json:"story_work_id"`
	PivotIndex  int    `json:"pivot_index"` // 对应哪个枢纽点
	Tag         string `json:"tag"`         // 标签: 黑化/反转/治愈
	Title       string `json:"title"`
	Preview     string `json:"preview"`    // 预告片段
	FullStory   string `json:"full_story"` // 完整内容
	PinID       string `json:"pin_id"`     // 发布的想法 ID
	Keyword     string `json:"keyword"`    // 触发关键词
	Unlocked    bool   `json:"unlocked"`   // 是否已被解锁
	CreatedAt   int64  `json:"created_at"`
}

// 存储中的故事记录
type StoryRecord struct {
	WorkID         string        `json:"work_id"`
	Title          string        `json:"title"`
	Author         string        `json:"author"`
	Content        string        `json:"content"`
	Status         StoryStatus   `json:"status"`
	AnalysisResult *AnalysisResult `json:"analysis_result,omitempty"`
	CreatedAt      int64         `json:"created_at"`
	UpdatedAt      int64         `json:"updated_at"`
}

// 已处理的评论记录 (去重用)
type InteractionRecord struct {
	CommentID  string `json:"comment_id"`
	Content    string `json:"content"`
	AuthorName string `json:"author_name"`
	MatchedKey string `json:"matched_key,omitempty"`
	Processed  bool   `json:"processed"`
	CreatedAt  int64  `json:"created_at"`
}

// 支线生成请求
type GenerateRequest struct {
	StoryTitle   string      `json:"story_title"`
	StoryContent string      `json:"story_content"`
	Worldview    string      `json:"worldview"`
	Characters   []string    `json:"characters"`
	Pivot        PivotPoint  `json:"pivot"`
	Count        int         `json:"count"` // 生成支线数量, 默认 3
}

// 支线生成响应
type GenerateResponse struct {
	Branches []struct {
		Tag       string `json:"tag"`
		Title     string `json:"title"`
		Preview   string `json:"preview"`
		FullStory string `json:"full_story"`
		Keyword   string `json:"keyword"`
	} `json:"branches"`
}
