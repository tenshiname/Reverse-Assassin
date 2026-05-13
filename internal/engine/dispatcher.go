package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"reverse-assassin/internal/config"
	"reverse-assassin/internal/model"
	"reverse-assassin/internal/store"
	"reverse-assassin/internal/zhihu"
)

type ReplyTask struct {
	CommentID string
	Content   string
}

var ReplyTaskQueue = make(chan ReplyTask, 1000)

func StartReplyWorker(client *zhihu.Client) {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for task := range ReplyTaskQueue {
			<-ticker.C
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if _, err := client.CreateComment(ctx, task.CommentID, "comment", task.Content); err != nil {
				log.Printf("[ReplyWorker] reply failed: %v", err)
			} else {
				log.Printf("[ReplyWorker] reply sent to comment %s", task.CommentID[:12])
			}
			cancel()
		}
	}()
}

type Dispatcher struct {
	zhihuClient *zhihu.Client
	store       *store.Store
}

func NewDispatcher(zc *zhihu.Client, s *store.Store) *Dispatcher {
	return &Dispatcher{zhihuClient: zc, store: s}
}

func (d *Dispatcher) PostBranch(ctx context.Context, branch *model.Branch, storyTitle string) (string, error) {
	content := fmt.Sprintf(
		"📖 【平行宇宙·%s】\n\n原著《%s》的另一个结局...\n\n%s\n\n---\n💬 在评论区回复关键词「%s」，解锁这条支线的完整剧情。",
		branch.Tag, storyTitle, branch.Preview, branch.Keyword,
	)
	title := fmt.Sprintf("【%s】%s", branch.Tag, branch.Title)
	pinID, err := d.zhihuClient.PublishPin(ctx, config.DefaultRing, content, title, nil)
	if err != nil {
		return "", fmt.Errorf("publish pin: %w", err)
	}
	branch.PinID = pinID
	log.Printf("[Dispatcher] branch '%s' published, pin_id=%s, keyword='%s'", branch.Tag, pinID, branch.Keyword)
	return pinID, nil
}

func (d *Dispatcher) UnlockAndReply(story *model.StoryRecord, branch *model.Branch, comment model.Comment, fullStory string) error {
	replyContent := fmt.Sprintf(
		"@%s 🔓 你已解锁【%s·%s】！\n\n%s\n\n---\n⚡ 这是你独家解锁的平行宇宙结局。感谢你的参与！",
		comment.AuthorName, branch.Tag, branch.Title, fullStory,
	)

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

func (d *Dispatcher) PublishCombined(ctx context.Context, branches []*model.Branch, storyTitle string) (string, error) {
	content := BuildPinMessage(branches, storyTitle)
	pinID, err := d.zhihuClient.PublishPin(ctx, config.DefaultRing, content, "🌌 平行宇宙分支", nil)
	if err != nil {
		return "", fmt.Errorf("publish combined pin: %w", err)
	}
	log.Printf("[Dispatcher] combined pin published, pin_id=%s", pinID)
	return pinID, nil
}

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
