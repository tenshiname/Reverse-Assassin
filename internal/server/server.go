package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"reverse-assassin/internal/config"
	"reverse-assassin/internal/engine"
	"reverse-assassin/internal/llm"
	"reverse-assassin/internal/model"
	"reverse-assassin/internal/store"
	"reverse-assassin/internal/zhihu"
)

// Server provides HTTP API + SSE + static file service
type Server struct {
	store      *store.Store
	analyzer   *engine.Analyzer
	monitor    *engine.Monitor
	generator  *engine.Generator
	dispatcher *engine.Dispatcher
	hub        *SSEHub
	mux        *http.ServeMux

	mu      sync.Mutex
	running bool
	closed  bool
	stopCh  chan struct{}
}

func New(dbPath string) (*Server, error) {
	db, err := store.New(dbPath)
	if err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}
	// Must set provider BEFORE creating LLM client so it reads from DB
	config.SetProvider(db)

	zhihuClient := zhihu.NewClient()
	llmClient := llm.NewClient()
	s := &Server{
		store:      db,
		analyzer:   engine.NewAnalyzer(zhihuClient, llmClient, db),
		monitor:    engine.NewMonitor(zhihuClient, llmClient, db),
		generator:  engine.NewGenerator(llmClient),
		dispatcher: engine.NewDispatcher(zhihuClient, db),
		hub:        NewSSEHub(),
		mux:        http.NewServeMux(),
		stopCh:     make(chan struct{}),
	}

	// Start async reply worker for rate-limited comment posting (10 QPS)
	engine.StartReplyWorker(zhihuClient)

	s.registerRoutes()
	return s, nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.running {
			close(s.stopCh)
			s.running = false
		}
	}
	s.mu.Unlock()
	return s.store.Close()
}

func (s *Server) Handler() http.Handler {
	return corsMiddleware(loggingMiddleware(s.mux))
}

func (s *Server) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// ============================================================
// Routes
// ============================================================

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/events", s.hub.SSEHandler)
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/stories", s.handleStories)
	s.mux.HandleFunc("/api/stories/", s.handleStoryDetail)
	s.mux.HandleFunc("/api/branches", s.handleBranches)
	s.mux.HandleFunc("/api/interactions", s.handleInteractions)
	s.mux.HandleFunc("/api/action/", s.handleAction)
	s.mux.HandleFunc("/api/ring", s.handleRing)
	s.mux.HandleFunc("/api/action/publish_branches", s.handlePublishBranches)
	s.mux.HandleFunc("/api/config", s.handleConfig)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/demo", s.handleDemoMode)
	s.mux.HandleFunc("/api/login", s.handleLogin)
	s.mux.HandleFunc("/api/oauth/authorize", s.handleOAuthAuthorize)
	s.mux.HandleFunc("/api/oauth/callback", s.handleOAuthCallback)
	s.mux.HandleFunc("/api/oauth/user", s.handleOAuthUser)
	s.mux.HandleFunc("/api/oauth/logout", s.handleOAuthLogout)
	s.mux.HandleFunc("/api/logout", s.handleLogout)

	staticDir := os.Getenv("STATIC_DIR")
	if staticDir == "" {
		staticDir = "web/static"
	}
	absDir, _ := filepath.Abs(staticDir)
	s.mux.Handle("/", http.FileServer(http.Dir(absDir)))
}

// ============================================================
// API - Status
// ============================================================

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	allStories, _ := s.store.ListStoriesByStatus("")
	pending, _ := s.store.ListStoriesByStatus(model.StatusPending)
	analyzed, _ := s.store.ListStoriesByStatus(model.StatusAnalyzed)
	branched, _ := s.store.ListStoriesByStatus(model.StatusBranched)
	activePins, _ := s.store.ListActivePinIDs()

	branchCount := 0
	unlockedCount := 0
	for _, st := range allStories {
		branches, _ := s.store.GetBranchesByStory(st.WorkID)
		branchCount += len(branches)
		for _, b := range branches {
			if b.Unlocked {
				unlockedCount++
			}
		}
	}

	writeJSON(w, map[string]interface{}{
		"agent_running":     s.isRunning(),
		"total_stories":     len(allStories),
		"pending_stories":   len(pending),
		"analyzed_stories":  len(analyzed),
		"branched_stories":  len(branched),
		"active_pins":       len(activePins),
		"total_branches":    branchCount,
		"unlocked_branches": unlockedCount,
		"llm_configured":    config.LLMAPIKey() != "",
	})
}

// ============================================================
// API - Stories (with pagination, search, filter)
// ============================================================

func (s *Server) handleStories(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	search := r.URL.Query().Get("search")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	var allStories []*model.StoryRecord
	if statusFilter != "" {
		allStories, _ = s.store.ListStoriesByStatus(model.StoryStatus(statusFilter))
	} else {
		for _, st := range []model.StoryStatus{
			model.StatusPending, model.StatusAnalyzed,
			model.StatusBranched, model.StatusDispatched,
		} {
			list, _ := s.store.ListStoriesByStatus(st)
			allStories = append(allStories, list...)
		}
	}

	if search != "" {
		search = strings.ToLower(search)
		var filtered []*model.StoryRecord
		for _, st := range allStories {
			if strings.Contains(strings.ToLower(st.Title), search) ||
				strings.Contains(strings.ToLower(st.Author), search) {
				filtered = append(filtered, st)
			}
		}
		allStories = filtered
	}

	total := len(allStories)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(allStories) {
		allStories = nil
	} else if end > len(allStories) {
		allStories = allStories[start:]
	} else {
		allStories = allStories[start:end]
	}

	type storyItem struct {
		*model.StoryRecord
		BranchCount int `json:"branch_count"`
	}
	var result []storyItem
	for _, st := range allStories {
		branches, _ := s.store.GetBranchesByStory(st.WorkID)
		result = append(result, storyItem{st, len(branches)})
	}

	writeJSON(w, map[string]interface{}{
		"items":     result,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// ============================================================
// API - Story Detail
// ============================================================

func (s *Server) handleStoryDetail(w http.ResponseWriter, r *http.Request) {
	workID := strings.TrimPrefix(r.URL.Path, "/api/stories/")
	workID = strings.TrimSuffix(workID, "/")
	if workID == "" {
		writeError(w, "work_id is required", 400)
		return
	}

	story, err := s.store.GetStory(workID)
	if err != nil {
		writeError(w, "story not found", 404)
		return
	}

	branches, _ := s.store.GetBranchesByStory(workID)
	writeJSON(w, map[string]interface{}{
		"story":    story,
		"branches": branches,
	})
}

// ============================================================
// API - Branches
// ============================================================

func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	workID := r.URL.Query().Get("story_id")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	var allBranches []*model.Branch
	if workID != "" {
		allBranches, _ = s.store.GetBranchesByStory(workID)
	} else {
		allStories, _ := s.store.ListStoriesByStatus("")
		for _, st := range allStories {
			branches, _ := s.store.GetBranchesByStory(st.WorkID)
			allBranches = append(allBranches, branches...)
		}
	}

	total := len(allBranches)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(allBranches) {
		allBranches = nil
	} else if end > len(allBranches) {
		allBranches = allBranches[start:]
	} else {
		allBranches = allBranches[start:end]
	}

	type branchItem struct {
		*model.Branch
		StoryTitle string `json:"story_title,omitempty"`
	}
	var result []branchItem
	storyTitleCache := make(map[string]string)
	for _, b := range allBranches {
		title, ok := storyTitleCache[b.StoryWorkID]
		if !ok {
			st, err := s.store.GetStory(b.StoryWorkID)
			if err == nil {
				title = st.Title
				storyTitleCache[b.StoryWorkID] = title
			}
		}
		result = append(result, branchItem{b, title})
	}

	writeJSON(w, map[string]interface{}{
		"items":     result,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// ============================================================
// API - Interactions
// ============================================================

// handleRing returns ring contents with comments.
func (s *Server) handleRing(w http.ResponseWriter, r *http.Request) {
	contents, err := s.monitor.RingPins()
	if err != nil {
		writeError(w, "ring fetch failed: "+err.Error(), 500)
		return
	}
	writeJSON(w, map[string]interface{}{
		"contents": contents,
	})
}

func (s *Server) handleInteractions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"note": "Interactions stored in SQLite; see interactions table.",
	})
}

// ============================================================
// API - Actions
// ============================================================

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", 405)
		return
	}

	action := strings.TrimPrefix(r.URL.Path, "/api/action/")

	switch action {
	case "discover":
		s.hub.BroadcastLog("info", "action", "开始发现新故事...")
		n, err := s.analyzer.DiscoverAndAnalyze()
		if err != nil {
			s.hub.BroadcastLog("error", "action", "发现故事失败: "+err.Error())
			writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		s.hub.BroadcastLog("success", "action", fmt.Sprintf("发现 %d 个新故事", n))
		writeJSON(w, map[string]interface{}{"success": true, "new_stories": n})

	case "analyze":
		s.hub.BroadcastLog("info", "action", "开始分析待处理故事...")
		n, err := s.analyzer.AnalyzePendingStories()
		if err != nil {
			s.hub.BroadcastLog("error", "action", "分析失败: "+err.Error())
			writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		s.hub.BroadcastLog("success", "action", fmt.Sprintf("分析了 %d 个故事", n))
		writeJSON(w, map[string]interface{}{"success": true, "analyzed_count": n})

	case "analyze_one":
		workID := r.URL.Query().Get("work_id")
		if workID == "" {
			writeError(w, "work_id is required", 400)
			return
		}
		s.hub.BroadcastLog("info", "action", "分析单个故事: "+workID)
		if err := s.analyzer.AnalyzeOneStory(workID); err != nil {
			s.hub.BroadcastLog("error", "action", "分析失败: "+err.Error())
			writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		s.hub.BroadcastLog("success", "action", "分析完成")
		writeJSON(w, map[string]interface{}{"success": true})

	case "trigger":
		s.hub.BroadcastLog("info", "action", "开始检查分支触发条件...")
		triggerBranchingServer(s.store, s.monitor, s.generator, s.dispatcher, s.hub)
		writeJSON(w, map[string]interface{}{"success": true})

	case "generate":
		workID := r.URL.Query().Get("work_id")
		if workID == "" {
			writeError(w, "work_id is required", 400)
			return
		}
		publish := r.URL.Query().Get("publish") != "false"
		s.hub.BroadcastLog("info", "action", fmt.Sprintf("生成分支: %s (publish=%v)", workID, publish))
		branches, err := directGenerate(s.store, s.analyzer, s.generator, s.dispatcher, workID, publish, s.hub)
		if err != nil {
			s.hub.BroadcastLog("error", "action", "生成失败: "+err.Error())
			writeError(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "branches": branches})

	case "scan":
		s.hub.BroadcastLog("info", "action", "开始扫描互动关键词...")
		scanInteractionsServer(s.store, s.monitor, s.generator, s.dispatcher, s.hub)
		writeJSON(w, map[string]interface{}{"success": true})

	case "agent":
		agentAction := r.URL.Query().Get("action")
		switch agentAction {
		case "start":
			s.mu.Lock()
			if s.running {
				s.mu.Unlock()
				writeJSON(w, map[string]interface{}{"success": true, "message": "already running"})
				return
			}
			s.running = true
			s.closed = false
			s.stopCh = make(chan struct{})
			s.mu.Unlock()
			go s.runAgentLoop()
			s.hub.BroadcastLog("success", "agent", "Agent started")
			writeJSON(w, map[string]interface{}{"success": true, "message": "agent started"})

		case "stop":
			s.mu.Lock()
			if !s.running {
				s.mu.Unlock()
				writeJSON(w, map[string]interface{}{"success": true, "message": "not running"})
				return
			}
			if !s.closed {
				close(s.stopCh)
				s.closed = true
			}
			s.running = false
			s.mu.Unlock()
			s.hub.BroadcastLog("info", "agent", "Agent stopped")
			writeJSON(w, map[string]interface{}{"success": true, "message": "agent stopped"})

		default:
			writeError(w, "action must be 'start' or 'stop'", 400)
		}

	default:
		writeError(w, "unknown action: "+action, 400)
	}
}

// ============================================================
// API - Config
// ============================================================

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"llm_configured":       config.LLMAPIKey() != "",
		"llm_base_url":         config.LLMBaseURL(),
		"llm_model":            config.LLMModel(),
		"default_ring":         config.DefaultRing,
		"branch_threshold":     config.BranchTriggerThreshold,
		"monitor_interval_sec": config.MonitorInterval,
		"story_poll_interval":  config.StoryPollInterval,
		"global_qps":           config.GlobalQPS,
		"pin_per_hour":         config.PinPerHour,
	})
}

// ============================================================
// Background Agent Loop
// ============================================================

func (s *Server) runAgentLoop() {
	s.hub.BroadcastLog("info", "agent", "Agent loop started")

	s.analyzer.DiscoverAndAnalyze()
	s.analyzer.AnalyzePendingStories()
	triggerBranchingServer(s.store, s.monitor, s.generator, s.dispatcher, s.hub)

	storyTicker := time.NewTicker(time.Duration(config.StoryPollInterval) * time.Second)
	monitorTicker := time.NewTicker(time.Duration(config.MonitorInterval) * time.Second)
	interactTicker := time.NewTicker(time.Duration(config.BranchPollInterval) * time.Second)
	defer storyTicker.Stop()
	defer monitorTicker.Stop()
	defer interactTicker.Stop()

	for {
		select {
		case <-storyTicker.C:
			s.analyzer.DiscoverAndAnalyze()
			if n, _ := s.analyzer.AnalyzePendingStories(); n > 0 {
				s.hub.BroadcastLog("success", "agent", fmt.Sprintf("Discovered & analyzed %d new stories", n))
				triggerBranchingServer(s.store, s.monitor, s.generator, s.dispatcher, s.hub)
			}

		case <-monitorTicker.C:
			triggerBranchingServer(s.store, s.monitor, s.generator, s.dispatcher, s.hub)

		case <-interactTicker.C:
			scanInteractionsServer(s.store, s.monitor, s.generator, s.dispatcher, s.hub)

		case <-s.stopCh:
			s.hub.BroadcastLog("info", "agent", "Agent loop exited")
			return
		}
	}
}

// ============================================================
// Business logic (with SSE broadcast)
// ============================================================

func triggerBranchingServer(s *store.Store, m *engine.Monitor, g *engine.Generator, d *engine.Dispatcher, hub *SSEHub) {
	stories, err := s.ListStoriesByStatus(model.StatusAnalyzed)
	if err != nil || len(stories) == 0 {
		return
	}

	contents, err := m.RingPins()
	if err != nil {
		return
	}

	var allComments []model.Comment
	for _, c := range contents {
		allComments = append(allComments, c.Comments...)
	}
	if len(allComments) == 0 {
		return
	}

	sentiment, err := m.AnalyzeComments(allComments)
	if err != nil || sentiment == nil || !sentiment.ShouldBranch {
		return
	}

	hub.BroadcastLog("info", "trigger", fmt.Sprintf("Sentiment: %.2f, themes: %v",
		sentiment.SentimentScore, sentiment.RegretThemes))

	for _, story := range stories {
		if story.AnalysisResult == nil {
			continue
		}
		var bestPivot *model.PivotPoint
		var bestScore float64
		for i := range story.AnalysisResult.Pivots {
			p := &story.AnalysisResult.Pivots[i]
			if engine.ShouldTriggerBranch(sentiment, *p) {
				score := (sentiment.SentimentScore * p.RegretWeight) / maxF(p.LogicDifficulty, 0.1)
				if score > bestScore {
					bestScore = score
					bestPivot = p
				}
			}
		}
		if bestPivot == nil {
			continue
		}

		hub.BroadcastLog("success", "trigger",
			fmt.Sprintf("Branch triggered: '%s' (score: %.2f)", story.Title, bestScore))

		branches, err := g.GenerateBranches(story, *bestPivot)
		if err != nil {
			hub.BroadcastLog("error", "trigger", "Generate failed: "+err.Error())
			continue
		}

		for _, branch := range branches {
			s.InsertBranch(branch)
		}

		pinID, err := d.PublishCombined(branches, story.Title)
		if err != nil {
			hub.BroadcastLog("error", "trigger", "Publish failed: "+err.Error())
			continue
		}

		for _, branch := range branches {
			branch.PinID = pinID
			s.UpdateBranchPinID(branch.ID, pinID)
		}
		s.UpdateStoryStatus(story.WorkID, model.StatusBranched)

		hub.BroadcastLog("success", "trigger",
			fmt.Sprintf("Published %d branches, pin=%s", len(branches), pinID))
	}
}

func scanInteractionsServer(s *store.Store, m *engine.Monitor, g *engine.Generator, d *engine.Dispatcher, hub *SSEHub) {
	stories, err := s.ListStoriesByStatus(model.StatusBranched)
	if err != nil || len(stories) == 0 {
		return
	}

	pinIDs, err := s.ListActivePinIDs()
	if err != nil || len(pinIDs) == 0 {
		return
	}

	storyMap := make(map[string]*model.StoryRecord)
	for _, st := range stories {
		storyMap[st.WorkID] = st
	}

	for _, pinID := range pinIDs {
		// Pass nil keywords to get ALL unprocessed comments (keyword match done below)
		comments, err := m.ScanPinComments(pinID, nil)
		if err != nil {
			continue
		}
		for _, comment := range comments {
			if s.InteractionExists(comment.CommentID) {
				continue
			}
			for _, story := range storyMap {
				branches, _ := s.GetBranchesByStory(story.WorkID)
				for _, branch := range branches {
					if branch.Unlocked || branch.PinID != pinID {
						continue
					}
					if !strings.Contains(comment.Content, branch.Keyword) {
						continue
					}
					// Anti-spam: skip if this author already triggered this keyword
					if s.HasAuthorTriggered(comment.AuthorName, branch.Keyword) {
						hub.BroadcastLog("info", "interact",
							fmt.Sprintf("spam blocked: @%s already triggered '%s'", comment.AuthorName, branch.Keyword))
						continue
					}

					hub.BroadcastLog("success", "interact",
						fmt.Sprintf("@%s unlocked '%s' (%s)", comment.AuthorName, branch.Keyword, branch.Tag))

					fullStory, err := g.GenerateUnlockStory(branch.Keyword, story, branch)
					if err != nil {
						hub.BroadcastLog("error", "interact", "Generate unlock failed: "+err.Error())
						continue
					}
					d.UnlockAndReply(story, branch, comment, fullStory)
				}
			}
		}
	}
}

// ============================================================
// Middleware & Utilities
// ============================================================

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		d := time.Since(start)
		if d > 100*time.Millisecond || strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %v", r.Method, r.URL.Path, d)
		}
	})
}

func writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": 0,
		"msg":    "success",
		"data":   data,
	})
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": 1,
		"msg":    msg,
		"data":   nil,
	})
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// handleSettings reads/writes runtime LLM config, persisted in SQLite.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.store.GetSettingsMap()
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		if settings == nil {
			settings = make(map[string]string)
		}
		if key, ok := settings["llm_api_key"]; ok && len(key) > 8 {
			settings["llm_api_key_masked"] = key[:4] + "****" + key[len(key)-4:]
		}
		writeJSON(w, settings)

	case http.MethodPost:
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, "invalid JSON: "+err.Error(), 400)
			return
		}
		for k, v := range body {
			if err := s.store.SetSetting(k, v); err != nil {
				writeError(w, "save failed: "+err.Error(), 500)
				return
			}
		}
		s.hub.BroadcastLog("success", "config", "LLM config updated")
		writeJSON(w, map[string]interface{}{"saved": len(body)})

	default:
		writeError(w, "method not allowed", 405)
	}
}

// directGenerate generates branches for a story without requiring ring comments.
func directGenerate(s *store.Store, a *engine.Analyzer, g *engine.Generator, d *engine.Dispatcher, workID string, publish bool, hub *SSEHub) (int, error) {
	story, err := s.GetStory(workID)
	if err != nil {
		return 0, fmt.Errorf("story not found: %w", err)
	}

	// Content moderation: check LLM classification
	if story.AnalysisResult != nil {
		if blocked := engine.CheckStoryBlocked(story.AnalysisResult, story.Title, story.Content); blocked.Blocked {
			s.UpdateStoryStatus(workID, "blocked")
			return 0, fmt.Errorf("%s", blocked.Reason)
		}
	}

	// Check for existing branches to avoid duplicates
	if existing, _ := s.GetBranchesByStory(workID); len(existing) > 0 {
		hub.BroadcastLog("info", "generate", fmt.Sprintf("Story '%s' already has %d branches, skipping", story.Title, len(existing)))
		return len(existing), nil
	}

	// Analyze if not yet analyzed
	if story.AnalysisResult == nil {
		hub.BroadcastLog("info", "generate", "Analyzing story first: "+story.Title)
		if err := a.AnalyzeOneStory(workID); err != nil {
			return 0, fmt.Errorf("analyze failed: %w", err)
		}
		// Reload after analysis
		story, err = s.GetStory(workID)
		if err != nil || story.AnalysisResult == nil {
			return 0, fmt.Errorf("analysis did not produce results")
		}
	}

	// Pick the pivot point with highest regret_weight
	if len(story.AnalysisResult.Pivots) == 0 {
		return 0, fmt.Errorf("no pivot points found")
	}
	bestPivot := story.AnalysisResult.Pivots[0]
	for _, p := range story.AnalysisResult.Pivots[1:] {
		if p.RegretWeight > bestPivot.RegretWeight {
			bestPivot = p
		}
	}

	hub.BroadcastLog("success", "generate",
		fmt.Sprintf("Generating branches for '%s', pivot: %s", story.Title, trunc(bestPivot.Scene, 50)))

	branches, err := g.GenerateBranches(story, bestPivot)
	if err != nil {
		return 0, fmt.Errorf("generate: %w", err)
	}

	for _, branch := range branches {
		s.InsertBranch(branch)
	}

	if publish {
		pinID, err := d.PublishCombined(branches, story.Title)
		if err != nil {
			hub.BroadcastLog("error", "generate", "Publish failed: "+err.Error())
		} else {
			for _, branch := range branches {
				branch.PinID = pinID
				s.UpdateBranchPinID(branch.ID, pinID)
			}
			hub.BroadcastLog("success", "generate", fmt.Sprintf("Published to ring, pin=%s", pinID))
		}
	} else {
		hub.BroadcastLog("info", "generate", "Skipped publishing (user chose private)")
	}

	s.UpdateStoryStatus(story.WorkID, model.StatusBranched)
	hub.BroadcastLog("success", "generate", fmt.Sprintf("Generated %d branches for '%s'", len(branches), story.Title))
	return len(branches), nil
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func (s *Server) handlePublishBranches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", 405)
		return
	}
	workID := r.URL.Query().Get("work_id")
	if workID == "" {
		writeError(w, "work_id is required", 400)
		return
	}
	branches, err := s.store.GetBranchesByStory(workID)
	if err != nil || len(branches) == 0 {
		writeError(w, "no branches to publish", 400)
		return
	}
	story, err := s.store.GetStory(workID)
	if err != nil {
		writeError(w, "story not found", 404)
		return
	}
	pinID, err := s.dispatcher.PublishCombined(branches, story.Title)
	if err != nil {
		writeError(w, "publish failed: "+err.Error(), 500)
		return
	}
	for _, b := range branches {
		b.PinID = pinID
		s.store.UpdateBranchPinID(b.ID, pinID)
	}
	s.hub.BroadcastLog("success", "publish", fmt.Sprintf("Published %d branches for '%s', pin=%s", len(branches), story.Title, pinID))
	writeJSON(w, map[string]interface{}{"success": true, "pin_id": pinID})
}

func (s *Server) handleDemoMode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]interface{}{"demo_mode": config.GetDemoMode()})
	case http.MethodPost:
		var body struct{ Enabled bool }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, "invalid body", 400)
			return
		}
		config.SetDemoMode(body.Enabled)
		status := "DISABLED"
		if body.Enabled {
			status = "ENABLED"
		}
		s.hub.BroadcastLog("success", "demo", "Demo mode "+status)
		writeJSON(w, map[string]interface{}{"demo_mode": body.Enabled})
	default:
		writeError(w, "method not allowed", 405)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "POST required", 405)
		return
	}
	var body struct {
		ZhihuToken string `json:"zhihu_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ZhihuToken == "" {
		writeError(w, "zhihu_token is required", 400)
		return
	}
	// Store as the app_key for API calls
	s.store.SetSetting("zhihu_token", body.ZhihuToken)
	// Also update the global config's app key - but that's const...
	// For now, just store and treat login as successful
	s.hub.BroadcastLog("success", "auth", "用户已登录")
		writeJSON(w, map[string]interface{}{"logged_in": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.store.SetSetting("zhihu_token", "")
	writeJSON(w, map[string]interface{}{"logged_in": false})
}

// OAuth handlers

func (s *Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("app_id")
	if appID == "" {
		// Try stored setting
		if v, err := s.store.GetSetting("oauth_app_id"); err == nil {
			appID = v
		}
	}
	if appID == "" {
		writeError(w, "app_id is required. Set it via POST /api/settings with oauth_app_id", 400)
		return
	}
	redirectURI := fmt.Sprintf("http://%s/api/oauth/callback", r.Host)
	authURL := fmt.Sprintf("https://openapi.zhihu.com/authorize?redirect_uri=%s&app_id=%s&response_type=code",
		redirectURI, appID)
	writeJSON(w, map[string]interface{}{"auth_url": authURL})
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, "authorization code is required", 400)
		return
	}

	appID, _ := s.store.GetSetting("oauth_app_id")
	appKey, _ := s.store.GetSetting("oauth_app_key")

	resp, err := http.PostForm("https://openapi.zhihu.com/access_token", map[string][]string{
		"app_id":       {appID},
		"app_key":      {appKey},
		"grant_type":   {"authorization_code"},
		"redirect_uri": {fmt.Sprintf("http://%s/api/oauth/callback", r.Host)},
		"code":         {code},
	})
	if err != nil {
		writeError(w, "token exchange failed: "+err.Error(), 500)
		return
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		writeError(w, "decode token response: "+err.Error(), 500)
		return
	}
	if tokenResp.Error != "" {
		writeError(w, "oauth error: "+tokenResp.Error, 400)
		return
	}

	// Store the access token
	s.store.SetSetting("oauth_access_token", tokenResp.AccessToken)
	s.hub.BroadcastLog("success", "oauth", "OAuth login successful")

	// Redirect back to main page with success
	http.Redirect(w, r, "/?oauth=success", http.StatusFound)
}

func (s *Server) handleOAuthUser(w http.ResponseWriter, r *http.Request) {
	token, err := s.store.GetSetting("oauth_access_token")
	if err != nil || token == "" {
		writeError(w, "not logged in", 401)
		return
	}

	req, _ := http.NewRequest("GET", "https://openapi.zhihu.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, "fetch user failed: "+err.Error(), 500)
		return
	}
	defer resp.Body.Close()

	var userInfo map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&userInfo)
	writeJSON(w, userInfo)
}

func (s *Server) handleOAuthLogout(w http.ResponseWriter, r *http.Request) {
	s.store.SetSetting("oauth_access_token", "")
	writeJSON(w, map[string]interface{}{"logged_out": true})
}
