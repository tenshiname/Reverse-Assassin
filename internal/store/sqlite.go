package store

import (
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
		analysisJSON, _ = json.Marshal(story.AnalysisResult)
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
	analysisJSON, _ := json.Marshal(result)
	_, err := s.db.Exec(
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
