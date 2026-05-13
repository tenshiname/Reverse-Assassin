package zhihu

import (
	"context"
	"encoding/json"
	"fmt"

	"reverse-assassin/internal/config"
	"reverse-assassin/internal/model"
)

// GetStoryList 获取故事概要列表
func (c *Client) GetStoryList(ctx context.Context) ([]model.StorySummary, error) {
	body, err := c.doGet(ctx, "/openapi/hackathon_story/list", nil, config.ZhihuAPIBase)
	if err != nil {
		return nil, err
	}

	var resp model.APIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("get story list failed: %s", resp.Msg)
	}

	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal story list data: %w", err)
	}
	var stories []model.StorySummary
	if err := json.Unmarshal(dataBytes, &stories); err != nil {
		return nil, fmt.Errorf("unmarshal stories: %w, raw: %s", err, string(body))
	}
	return stories, nil
}

// GetStoryDetail 获取故事详情
func (c *Client) GetStoryDetail(ctx context.Context, workID string) (*model.StoryDetail, error) {
	params := map[string]string{
		"work_id": workID,
	}

	body, err := c.doGet(ctx, "/openapi/hackathon_story/detail", params, config.ZhihuAPIBase)
	if err != nil {
		return nil, err
	}

	var resp model.APIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("get story detail failed: %s", resp.Msg)
	}

	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal story detail data: %w", err)
	}
	var story model.StoryDetail
	if err := json.Unmarshal(dataBytes, &story); err != nil {
		return nil, fmt.Errorf("unmarshal story detail: %w, raw: %s", err, string(body))
	}
	return &story, nil
}
