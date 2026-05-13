package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"reverse-assassin/internal/model"
)

type Store struct {
	db *sql.DB
}

func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// WAL mode for concurrent reads, busy timeout to reduce lock errors
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("pragma %s: %w", pragma, err)
		}
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &Store{db: db}, nil
}


// ContextKey is used for context value keys to avoid collisions.
type ContextKey string

// StoreFromContext retrieves the namespace store from context, or returns nil.
func StoreFromContext(ctx context.Context) *Store {
	if st, ok := ctx.Value(ContextKey("store")).(*Store); ok {
		return st
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func migrate(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS stories (
			work_id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			author TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			analysis_result TEXT DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS branches (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			story_work_id TEXT NOT NULL,
			pivot_index INTEGER NOT NULL DEFAULT 0,
			tag TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			preview TEXT NOT NULL DEFAULT '',
			full_story TEXT NOT NULL DEFAULT '',
			pin_id TEXT NOT NULL DEFAULT '',
			keyword TEXT NOT NULL DEFAULT '',
			unlocked INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (story_work_id) REFERENCES stories(work_id)
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS interactions (
			comment_id TEXT PRIMARY KEY,
			content TEXT NOT NULL DEFAULT '',
			author_name TEXT NOT NULL DEFAULT '',
			matched_key TEXT NOT NULL DEFAULT '',
			processed INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_interactions_unique_trigger
		 ON interactions(author_name, matched_key)`,
		// Narrative State Engine tables
		`CREATE TABLE IF NOT EXISTS story_states (
			story_work_id TEXT PRIMARY KEY,
			story_title TEXT NOT NULL DEFAULT '',
			worldview TEXT NOT NULL DEFAULT '',
			current_round INTEGER NOT NULL DEFAULT 0,
			summary TEXT NOT NULL DEFAULT '',
			plot_threads TEXT NOT NULL DEFAULT '[]',
			tone TEXT NOT NULL DEFAULT '[]',
			updated_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS character_states (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			story_work_id TEXT NOT NULL,
			name TEXT NOT NULL,
			data TEXT NOT NULL DEFAULT '{}',
			updated_at INTEGER NOT NULL DEFAULT 0,
			UNIQUE(story_work_id, name),
			FOREIGN KEY (story_work_id) REFERENCES stories(work_id)
		)`,
		`CREATE TABLE IF NOT EXISTS timeline_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			story_work_id TEXT NOT NULL,
			branch_id INTEGER NOT NULL DEFAULT 0,
			round INTEGER NOT NULL DEFAULT 0,
			sequence INTEGER NOT NULL DEFAULT 0,
			type TEXT NOT NULL DEFAULT '',
			scene TEXT NOT NULL DEFAULT '',
			characters TEXT NOT NULL DEFAULT '[]',
			cause_event INTEGER NOT NULL DEFAULT 0,
			outcome TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (story_work_id) REFERENCES stories(work_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_timeline_story ON timeline_events(story_work_id, round)`,
		`CREATE INDEX IF NOT EXISTS idx_char_story ON character_states(story_work_id)`,
		// Worldline System
		`CREATE TABLE IF NOT EXISTS worldline_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			story_work_id TEXT NOT NULL,
			parent_id INTEGER NOT NULL DEFAULT 0,
			branch_id INTEGER NOT NULL DEFAULT 0,
			branch_reason TEXT NOT NULL DEFAULT '',
			timeline_summary TEXT NOT NULL DEFAULT '',
			node_title TEXT NOT NULL DEFAULT '',
			tag TEXT NOT NULL DEFAULT '',
			depth INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (story_work_id) REFERENCES stories(work_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_worldline_story ON worldline_nodes(story_work_id, depth)`,
	}
	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("exec: %w\n%s", err, q)
		}
	}
	return nil
}

// ============================================================
// 故事操作
// ============================================================

func (s *Store) InsertStory(story *model.StoryRecord) error {
	now := time.Now().Unix()
	story.CreatedAt = now
	story.UpdatedAt = now

	var analysisJSON []byte
	if story.AnalysisResult != nil {
		var err error
		analysisJSON, err = json.Marshal(story.AnalysisResult)
		if err != nil {
			return fmt.Errorf("marshal analysis result: %w", err)
		}
	}

	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO stories (work_id, title, author, content, status, analysis_result, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		story.WorkID, story.Title, story.Author, story.Content,
		string(story.Status), string(analysisJSON), story.CreatedAt, story.UpdatedAt,
	)
	return err
}

func (s *Store) UpdateStoryStatus(workID string, status model.StoryStatus) error {
	_, err := s.db.Exec(
		`UPDATE stories SET status = ?, updated_at = ? WHERE work_id = ?`,
		string(status), time.Now().Unix(), workID,
	)
	return err
}

func (s *Store) UpdateStoryAnalysis(workID string, result *model.AnalysisResult) error {
	analysisJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal analysis result: %w", err)
	}
	_, err = s.db.Exec(
		`UPDATE stories SET analysis_result = ?, status = ?, updated_at = ? WHERE work_id = ?`,
		string(analysisJSON), string(model.StatusAnalyzed), time.Now().Unix(), workID,
	)
	return err
}

func (s *Store) GetStory(workID string) (*model.StoryRecord, error) {
	row := s.db.QueryRow(
		`SELECT work_id, title, author, content, status, analysis_result, created_at, updated_at
		 FROM stories WHERE work_id = ?`, workID,
	)

	var r model.StoryRecord
	var analysisJSON string
	err := row.Scan(&r.WorkID, &r.Title, &r.Author, &r.Content, &r.Status, &analysisJSON, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	r.Status = model.StoryStatus(r.Status)
	if analysisJSON != "" {
		var ar model.AnalysisResult
		if json.Unmarshal([]byte(analysisJSON), &ar) == nil {
			r.AnalysisResult = &ar
		}
	}
	return &r, nil
}

func (s *Store) StoryExists(workID string) bool {
	var exists int
	s.db.QueryRow(`SELECT 1 FROM stories WHERE work_id = ?`, workID).Scan(&exists)
	return exists == 1
}

func (s *Store) ListStoriesByStatus(status model.StoryStatus) ([]*model.StoryRecord, error) {
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = s.db.Query(
			`SELECT work_id, title, author, content, status, analysis_result, created_at, updated_at
			 FROM stories ORDER BY created_at DESC`,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT work_id, title, author, content, status, analysis_result, created_at, updated_at
			 FROM stories WHERE status = ? ORDER BY created_at DESC`, string(status),
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*model.StoryRecord
	for rows.Next() {
		var r model.StoryRecord
		var analysisJSON string
		if err := rows.Scan(&r.WorkID, &r.Title, &r.Author, &r.Content, &r.Status, &analysisJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
			continue
		}
		r.Status = model.StoryStatus(r.Status)
		if analysisJSON != "" {
			var ar model.AnalysisResult
			if json.Unmarshal([]byte(analysisJSON), &ar) == nil {
				r.AnalysisResult = &ar
			}
		}
		results = append(results, &r)
	}
	return results, rows.Err()
}

// ============================================================
// 分支操作
// ============================================================

func (s *Store) InsertBranch(branch *model.Branch) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO branches (story_work_id, pivot_index, tag, title, preview, full_story, pin_id, keyword, unlocked, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		branch.StoryWorkID, branch.PivotIndex, branch.Tag, branch.Title,
		branch.Preview, branch.FullStory, branch.PinID, branch.Keyword,
		boolToInt(branch.Unlocked), time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateBranchPinID(branchID int64, pinID string) error {
	_, err := s.db.Exec(`UPDATE branches SET pin_id = ? WHERE id = ?`, pinID, branchID)
	return err
}

func (s *Store) GetBranchesByStory(workID string) ([]*model.Branch, error) {
	rows, err := s.db.Query(
		`SELECT id, story_work_id, pivot_index, tag, title, preview, full_story, pin_id, keyword, unlocked, created_at
		 FROM branches WHERE story_work_id = ? ORDER BY id`, workID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var branches []*model.Branch
	for rows.Next() {
		var b model.Branch
		var unlocked int
		if err := rows.Scan(&b.ID, &b.StoryWorkID, &b.PivotIndex, &b.Tag, &b.Title,
			&b.Preview, &b.FullStory, &b.PinID, &b.Keyword, &unlocked, &b.CreatedAt); err != nil {
			continue
		}
		b.Unlocked = unlocked == 1
		branches = append(branches, &b)
	}
	return branches, rows.Err()
}

// GetBranchByID loads a single branch by its ID.
func (s *Store) GetBranchByID(id int64) (*model.Branch, error) {
	row := s.db.QueryRow(
		`SELECT id, story_work_id, pivot_index, tag, title, preview, full_story, pin_id, keyword, unlocked, created_at
		 FROM branches WHERE id = ?`, id,
	)
	var b model.Branch
	var unlocked int
	err := row.Scan(&b.ID, &b.StoryWorkID, &b.PivotIndex, &b.Tag, &b.Title,
		&b.Preview, &b.FullStory, &b.PinID, &b.Keyword, &unlocked, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	b.Unlocked = unlocked == 1
	return &b, nil
}

func (s *Store) GetBranchByKeyword(workID, keyword string) (*model.Branch, error) {
	row := s.db.QueryRow(
		`SELECT id, story_work_id, pivot_index, tag, title, preview, full_story, pin_id, keyword, unlocked, created_at
		 FROM branches WHERE story_work_id = ? AND keyword = ? AND unlocked = 0 LIMIT 1`, workID, keyword,
	)
	var b model.Branch
	var unlocked int
	err := row.Scan(&b.ID, &b.StoryWorkID, &b.PivotIndex, &b.Tag, &b.Title,
		&b.Preview, &b.FullStory, &b.PinID, &b.Keyword, &unlocked, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	b.Unlocked = unlocked == 1
	return &b, nil
}

func (s *Store) UnlockBranch(branchID int64) error {
	_, err := s.db.Exec(`UPDATE branches SET unlocked = 1 WHERE id = ?`, branchID)
	return err
}

func (s *Store) ListActivePinIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT pin_id FROM branches WHERE pin_id != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ============================================================
// 互动记录操作
// ============================================================

func (s *Store) InteractionExists(commentID string) bool {
	var exists int
	s.db.QueryRow(`SELECT 1 FROM interactions WHERE comment_id = ?`, commentID).Scan(&exists)
	return exists == 1
}

func (s *Store) RecordInteraction(record *model.InteractionRecord) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO interactions (comment_id, content, author_name, matched_key, processed, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		record.CommentID, record.Content, record.AuthorName,
		record.MatchedKey, boolToInt(record.Processed), time.Now().Unix(),
	)
	return err
}

// HasAuthorTriggered checks if an author already triggered a keyword (anti-spam).
func (s *Store) HasAuthorTriggered(authorName, keyword string) bool {
	var exists int
	s.db.QueryRow(`SELECT 1 FROM interactions WHERE author_name = ? AND matched_key = ?`, authorName, keyword).Scan(&exists)
	return exists == 1
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ============================================================
// 系统配置 (Key-Value)
// ============================================================

func (s *Store) GetSetting(key string) (string, error) {
	var val string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	if err != nil {
		return "", err
	}
	return val, nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES (?, ?, ?)`,
		key, value, time.Now().Unix(),
	)
	return err
}

func (s *Store) GetSettingsMap() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		m[k] = v
	}
	return m, rows.Err()
}


// ============================================================
// Narrative State Engine 操作
// ============================================================

func (s *Store) SaveStoryState(state *model.StoryState) error {
	plotJSON, err := json.Marshal(state.PlotThreads)
	if err != nil {
		return fmt.Errorf("marshal plot threads: %w", err)
	}
	toneJSON, err := json.Marshal(state.Tone)
	if err != nil {
		return fmt.Errorf("marshal tone: %w", err)
	}
	state.UpdatedAt = time.Now().Unix()
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO story_states (story_work_id, story_title, worldview, current_round, summary, plot_threads, tone, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		state.StoryWorkID, state.StoryTitle, state.Worldview,
		state.CurrentRound, state.Summary, string(plotJSON), string(toneJSON), state.UpdatedAt,
	)
	return err
}

func (s *Store) LoadStoryState(workID string) (*model.StoryState, error) {
	row := s.db.QueryRow(
		`SELECT story_work_id, story_title, worldview, current_round, summary, plot_threads, tone, updated_at
		 FROM story_states WHERE story_work_id = ?`, workID,
	)
	var st model.StoryState
	var plotJSON, toneJSON string
	if err := row.Scan(&st.StoryWorkID, &st.StoryTitle, &st.Worldview, &st.CurrentRound, &st.Summary, &plotJSON, &toneJSON, &st.UpdatedAt); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(plotJSON), &st.PlotThreads)
	json.Unmarshal([]byte(toneJSON), &st.Tone)
	return &st, nil
}

func (s *Store) SaveCharacterStateForStory(workID string, cs *model.CharacterState) error {
	dataJSON, err := json.Marshal(cs)
	if err != nil {
		return fmt.Errorf("marshal character state: %w", err)
	}
	cs.LastUpdated = time.Now().Unix()
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO character_states (story_work_id, name, data, updated_at)
		 VALUES (?, ?, ?, ?)`,
		workID, cs.Name, string(dataJSON), cs.LastUpdated,
	)
	return err
}

func (s *Store) LoadCharacterStates(workID string) ([]*model.CharacterState, error) {
	rows, err := s.db.Query(
		`SELECT data FROM character_states WHERE story_work_id = ? ORDER BY name`, workID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chars []*model.CharacterState
	for rows.Next() {
		var dataJSON string
		if err := rows.Scan(&dataJSON); err != nil {
			continue
		}
		var cs model.CharacterState
		if json.Unmarshal([]byte(dataJSON), &cs) == nil {
			chars = append(chars, &cs)
		}
	}
	return chars, rows.Err()
}

func (s *Store) AddTimelineEvent(ev *model.TimelineEvent) (int64, error) {
	charsJSON, err := json.Marshal(ev.Characters)
	if err != nil {
		return 0, fmt.Errorf("marshal characters: %w", err)
	}
	ev.CreatedAt = time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO timeline_events (story_work_id, branch_id, round, sequence, type, scene, characters, cause_event, outcome, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.StoryWorkID, ev.BranchID, ev.Round, ev.Sequence, ev.Type,
		ev.Scene, string(charsJSON), ev.CauseEvent, ev.Outcome, ev.CreatedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) LoadTimeline(workID string) ([]*model.TimelineEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, story_work_id, branch_id, round, sequence, type, scene, characters, cause_event, outcome, created_at
		 FROM timeline_events WHERE story_work_id = ? ORDER BY round, sequence`, workID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*model.TimelineEvent
	for rows.Next() {
		var ev model.TimelineEvent
		var charsJSON string
		if err := rows.Scan(&ev.ID, &ev.StoryWorkID, &ev.BranchID, &ev.Round, &ev.Sequence, &ev.Type, &ev.Scene, &charsJSON, &ev.CauseEvent, &ev.Outcome, &ev.CreatedAt); err != nil {
			continue
		}
		json.Unmarshal([]byte(charsJSON), &ev.Characters)
		events = append(events, &ev)
	}
	return events, rows.Err()
}

// ============================================================
// Worldline System 操作
// ============================================================

func (s *Store) InsertWorldlineNode(node *model.WorldlineNode) (int64, error) {
	node.CreatedAt = time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO worldline_nodes (story_work_id, parent_id, branch_id, branch_reason, timeline_summary, node_title, tag, depth, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		node.StoryWorkID, node.ParentID, node.BranchID, node.BranchReason,
		node.TimelineSummary, node.NodeTitle, node.Tag, node.Depth, node.CreatedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetWorldlineTree(workID string) ([]*model.WorldlineNode, error) {
	rows, err := s.db.Query(
		`SELECT id, story_work_id, parent_id, branch_id, branch_reason, timeline_summary, node_title, tag, depth, created_at
		 FROM worldline_nodes WHERE story_work_id = ? ORDER BY depth, id`, workID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*model.WorldlineNode
	for rows.Next() {
		var n model.WorldlineNode
		if err := rows.Scan(&n.ID, &n.StoryWorkID, &n.ParentID, &n.BranchID, &n.BranchReason, &n.TimelineSummary, &n.NodeTitle, &n.Tag, &n.Depth, &n.CreatedAt); err != nil {
			continue
		}
		nodes = append(nodes, &n)
	}
	return nodes, rows.Err()
}

func (s *Store) GetWorldlineNodeByBranch(branchID int64) (*model.WorldlineNode, error) {
	row := s.db.QueryRow(
		`SELECT id, story_work_id, parent_id, branch_id, branch_reason, timeline_summary, node_title, tag, depth, created_at
		 FROM worldline_nodes WHERE branch_id = ?`, branchID,
	)
	var n model.WorldlineNode
	err := row.Scan(&n.ID, &n.StoryWorkID, &n.ParentID, &n.BranchID, &n.BranchReason, &n.TimelineSummary, &n.NodeTitle, &n.Tag, &n.Depth, &n.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}
