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

// BlockReason explains why a story was blocked.
type BlockReason struct {
	Blocked bool
	Reason  string
}

// CheckStoryBlocked determines if a story should be blocked from branch generation.
// Uses LLM classification first, with keyword fallback as safety net.
func CheckStoryBlocked(analysis *model.AnalysisResult, title, content string) BlockReason {
	if analysis == nil {
		return BlockReason{true, "未完成分析，请先解构故事"}
	}

	// Keyword safety net: override LLM classification if strong modern keywords found
	text := title + " " + content
	for _, kw := range modernKeywords {
		if strings.Contains(text, kw) {
			return BlockReason{true, "关键词拦截: 涉及中国近现代真实历史 (" + kw + ")"}
		}
	}

	switch analysis.Classification {
	case "fiction", "":
		return BlockReason{false, ""}
	case "real_history":
		return BlockReason{false, ""}
	case "real_modern":
		return BlockReason{true, "涉及中国近现代真实历史，暂不支持生成平行支线"}
	default:
		return BlockReason{false, ""}
	}
}

// ClassificationLabel returns a human-readable label for the classification.
func ClassificationLabel(c string) string {
	switch c {
	case "fiction":
		return "虚构故事"
	case "real_history":
		return "历史纪实"
	case "real_modern":
		return "近现代史"
	default:
		return "未分类"
	}
}
