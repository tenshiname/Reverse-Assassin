package zhihu

import (
	"context"
	"encoding/json"
	"fmt"

	"reverse-assassin/internal/config"
	"reverse-assassin/internal/model"
)

// PublishPin 在指定圈子发布想法
func (c *Client) PublishPin(ctx context.Context, ringID, content, title string, imageURLs []string) (string, error) {
	body := map[string]interface{}{
		"ring_id": ringID,
		"content": content,
	}
	if title != "" {
		body["title"] = title
	}
	if len(imageURLs) > 0 {
		body["image_urls"] = imageURLs
	}

	respBody, err := c.doPost(ctx, "/openapi/publish/pin", body, config.ZhihuAPIBase)
	if err != nil {
		return "", err
	}

	var resp model.APIResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Status != 0 {
		return "", fmt.Errorf("publish pin failed: %s", resp.Msg)
	}

	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		return "", fmt.Errorf("marshal publish pin data: %w", err)
	}
	var data model.PublishPinData
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		return "", fmt.Errorf("unmarshal data: %w", err)
	}
	return data.ContentToken, nil
}
