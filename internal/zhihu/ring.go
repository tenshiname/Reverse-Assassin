package zhihu

import (
	"encoding/json"
	"fmt"

	"reverse-assassin/internal/config"
	"reverse-assassin/internal/model"
)

// GetRingDetail 获取圈子详情和最新内容
func (c *Client) GetRingDetail(ringID string, pageNum, pageSize int) (*model.RingDetailData, error) {
	if pageNum <= 0 {
		pageNum = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	params := map[string]string{
		"ring_id":   ringID,
		"page_num":  fmt.Sprintf("%d", pageNum),
		"page_size": fmt.Sprintf("%d", pageSize),
	}

	body, err := c.doGet("/openapi/ring/detail", params, config.ZhihuAPIBase)
	if err != nil {
		return nil, err
	}

	var resp model.APIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("API error: %s", resp.Msg)
	}

	// 重新解析 data
	dataBytes, _ := json.Marshal(resp.Data)
	var data model.RingDetailData
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		return nil, fmt.Errorf("unmarshal data: %w, raw: %s", err, string(body))
	}
	return &data, nil
}

// GetRingDefaultRing 获取默认圈子详情
func (c *Client) GetDefaultRing(pageSize int) (*model.RingDetailData, error) {
	return c.GetRingDetail(config.DefaultRing, 1, pageSize)
}
