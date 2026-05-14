package engine

import (
	"strings"

	"reverse-assassin/internal/model"
)

// modernKeywords are strong indicators of modern Chinese history.
// Even if LLM misclassifies, these trigger a block.
var modernKeywords = []string{
	"长征", "红军", "八路军", "解放军", "共产党", "中共",
	"毛泽东", "周恩来", "邓小平", "文化大革命", "文革",
	"土改", "反右", "大跃进", "改革开放",
}

type BlockReason struct {
	Blocked bool
	Reason  string
}

func CheckStoryBlocked(analysis *model.AnalysisResult, title, content string) BlockReason {
	if analysis == nil {
		return BlockReason{true, "Analysis required"}
	}
	text := title + " " + content
	for _, kw := range modernKeywords {
		if strings.Contains(text, kw) {
			return BlockReason{true, ""}
		}
	}
	if analysis.Classification == "real_modern" {
		return BlockReason{true, ""}
	}
	return BlockReason{false, ""}
}

func ClassificationLabel(c string) string {
	switch c {
	case "fiction": return "虚构"
	case "real_history": return "历史"
	case "real_modern": return "纪实"
	default: return ""
	}
}
