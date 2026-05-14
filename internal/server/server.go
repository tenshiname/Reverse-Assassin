package server

import (
	"context"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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
type contextKey string
const (
	namespaceKey contextKey = "namespace"
)

type Server struct {
	store      *store.Store
	llmClient  *llm.Client
	stores     map[string]*store.Store
	storesMu   sync.RWMutex
	dbBasePath string
	analyzer   *engine.Analyzer
	monitor    *engine.Monitor
	generator  *engine.Generator
	dispatcher *engine.Dispatcher
	stateMgr   *engine.StateManager
	hub        *SSEHub
	mux        *http.ServeMux

	mu      sync.Mutex
	running bool
	closed  bool
	stopCh  chan struct{}
}

func getNamespace(r *http.Request) string {
	if ns, ok := r.Context().Value(namespaceKey).(string); ok && ns != "" {
		return ns
	}
	return "default"
}

func New(dbPath string) (*Server, error) {
	// Open default store for startup
	db, err := store.New(dbPath)
	if err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}
	config.SetProvider(db)
	// Derive base path for namespace DBs
	basePath := strings.TrimSuffix(dbPath, ".db")
	if !strings.HasSuffix(dbPath, ".db") {
		basePath = filepath.Join(filepath.Dir(dbPath), "ns")
	}

	zhihuClient := zhihu.NewClient()
	llmClient := llm.NewClient()
	s := &Server{
		store:      db,
		llmClient:  llmClient,
		stores:     make(map[string]*store.Store),
		dbBasePath: basePath,
		analyzer:   engine.NewAnalyzer(zhihuClient, llmClient, db),
		monitor:    engine.NewMonitor(zhihuClient, llmClient, db),
		generator:  engine.NewGenerator(llmClient),
		dispatcher: engine.NewDispatcher(zhihuClient, db),
		stateMgr:   engine.NewStateManager(db, llmClient),
		hub:        NewSSEHub(),
		mux:        http.NewServeMux(),
		stopCh:     make(chan struct{}),
	}

	// Start async reply worker for rate-limited comment posting (10 QPS)
	engine.StartReplyWorker(zhihuClient)

	s.registerRoutes()
	return s, nil
}

// getStore returns the store for the current request'''s namespace.
func (s *Server) getStore(r *http.Request) *store.Store {
	ns := getNamespace(r)
	if ns == "default" {
		return s.store
	}
	s.storesMu.RLock()
	st, ok := s.stores[ns]
	s.storesMu.RUnlock()
	if ok {
		return st
	}
	s.storesMu.Lock()
	defer s.storesMu.Unlock()
	// Double-check
	if st, ok = s.stores[ns]; ok {
		return st
	}
	dbPath := fmt.Sprintf("%s_%s.db", s.dbBasePath, ns)
	var err error
	st, err = store.New(dbPath)
	if err != nil {
		log.Printf("failed to create namespace store %s: %v", ns, err)
		return s.store
	}
	s.stores[ns] = st
	return st
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
	s.storesMu.Lock()
	for _, st := range s.stores {
		st.Close()
	}
	s.storesMu.Unlock()
	return s.store.Close()
}

func (s *Server) Handler() http.Handler {
	return corsMiddleware(gzipMiddleware(s.namespaceMiddleware(loggingMiddleware(s.mux))))
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
	staticDir := os.Getenv("STATIC_DIR")
	if staticDir == "" {
		staticDir = "web/static"
	}
	absDir, err := filepath.Abs(staticDir)
	if err != nil {
		log.Printf("filepath.Abs(%s): %v, using staticDir as-is", staticDir, err)
		absDir = staticDir
	}

	s.mux.HandleFunc("/api/events", s.hub.SSEHandler)
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/states/", s.handleState)
	s.mux.HandleFunc("/api/worldline/", s.handleWorldline)
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
	s.mux.HandleFunc("/auth/callback", s.handleOAuthCallback)
	// /oauth/callback serves the JS bridge page to extract code from fragment
	s.mux.HandleFunc("/oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(absDir, "oauth-callback.html"))
	})
	s.mux.HandleFunc("/api/oauth/user", s.handleOAuthUser)
	s.mux.HandleFunc("/api/oauth/logout", s.handleOAuthLogout)
	s.mux.HandleFunc("/api/logout", s.handleLogout)

	noCacheFS := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		http.FileServer(http.Dir(absDir)).ServeHTTP(w, r)
	})
	s.mux.Handle("/", noCacheFS)
}

// ============================================================
// API - Status
// ============================================================

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	allStories, err := s.getStore(r).ListStoriesByStatus("")
	if err != nil {
		log.Printf("[status] list stories: %v", err)
	}
	pending, err := s.getStore(r).ListStoriesByStatus(model.StatusPending)
	if err != nil {
		log.Printf("[status] list pending: %v", err)
	}
	analyzed, err := s.getStore(r).ListStoriesByStatus(model.StatusAnalyzed)
	if err != nil {
		log.Printf("[status] list analyzed: %v", err)
	}
	branched, err := s.getStore(r).ListStoriesByStatus(model.StatusBranched)
	if err != nil {
		log.Printf("[status] list branched: %v", err)
	}
	activePins, err := s.getStore(r).ListActivePinIDs()
	if err != nil {
		log.Printf("[status] list active pins: %v", err)
	}

	branchCount := 0
	unlockedCount := 0
	for _, st := range allStories {
		branches, err := s.getStore(r).GetBranchesByStory(st.WorkID)
		if err != nil {
			log.Printf("[status] get branches for %s: %v", st.WorkID, err)
			continue
		}
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
// API - Worldline tree
func (s *Server) handleWorldline(w http.ResponseWriter, r *http.Request) {
	workID := strings.TrimPrefix(r.URL.Path, "/api/worldline/")
	workID = strings.TrimSuffix(workID, "/")
	if workID == "" {
		writeError(w, "work_id is required", 400)
		return
	}

	story, err := s.getStore(r).GetStory(workID)
	if err != nil {
		writeError(w, "story not found", 404)
		return
	}

	nodes, err := s.getStore(r).GetWorldlineTree(workID)
	if err != nil {
		writeError(w, "load worldline: "+err.Error(), 500)
		return
	}

	// Build edges from parent_id relationships
	var edges []map[string]int64
	for _, n := range nodes {
		if n.ParentID > 0 {
			edges = append(edges, map[string]int64{"from": n.ParentID, "to": n.ID})
		}
	}

	writeJSON(w, map[string]interface{}{
		"story_work_id": story.WorkID,
		"story_title":   story.Title,
		"nodes":         nodes,
		"edges":         edges,
	})
}

// API - State (character states + timeline for a story)
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	workID := strings.TrimPrefix(r.URL.Path, "/api/states/")
	workID = strings.TrimSuffix(workID, "/")
	if workID == "" {
		writeError(w, "work_id is required", 400)
		return
	}

	story, err := s.getStore(r).GetStory(workID)
	if err != nil {
		writeError(w, "story not found", 404)
		return
	}
	if story.AnalysisResult == nil {
		writeJSON(w, map[string]interface{}{
			"story_work_id": story.WorkID,
			"story_title":   story.Title,
			"current_round": 0,
			"worldview":     "",
			"summary":       "",
			"characters":    []interface{}{},
			"timeline":      []interface{}{},
			"pivots":        []interface{}{},
			"plot_threads":  []interface{}{},
		})
		return
	}

	state, err := s.stateMgr.LoadOrCreate(workID, story.Title, story.AnalysisResult)
	if err != nil {
		writeError(w, "load state: "+err.Error(), 500)
		return
	}

	// Format character states for display
	type charInfo struct {
		Name       string            `json:"name"`
		Role       string            `json:"role"`
		Traits     []string          `json:"traits"`
		Emotion    string            `json:"emotion"`
		Goal       string            `json:"goal"`
		Status     string            `json:"status"`
		Location   string            `json:"location"`
		Relations  map[string]string `json:"relations"`
		Memories   []string          `json:"memories"`
		ArcSummary string            `json:"arc_summary"`
	}
	var chars []charInfo
	for _, cs := range state.Characters {
		chars = append(chars, charInfo{
			Name:       cs.Name,
			Role:       cs.Role,
			Traits:     cs.Traits,
			Emotion:    cs.Emotion,
			Goal:       cs.Goal,
			Status:     cs.Status,
			Location:   cs.Location,
			Relations:  cs.Relations,
			Memories:   cs.Memories,
			ArcSummary: cs.ArcSummary,
		})
	}

	// Format timeline
	type evInfo struct {
		ID         int64    `json:"id"`
		Round      int      `json:"round"`
		Sequence   int      `json:"sequence"`
		Type       string   `json:"type"`
		Scene      string   `json:"scene"`
		Characters []string `json:"characters"`
		Outcome    string   `json:"outcome"`
	}
	var events []evInfo
	for _, ev := range state.Timeline {
		events = append(events, evInfo{
			ID: ev.ID, Round: ev.Round, Sequence: ev.Sequence,
			Type: ev.Type, Scene: ev.Scene,
			Characters: ev.Characters, Outcome: ev.Outcome,
		})
	}

	var pivots []model.PivotPoint
	if story.AnalysisResult != nil {
		pivots = story.AnalysisResult.Pivots
	}

	writeJSON(w, map[string]interface{}{
		"story_title":   state.StoryTitle,
		"worldview":     state.Worldview,
		"current_round": state.CurrentRound,
		"summary":       state.Summary,
		"characters":    chars,
		"timeline":      events,
		"plot_threads":  state.PlotThreads,
		"pivots":        pivots,
	})
}

// API - Stories (with pagination, search, filter)

// API - Stories (with pagination, search, filter)
// ============================================================

func (s *Server) handleStories(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	search := r.URL.Query().Get("search")
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil {
		page = 0
	}
	pageSize, err := strconv.Atoi(r.URL.Query().Get("page_size"))
	if err != nil {
		pageSize = 0
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	var allStories []*model.StoryRecord
	if statusFilter != "" {
		allStories, err = s.getStore(r).ListStoriesByStatus(model.StoryStatus(statusFilter))
		if err != nil {
			log.Printf("[stories] list by status: %v", err)
		}
	} else {
		for _, st := range []model.StoryStatus{
			model.StatusPending, model.StatusAnalyzed,
			model.StatusBranched, model.StatusDispatched,
		} {
			list, err := s.getStore(r).ListStoriesByStatus(st)
			if err != nil {
				log.Printf("[stories] list %s: %v", st, err)
				continue
			}
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
		branches, err := s.getStore(r).GetBranchesByStory(st.WorkID)
		if err != nil {
			log.Printf("[stories] get branches for %s: %v", st.WorkID, err)
		}
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

	story, err := s.getStore(r).GetStory(workID)
	if err != nil {
		writeError(w, "story not found", 404)
		return
	}

	branches, err := s.getStore(r).GetBranchesByStory(workID)
	if err != nil {
		log.Printf("[story-detail] get branches for %s: %v", workID, err)
	}
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
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil {
		page = 0
	}
	pageSize, err := strconv.Atoi(r.URL.Query().Get("page_size"))
	if err != nil {
		pageSize = 0
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	var allBranches []*model.Branch
	if workID != "" {
		allBranches, err = s.getStore(r).GetBranchesByStory(workID)
		if err != nil {
			log.Printf("[branches] get by story: %v", err)
		}
	} else {
		allStories, err := s.getStore(r).ListStoriesByStatus("")
		if err != nil {
			log.Printf("[branches] list all stories: %v", err)
		} else {
			for _, st := range allStories {
				branches, err := s.getStore(r).GetBranchesByStory(st.WorkID)
				if err != nil {
					log.Printf("[branches] get branches for %s: %v", st.WorkID, err)
					continue
				}
				allBranches = append(allBranches, branches...)
			}
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
			st, err := s.getStore(r).GetStory(b.StoryWorkID)
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
	contents, err := s.monitor.RingPins(r.Context())
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
		n, err := s.analyzer.DiscoverAndAnalyze(r.Context())
		if err != nil {
			s.hub.BroadcastLog("error", "action", "发现故事失败: "+err.Error())
			writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		s.hub.BroadcastLog("success", "action", fmt.Sprintf("发现 %d 个新故事", n))
		writeJSON(w, map[string]interface{}{"success": true, "new_stories": n})

	case "analyze":
		s.hub.BroadcastLog("info", "action", "开始分析待处理故事...")
		n, err := s.analyzer.AnalyzePendingStories(r.Context())
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
		if err := s.analyzer.AnalyzeOneStory(r.Context(), workID); err != nil {
			s.hub.BroadcastLog("error", "action", "分析失败: "+err.Error())
			writeJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}
		s.hub.BroadcastLog("success", "action", "分析完成")
		writeJSON(w, map[string]interface{}{"success": true})

	case "trigger":
		s.hub.BroadcastLog("info", "action", "开始检查分支触发条件...")
		triggerBranchingServer(r.Context(), s.getStore(r), s.monitor, s.generator, s.dispatcher, s.hub)
		writeJSON(w, map[string]interface{}{"success": true})

	case "generate":
		workID := r.URL.Query().Get("work_id")
		if workID == "" {
			writeError(w, "work_id is required", 400)
			return
		}
		publish := r.URL.Query().Get("publish") != "false"
		pivotIdx := -1
		if pi := r.URL.Query().Get("pivot_index"); pi != "" {
			if n, err := strconv.Atoi(pi); err == nil {
				pivotIdx = n
			}
		}
		var body struct {
			CustomPrompt string `json:"custom_prompt"`
			Scene        string `json:"scene"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		s.hub.BroadcastLog("info", "action", fmt.Sprintf("生成分支: %s (publish=%v, pivot=%d, custom=%v, scene=%v)", workID, publish, pivotIdx, body.CustomPrompt != "", body.Scene != ""))
		branches, err := directGenerate(r.Context(), s.getStore(r), s.analyzer, s.generator, s.dispatcher, s.stateMgr, workID, publish, pivotIdx, body.CustomPrompt, body.Scene, s.hub)
		if err != nil {
			s.hub.BroadcastLog("error", "action", "生成失败: "+err.Error())
			writeError(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "branches": branches})

	case "scan":
		s.hub.BroadcastLog("info", "action", "开始扫描互动关键词...")
		scanInteractionsServer(r.Context(), s.getStore(r), s.monitor, s.generator, s.dispatcher, s.hub)
		writeJSON(w, map[string]interface{}{"success": true})

	case "continue":
		workID := r.URL.Query().Get("work_id")
		pivotIdx := -1
		if pi := r.URL.Query().Get("pivot_index"); pi != "" {
			if n, err := strconv.Atoi(pi); err == nil {
				pivotIdx = n
			}
		}
		if workID == "" {
			writeError(w, "work_id is required", 400)
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
			writeError(w, "prompt is required in JSON body", 400)
			return
		}
		s.hub.BroadcastLog("info", "action", fmt.Sprintf("继续生成: %s, prompt: %s", workID, trunc(body.Prompt, 50)))
		branches, err := continueStory(r.Context(), s.getStore(r), s.generator, s.stateMgr, workID, body.Prompt, pivotIdx, s.hub)
		if err != nil {
			s.hub.BroadcastLog("error", "action", "继续生成失败: "+err.Error())
			writeError(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "branches": branches})

	case "continue_branch":
		branchIDStr := r.URL.Query().Get("branch_id")
		if branchIDStr == "" {
			writeError(w, "branch_id is required", 400)
			return
		}
		branchID, err := strconv.ParseInt(branchIDStr, 10, 64)
		if err != nil {
			writeError(w, "invalid branch_id", 400)
			return
		}
		var body struct {
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Prompt == "" {
			writeError(w, "prompt is required in JSON body", 400)
			return
		}
		s.hub.BroadcastLog("info", "action", fmt.Sprintf("继续分支 %d: %s", branchID, trunc(body.Prompt, 50)))
		newBranches, err := continueBranch(r.Context(), s.getStore(r), s.llmClient, s.stateMgr, branchID, body.Prompt, s.hub)
		if err != nil {
			s.hub.BroadcastLog("error", "action", "继续分支失败: "+err.Error())
			writeError(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]interface{}{"success": true, "branches": newBranches})

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
		"llm_configured":        config.LLMAPIKey() != "",
		"llm_base_url":          config.LLMBaseURL(),
		"llm_model":             config.LLMModel(),
		"zhihu_configured":      config.AppKey() != "",
		"default_ring":          config.DefaultRing,
		"branch_threshold":      config.BranchTriggerThreshold,
		"monitor_interval_sec":  config.MonitorInterval,
		"story_poll_interval":   config.StoryPollInterval,
		"global_qps":            config.GlobalQPS,
		"pin_per_hour":          config.PinPerHour,
		"demo_mode":             config.GetDemoMode(),
	})
}

// ============================================================
// Background Agent Loop
// ============================================================

func (s *Server) runAgentLoop() {
	s.hub.BroadcastLog("info", "agent", "Agent loop started (chained scheduling)")

	// Initial full cycle
	s.runAgentCycle(context.Background())

	// Continuous chained cycles with cooldown
	cooldown := 30 * time.Second
	timer := time.NewTimer(cooldown)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			s.runAgentCycle(context.Background())
			timer.Reset(cooldown)

		case <-s.stopCh:
			s.hub.BroadcastLog("info", "agent", "Agent loop exited")
			return
		}
	}
}

// runAgentCycle runs one complete agent cycle: discover → analyze → generate → scan
func (s *Server) runAgentCycle(ctx context.Context) {
	// Step 1: Discover new stories
	if n, err := s.analyzer.DiscoverAndAnalyze(ctx); err != nil {
		s.hub.BroadcastLog("error", "agent", "Discover: "+err.Error())
	} else if n > 0 {
		s.hub.BroadcastLog("success", "agent", fmt.Sprintf("Discovered %d new stories", n))
	}

	// Step 2: Analyze pending stories
	if n, err := s.analyzer.AnalyzePendingStories(ctx); err != nil {
		s.hub.BroadcastLog("error", "agent", "Analyze: "+err.Error())
	} else if n > 0 {
		s.hub.BroadcastLog("success", "agent", fmt.Sprintf("Analyzed %d stories", n))
	}

	// Step 3: Trigger branch generation for analyzed stories
	triggerBranchingServer(ctx, s.store, s.monitor, s.generator, s.dispatcher, s.hub)

	// Step 4: Scan interactions for keyword unlocks
	scanInteractionsServer(ctx, s.store, s.monitor, s.generator, s.dispatcher, s.hub)
}

// ============================================================
// Business logic (with SSE broadcast)
// ============================================================

func triggerBranchingServer(ctx context.Context, s *store.Store, m *engine.Monitor, g *engine.Generator, d *engine.Dispatcher, hub *SSEHub) {
	stories, err := s.ListStoriesByStatus(model.StatusAnalyzed)
	if err != nil || len(stories) == 0 {
		return
	}

	contents, err := m.RingPins(ctx)
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

	sentiment, err := m.AnalyzeComments(ctx, allComments)
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

		branches, err := g.GenerateBranches(ctx, story, *bestPivot)
		if err != nil {
			hub.BroadcastLog("error", "trigger", "Generate failed: "+err.Error())
			continue
		}

		for _, branch := range branches {
			id, err := s.InsertBranch(branch)
			if err != nil {
				hub.BroadcastLog("error", "trigger", "Insert branch: "+err.Error())
				continue
			}
			branch.ID = id
		}

		pinID, err := d.PublishCombined(ctx, branches, story.Title)
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

func scanInteractionsServer(ctx context.Context, s *store.Store, m *engine.Monitor, g *engine.Generator, d *engine.Dispatcher, hub *SSEHub) {
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
		comments, err := m.ScanPinComments(ctx, pinID, nil)
		if err != nil {
			continue
		}
		for _, comment := range comments {
			if s.InteractionExists(comment.CommentID) {
				continue
			}
			for _, story := range storyMap {
				branches, err := s.GetBranchesByStory(story.WorkID)
				if err != nil {
					log.Printf("[scan] get branches for %s: %v", story.WorkID, err)
					continue
				}
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

					fullStory, err := g.GenerateUnlockStory(ctx, branch.Keyword, story, branch)
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

func (s *Server) namespaceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ns := r.Header.Get("X-Namespace")
		if ns == "" {
			ns = "default"
		}
		nr := r.WithContext(context.WithValue(r.Context(), namespaceKey, ns))
		st := s.getStore(nr)
		ctx := context.WithValue(nr.Context(), store.ContextKey("store"), st)
		config.SetProvider(st)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip gzip for SSE
		if strings.HasPrefix(r.URL.Path, "/api/events") {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		gzw := &gzipResponseWriter{Writer: gz, ResponseWriter: w}
		next.ServeHTTP(gzw, r)
	})
}

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
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
		settings, err := s.getStore(r).GetSettingsMap()
		if err != nil {
			writeError(w, err.Error(), 500)
			return
		}
		if settings == nil {
			settings = make(map[string]string)
		}
		if key, ok := settings["llm_api_key"]; ok && len(key) > 8 {
			settings["llm_api_key_masked"] = key[:4] + "****" + key[len(key)-4:]
			delete(settings, "llm_api_key")
		}
		if tok, ok := settings["zhihu_token"]; ok && len(tok) > 4 {
			settings["zhihu_token_masked"] = tok[:2] + "****"
			delete(settings, "zhihu_token")
		}
		if sec, ok := settings["zhihu_secret"]; ok && len(sec) > 4 {
			settings["zhihu_secret_masked"] = sec[:2] + "****"
			delete(settings, "zhihu_secret")
		}
		writeJSON(w, settings)

	case http.MethodPost:
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, "invalid JSON: "+err.Error(), 400)
			return
		}
		for k, v := range body {
			if err := s.getStore(r).SetSetting(k, v); err != nil {
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
func directGenerate(ctx context.Context, s *store.Store, a *engine.Analyzer, g *engine.Generator, d *engine.Dispatcher, sm *engine.StateManager, workID string, publish bool, pivotIdx int, customPrompt, customScene string, hub *SSEHub) (int, error) {
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
	if existing, err := s.GetBranchesByStory(workID); err == nil && len(existing) > 0 {
		hub.BroadcastLog("info", "generate", fmt.Sprintf("Story '%s' already has %d branches, skipping", story.Title, len(existing)))
		return len(existing), nil
	}

	// Analyze if not yet analyzed
	if story.AnalysisResult == nil {
		hub.BroadcastLog("info", "generate", "Analyzing story first: "+story.Title)
		if err := a.AnalyzeOneStory(ctx, workID); err != nil {
			return 0, fmt.Errorf("analyze failed: %w", err)
		}
		story, err = s.GetStory(workID)
		if err != nil || story.AnalysisResult == nil {
			return 0, fmt.Errorf("analysis did not produce results")
		}
	}

	// Pick or create the pivot point
	if len(story.AnalysisResult.Pivots) == 0 && customScene == "" {
		return 0, fmt.Errorf("no pivot points found")
	}
	var bestPivot model.PivotPoint
	if customScene != "" {
		// User provided a custom branching scene — create synthetic pivot
		bestPivot = model.PivotPoint{
			Scene:           customScene,
			RegretWeight:    0.7,
			LogicDifficulty: 0.5,
			BranchPotential: "用户自定义分支点",
		}
		hub.BroadcastLog("info", "generate", fmt.Sprintf("Using custom scene: %s", trunc(customScene, 50)))
	} else if pivotIdx >= 0 && pivotIdx < len(story.AnalysisResult.Pivots) {
		bestPivot = story.AnalysisResult.Pivots[pivotIdx]
		hub.BroadcastLog("info", "generate", fmt.Sprintf("Using user-selected pivot %d: %s", pivotIdx, trunc(bestPivot.Scene, 50)))
	} else {
		bestPivot = story.AnalysisResult.Pivots[0]
		for _, p := range story.AnalysisResult.Pivots[1:] {
			if p.RegretWeight > bestPivot.RegretWeight {
				bestPivot = p
			}
		}
	}

	// If user provided a custom direction, modify the pivot's branch_potential
	if customPrompt != "" {
		bestPivot.BranchPotential = customPrompt
		hub.BroadcastLog("info", "generate", fmt.Sprintf("Custom direction: %s", customPrompt))
	}
	hub.BroadcastLog("success", "generate",
		fmt.Sprintf("Generating branches for '%s', pivot: %s", story.Title, trunc(bestPivot.Scene, 50)))

	// Load or create state
	state, err := sm.LoadOrCreate(workID, story.Title, story.AnalysisResult)
	if err != nil {
		hub.BroadcastLog("error", "generate", "State load failed: "+err.Error())
	}

	// Generate with state
	result, err := g.Generate(ctx, story, bestPivot, state)
	if err != nil {
		return 0, fmt.Errorf("generate: %w", err)
	}
	branches := result.Branches

	for _, branch := range branches {
		id, err := s.InsertBranch(branch)
		if err != nil {
			hub.BroadcastLog("error", "generate", "Insert branch: "+err.Error())
			continue
		}
		branch.ID = id
	}

	if publish {
		pinID, err := d.PublishCombined(ctx, branches, story.Title)
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

	// Extract and apply state changes
	if state != nil && result.GeneratedRaw != "" {
		changes, extractErr := sm.ExtractStateFromText(ctx, state, result.GeneratedRaw)
		if extractErr != nil {
			hub.BroadcastLog("error", "generate", "State extraction failed: "+extractErr.Error())
		} else if changes != nil {
			branchID := int64(0)
			if len(branches) > 0 {
				branchID = branches[0].ID
			}
			if applyErr := sm.ApplyChanges(state, changes, branchID); applyErr != nil {
				hub.BroadcastLog("error", "generate", "State save failed: "+applyErr.Error())
			} else {
				hub.BroadcastLog("success", "generate",
					fmt.Sprintf("State updated: %d character changes, %d new events (round %d)",
						len(changes.CharacterUpdates), len(changes.NewEvents), state.CurrentRound))
			}
		}
	}

	// Create worldline nodes
	rootID := createRootNodeIfNeeded(s, workID, story.Title)
	reason := fmt.Sprintf("枢纽点: %s", trunc(bestPivot.Scene, 60))
	if customPrompt != "" {
		reason = "自由分支: " + trunc(customPrompt, 60)
	}
	createWorldlineNodes(s, workID, rootID, branches, reason, 1, hub)

	s.UpdateStoryStatus(story.WorkID, model.StatusBranched)
	hub.BroadcastLog("success", "generate", fmt.Sprintf("Generated %d branches for '%s'", len(branches), story.Title))
	return len(branches), nil
}
func continueStory(ctx context.Context, s *store.Store, g *engine.Generator, sm *engine.StateManager, workID, userPrompt string, pivotIdx int, hub *SSEHub) (int, error) {
	story, err := s.GetStory(workID)
	if err != nil {
		return 0, fmt.Errorf("story not found: %w", err)
	}
	if story.AnalysisResult == nil {
		return 0, fmt.Errorf("story must be analyzed first")
	}
	if len(story.AnalysisResult.Pivots) == 0 {
		return 0, fmt.Errorf("no pivot points found")
	}

	// Load current state
	state, err := sm.LoadOrCreate(workID, story.Title, story.AnalysisResult)
	if err != nil {
		return 0, fmt.Errorf("load state: %w", err)
	}

	// Build state context
	stateCtx := sm.BuildStateContext(state)
	if stateCtx == "" {
		stateCtx = fmt.Sprintf("## 初始状态\n世界观: %s\n角色: %s",
			story.AnalysisResult.Worldview,
			strings.Join(story.AnalysisResult.Characters, ", "))
	}

	// Extract style (simplified: reuse existing style from state, or extract fresh)
	styleSummary := "保持原文风格"
	if state.Summary != "" {
		styleSummary = state.Summary
	}

	// Use specified or best pivot
	bestPivot := story.AnalysisResult.Pivots[0]
	if pivotIdx >= 0 && pivotIdx < len(story.AnalysisResult.Pivots) {
		bestPivot = story.AnalysisResult.Pivots[pivotIdx]
	} else {
		for _, p := range story.AnalysisResult.Pivots[1:] {
			if p.RegretWeight > bestPivot.RegretWeight {
				bestPivot = p
			}
		}
	}

	hub.BroadcastLog("success", "continue",
		fmt.Sprintf("Continuing '%s' (round %d) with: %s", story.Title, state.CurrentRound+1, trunc(userPrompt, 50)))

	result, err := g.Continue(ctx, story, bestPivot, styleSummary, stateCtx, userPrompt)
	if err != nil {
		return 0, fmt.Errorf("continue: %w", err)
	}

	for _, branch := range result.Branches {
		id, err := s.InsertBranch(branch)
		if err != nil {
			hub.BroadcastLog("error", "continue", "Insert branch: "+err.Error())
			continue
		}
		branch.ID = id
	}

	// Extract and apply state changes
	if result.GeneratedRaw != "" {
		changes, extractErr := sm.ExtractStateFromText(ctx, state, result.GeneratedRaw)
		if extractErr != nil {
			hub.BroadcastLog("error", "continue", "State extraction failed: "+extractErr.Error())
		} else if changes != nil {
			branchID := int64(0)
			if len(result.Branches) > 0 {
				branchID = result.Branches[0].ID
			}
			if applyErr := sm.ApplyChanges(state, changes, branchID); applyErr != nil {
				hub.BroadcastLog("error", "continue", "State save failed: "+applyErr.Error())
			} else {
				hub.BroadcastLog("success", "continue",
					fmt.Sprintf("State updated: %d char changes, %d events (round %d)",
						len(changes.CharacterUpdates), len(changes.NewEvents), state.CurrentRound))
			}
		}
	}

	s.UpdateStoryStatus(story.WorkID, model.StatusBranched)
	hub.BroadcastLog("success", "continue", fmt.Sprintf("Continued '%s' with %d branches", story.Title, len(result.Branches)))
	return len(result.Branches), nil
}

func continueBranch(ctx context.Context, s *store.Store, lc *llm.Client, sm *engine.StateManager, branchID int64, userPrompt string, hub *SSEHub) (int, error) {
	// Load the target branch directly
	targetBranch, err := s.GetBranchByID(branchID)
	if err != nil {
		return 0, fmt.Errorf("branch %d not found: %w", branchID, err)
	}
	parentStory, err := s.GetStory(targetBranch.StoryWorkID)
	if err != nil {
		return 0, fmt.Errorf("parent story not found: %w", err)
	}
	if parentStory.AnalysisResult == nil {
		return 0, fmt.Errorf("parent story must be analyzed first")
	}

	// Load or create state
	state, err := sm.LoadOrCreate(parentStory.WorkID, parentStory.Title, parentStory.AnalysisResult)
	if err != nil {
		return 0, fmt.Errorf("load state: %w", err)
	}

	// Build context: combine state + branch content
	stateCtx := sm.BuildStateContext(state)
	branchCtx := fmt.Sprintf("## 当前分支剧情\n【%s】%s\n%s\n\n用户希望在这个分支的基础上继续推进: %s",
		targetBranch.Tag, targetBranch.Title, targetBranch.FullStory, userPrompt)
	if stateCtx != "" {
		branchCtx = stateCtx + "\n\n" + branchCtx
	}

	// Build a continuation prompt using the branch content as the story base
	prompt := fmt.Sprintf(`你是一个创意无限的平行宇宙叙事引擎。用户选择了下面这条平行支线，希望继续推进情节。

%s

## 任务
基于上面这条支线的剧情，根据用户的推进方向，生成 1-2 条延续的剧情支线。
必须保持角色性格、关系和记忆的一致性。
每条支线应展示用户推进方向带来的不同可能性。

输出格式（严格JSON）:
{
  "branches": [
    {
      "tag": "反转线",
      "title": "...(15字以内)",
      "preview": "...(150-200字预告)",
      "full_story": "...(500-800字完整内容)",
      "keyword": "...(2-4字解锁词)"
    }
  ]
}`, branchCtx)

	hub.BroadcastLog("success", "continue_branch",
		fmt.Sprintf("Continuing branch #%d '%s', prompt: %s", branchID, targetBranch.Title, trunc(userPrompt, 50)))

	// Generate
	var resp model.GenerateResponse
	if err := lc.ChatJSONWithRetry(ctx, "", prompt, &resp, 3); err != nil {
		return 0, fmt.Errorf("continue branch: %w", err)
	}

	count := 0
	rawText := ""
	var newBranchList []*model.Branch
	for i, b := range resp.Branches {
		branch := &model.Branch{
			StoryWorkID: parentStory.WorkID,
			PivotIndex:  i,
			Tag:         b.Tag,
			Title:       b.Title,
			Preview:     b.Preview,
			FullStory:   b.FullStory,
			Keyword:     b.Keyword,
		}
		id, err := s.InsertBranch(branch)
		if err != nil {
			hub.BroadcastLog("error", "continue_branch", "Insert branch: "+err.Error())
			continue
		}
		branch.ID = id
		newBranchList = append(newBranchList, branch)
		rawText += fmt.Sprintf("【%s】%s\n%s\n\n", b.Tag, b.Title, b.FullStory)
		count++
	}

	// Extract and apply state changes
	if rawText != "" {
		changes, extractErr := sm.ExtractStateFromText(ctx, state, rawText)
		if extractErr != nil {
			hub.BroadcastLog("error", "continue_branch", "State extraction failed: "+extractErr.Error())
		} else if changes != nil {
			if applyErr := sm.ApplyChanges(state, changes, branchID); applyErr != nil {
				hub.BroadcastLog("error", "continue_branch", "State save failed: "+applyErr.Error())
			} else {
				hub.BroadcastLog("success", "continue_branch",
					fmt.Sprintf("State updated: %d char changes, %d events (round %d)",
						len(changes.CharacterUpdates), len(changes.NewEvents), state.CurrentRound))
			}
		}
	}

	// Find parent worldline node for the source branch
	parentNode, err := s.GetWorldlineNodeByBranch(branchID)
	parentDepth := 1
	parentNodeID := createRootNodeIfNeeded(s, parentStory.WorkID, parentStory.Title)
	if err == nil && parentNode != nil {
		parentNodeID = parentNode.ID
		parentDepth = parentNode.Depth + 1
	}
	// Create worldline nodes for the new branches we just generated
	if len(newBranchList) > 0 {
		createWorldlineNodes(s, parentStory.WorkID, parentNodeID, newBranchList, "继续推进: "+trunc(userPrompt, 60), parentDepth, hub)
	}

	hub.BroadcastLog("success", "continue_branch", fmt.Sprintf("Branch #%d continued with %d new branches", branchID, count))
	return count, nil
}

// createWorldlineNodes creates worldline nodes for newly generated branches.
func createWorldlineNodes(s *store.Store, workID string, parentNodeID int64, branches []*model.Branch, reason string, depth int, hub *SSEHub) {
	for _, b := range branches {
		node := &model.WorldlineNode{
			StoryWorkID:     workID,
			ParentID:        parentNodeID,
			BranchID:        b.ID,
			BranchReason:    reason,
			TimelineSummary: trunc(b.Preview, 100),
			NodeTitle:       b.Tag + " · " + b.Title,
			Tag:             b.Tag,
			Depth:           depth,
		}
		id, err := s.InsertWorldlineNode(node)
		if err != nil {
			hub.BroadcastLog("error", "worldline", "Failed to create node: "+err.Error())
			continue
		}
		node.ID = id
		hub.BroadcastLog("info", "worldline", fmt.Sprintf("Node #%d depth=%d: %s", id, depth, node.NodeTitle))
	}
}

// createRootNodeIfNeeded ensures a root worldline node exists for the story.
func createRootNodeIfNeeded(s *store.Store, workID, title string) int64 {
	existing, err := s.GetWorldlineTree(workID)
	if err != nil {
		log.Printf("[worldline] get tree: %v", err)
	}
	// Return existing root node ID if already created
	for _, n := range existing {
		if n.Depth == 0 {
			return n.ID
		}
	}
	if len(existing) > 0 {
		return existing[0].ID
	}
	node := &model.WorldlineNode{
		StoryWorkID:     workID,
		ParentID:        0,
		BranchID:        0,
		BranchReason:    "原始故事",
		TimelineSummary: "故事起点",
		NodeTitle:       title,
		Tag:             "origin",
		Depth:           0,
	}
	id, err := s.InsertWorldlineNode(node)
	if err != nil {
		log.Printf("[worldline] create root: %v", err)
		return 0
	}
	return id
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
	branches, err := s.getStore(r).GetBranchesByStory(workID)
	if err != nil || len(branches) == 0 {
		writeError(w, "no branches to publish", 400)
		return
	}
	story, err := s.getStore(r).GetStory(workID)
	if err != nil {
		writeError(w, "story not found", 404)
		return
	}
	pinID, err := s.dispatcher.PublishCombined(r.Context(), branches, story.Title)
	if err != nil {
		writeError(w, "publish failed: "+err.Error(), 500)
		return
	}
	for _, b := range branches {
		b.PinID = pinID
		s.getStore(r).UpdateBranchPinID(b.ID, pinID)
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
		ZhihuToken  string `json:"zhihu_token"`
		ZhihuSecret string `json:"zhihu_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ZhihuToken == "" {
		writeError(w, "zhihu_token is required", 400)
		return
	}
	s.getStore(r).SetSetting("zhihu_token", body.ZhihuToken)
	if body.ZhihuSecret != "" {
		s.getStore(r).SetSetting("zhihu_secret", body.ZhihuSecret)
	}
	s.hub.BroadcastLog("success", "auth", "用户已登录")
	writeJSON(w, map[string]interface{}{"logged_in": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.getStore(r).SetSetting("zhihu_token", "")
	s.getStore(r).SetSetting("zhihu_secret", "")
	writeJSON(w, map[string]interface{}{"logged_in": false})
}

// OAuth handlers

func (s *Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	authURL := fmt.Sprintf("https://openapi.zhihu.com/authorize?redirect_uri=%s&app_id=%s&response_type=code",
		url.QueryEscape(config.OAuthRedirectURI()), config.OAuthAppID())
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		code = r.URL.Query().Get("authorization_code")
	}
	log.Printf("[oauth] received code=%q (len=%d)", code, len(code))
	if code == "" {
		writeError(w, "authorization code is required", 400)
		return
	}

	appID := config.OAuthAppID()
	appKey := config.OAuthAppKey()

	formData := url.Values{
		"app_id":       {appID},
		"app_key":      {appKey},
		"grant_type":   {"authorization_code"},
		"redirect_uri": {config.OAuthRedirectURI()},
		"code":         {code},
	}
	oauthCtx, oauthCancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer oauthCancel()
	oauthReq, err := http.NewRequestWithContext(oauthCtx, "POST", "https://openapi.zhihu.com/access_token", strings.NewReader(formData.Encode()))
	if err != nil {
		writeError(w, "token exchange request failed: "+err.Error(), 500)
		return
	}
	oauthReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	log.Printf("[oauth] exchanging code with app_id=%s redirect_uri=%s", appID, config.OAuthRedirectURI())
	resp, err := http.DefaultClient.Do(oauthReq)
	if err != nil {
		writeError(w, "token exchange failed: "+err.Error(), 500)
		return
	}
	defer resp.Body.Close()

	// Read raw response for debugging
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[oauth] failed to read token response: %v", err)
		writeError(w, "failed to read token response", 500)
		return
	}
	log.Printf("[oauth] token exchange completed (len=%d)", len(respBody))

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
		Code        int    `json:"code"`
		Msg         string `json:"msg"`
		Message     string `json:"message"`
		Data        string `json:"data"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		writeError(w, "decode token response: "+err.Error(), 500)
		return
	}
	if tokenResp.AccessToken == "" {
		writeError(w, "oauth token exchange returned empty token", 400)
		return
	}

	// Store the access token
	s.getStore(r).SetSetting("oauth_access_token", tokenResp.AccessToken)
	s.hub.BroadcastLog("success", "oauth", "OAuth login successful")

	writeJSON(w, map[string]interface{}{"logged_in": true})
}

func (s *Server) handleOAuthUser(w http.ResponseWriter, r *http.Request) {
	token, err := s.getStore(r).GetSetting("oauth_access_token")
	if err != nil || token == "" {
		writeError(w, "not logged in", 401)
		return
	}

	userCtx, userCancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer userCancel()
	req, err := http.NewRequestWithContext(userCtx, "GET", "https://openapi.zhihu.com/user", nil)
	if err != nil {
		writeError(w, "create request failed: "+err.Error(), 500)
		return
	}
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
	s.getStore(r).SetSetting("oauth_access_token", "")
	writeJSON(w, map[string]interface{}{"logged_out": true})
}
