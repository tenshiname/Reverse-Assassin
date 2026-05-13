package zhihu

import (
	"context"
	"encoding/json"
	"fmt"

	"reverse-assassin/internal/config"
	"reverse-assassin/internal/model"
)

// GetCommentList 获取评论列表
func (c *Client) GetCommentList(ctx context.Context, contentToken, contentType string, pageNum, pageSize int) (*model.CommentListData, error) {
	if pageNum <= 0 {
		pageNum = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	params := map[string]string{
		"content_token": contentToken,
		"content_type":  contentType,
		"page_num":      fmt.Sprintf("%d", pageNum),
		"page_size":     fmt.Sprintf("%d", pageSize),
	}

	respBody, err := c.doGet(ctx, "/openapi/comment/list", params, config.ZhihuAPIBase)
	if err != nil {
		return nil, err
	}

	var resp model.APIResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("get comments failed: %s", resp.Msg)
	}

	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal comment list data: %w", err)
	}
	var data model.CommentListData
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		return nil, fmt.Errorf("unmarshal data: %w, raw: %s", err, string(respBody))
	}
	return &data, nil
}

// CreateComment 创建评论
func (c *Client) CreateComment(ctx context.Context, contentToken, contentType, content string) (string, error) {
	body := map[string]string{
		"content_token": contentToken,
		"content_type":  contentType,
		"content":       content,
	}

	respBody, err := c.doPost(ctx, "/openapi/comment/create", body, config.ZhihuAPIBase)
	if err != nil {
		return "", err
	}

	var raw struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			CommentID int64 `json:"comment_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if raw.Code != 0 {
		return "", fmt.Errorf("create comment failed: %s", raw.Msg)
	}

	return fmt.Sprintf("%d", raw.Data.CommentID), nil
}

// DeleteComment 删除评论
func (c *Client) DeleteComment(ctx context.Context, commentID string) error {
	body := map[string]string{
		"comment_id": commentID,
	}

	respBody, err := c.doPost(ctx, "/openapi/comment/delete", body, config.ZhihuAPIBase)
	if err != nil {
		return err
	}

	var resp model.APIResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Status != 0 {
		return fmt.Errorf("delete comment failed: %s", resp.Msg)
	}
	return nil
}

// Like 点赞/取消点赞
func (c *Client) Like(ctx context.Context, contentType, contentToken string, actionValue int) error {
	body := map[string]interface{}{
		"content_token": contentToken,
		"content_type":  contentType,
		"action_type":   "like",
		"action_value":  actionValue,
	}

	respBody, err := c.doPost(ctx, "/openapi/reaction", body, config.ZhihuAPIBase)
	if err != nil {
		return err
	}

	var resp model.APIResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Status != 0 {
		return fmt.Errorf("like failed: %s", resp.Msg)
	}
	return nil
}
