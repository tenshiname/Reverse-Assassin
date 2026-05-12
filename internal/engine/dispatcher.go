package engine

import (
	"fmt"
	"log"
	"strings"
	"time"

	"reverse-assassin/internal/config"
	"reverse-assassin/internal/model"
	"reverse-assassin/internal/store"
	"reverse-assassin/internal/zhihu"
)

// ReplyTask represents a queued reply operation.
type ReplyTask struct {
	CommentID string
	Content   string
}

// ReplyTaskQueue is the async reply queue (capacity 1000).
var ReplyTaskQueue = make(chan ReplyTask, 1000)

// StartReplyWorker launches a rate-limited goroutine that consumes the reply queue.
// Strictly respects 10 QPS via 100ms ticker.
func StartReplyWorker(client *zhihu.Client) {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for task := range ReplyTaskQueue {
			<-ticker.C // rate limit
			_, err := client.CreateComment(task.CommentID, "comment", task.Content)
			if err != nil {
				log.Printf("[ReplyWorker] reply failed: %v", err)
			} else {
				log.Printf("[ReplyWorker] reply sent to comment %s", task.CommentID[:12])
			}
		}
	}()
}

// Dispatcher handles branch publishing and interaction replies.
type Dispatcher struct {
	zhihuClient *zhihu.Client
	store       *store.Store
}

func NewDispatcher(zc *zhihu.Client, s *store.Store) *Dispatcher {
	return &Dispatcher{zhihuClient: zc, store: s}
}

// PostBranch publishes a single branch preview to the ring.
func (d *Dispatcher) PostBranch(branch *model.Branch, storyTitle string) (string, error) {
	content := fmt.Sprintf(
		"📖 【平行宇宙·%s】\n\n原著《%s》的另一个结局...\n\n%s\n\n---\n💬 在评论区回复关键词「%s」，解锁这条支线的完整剧情。",
		branch.Tag, storyTitle, branch.Preview, branch.Keyword,
	)
	title := fmt.Sprintf("【%s】%s", branch.Tag, branch.Title)
	pinID, err := d.zhihuClient.PublishPin(config.DefaultRing, content, title, nil)
	if err != nil {
		return "", fmt.Errorf("publish pin: %w", err)
	}
	branch.PinID = pinID
	log.Printf("[Dispatcher] branch '%s' published, pin_id=%s, keyword='%s'", branch.Tag, pinID, branch.Keyword)
	return pinID, nil
}

// UnlockAndReply queues an unlock reply via the async queue instead of calling the API synchronously.
func (d *Dispatcher) UnlockAndReply(story *model.StoryRecord, branch *model.Branch, comment model.Comment, fullStory string) error {
	replyContent := fmt.Sprintf(
		"@%s 🔓 你已解锁【%s·%s】！\n\n%s\n\n---\n⚡ 这是你独家解锁的平行宇宙结局。感谢你的参与！",
		comment.AuthorName, branch.Tag, branch.Title, fullStory,
	)

	// Queue reply asynchronously (non-blocking with fallback)
	select {
	case ReplyTaskQueue <- ReplyTask{CommentID: comment.CommentID, Content: replyContent}:
	default:
		log.Printf("[Dispatcher] reply queue full, dropping reply to %s", comment.CommentID[:12])
	}

	if err := d.store.UnlockBranch(branch.ID); err != nil {
		log.Printf("[Dispatcher] unlock branch failed: %v", err)
	}

	record := &model.InteractionRecord{
		CommentID:  comment.CommentID,
		Content:    comment.Content,
		AuthorName: comment.AuthorName,
		MatchedKey: branch.Keyword,
		Processed:  true,
	}
	if err := d.store.RecordInteraction(record); err != nil {
		log.Printf("[Dispatcher] record interaction failed: %v", err)
	}

	log.Printf("[Dispatcher] ✅ unlocked '%s' for @%s, keyword='%s'", branch.Tag, comment.AuthorName, branch.Keyword)
	return nil
}

// PublishCombined publishes a combined idea containing multiple branch previews.
func (d *Dispatcher) PublishCombined(branches []*model.Branch, storyTitle string) (string, error) {
	content := BuildPinMessage(branches, storyTitle)
	pinID, err := d.zhihuClient.PublishPin(config.DefaultRing, content, "🌌 平行宇宙分支", nil)
	if err != nil {
		return "", fmt.Errorf("publish combined pin: %w", err)
	}
	log.Printf("[Dispatcher] combined pin published, pin_id=%s", pinID)
	return pinID, nil
}

// BuildPinMessage constructs the combined idea text.
func BuildPinMessage(branches []*model.Branch, storyTitle string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🌌 【平行宇宙分支】《%s》\n\n", storyTitle))
	sb.WriteString("经过对评论区情绪的深度分析，我们发现了3个完全不同的剧情可能性：\n\n")
	for i, b := range branches {
		sb.WriteString(fmt.Sprintf("## %d. 【%s】%s\n", i+1, b.Tag, b.Title))
		sb.WriteString(fmt.Sprintf("%s\n", b.Preview))
		sb.WriteString(fmt.Sprintf("> 💬 回复「%s」解锁完整剧情\n\n", b.Keyword))
	}
	sb.WriteString("---\n⚡ 反转刺客 AI · 每一个意难平的结局，都是新宇宙的起点。")
	return sb.String()
}
