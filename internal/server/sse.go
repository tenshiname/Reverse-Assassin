package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

var (
	globalLogBuf []logEntry
	globalLogMu  sync.Mutex
)

func pushGlobalLog(level, module, msg string) {
	globalLogMu.Lock()
	globalLogBuf = append(globalLogBuf, logEntry{Module: module, Msg: msg, Level: level, Time: time.Now().UnixMilli()})
	if len(globalLogBuf) > 100 { globalLogBuf = globalLogBuf[1:] }
	globalLogMu.Unlock()
}

type logEntry struct {
	Module  string `json:"module"`
	Msg     string `json:"msg"`
	Level   string `json:"level"`
	Time    int64  `json:"time"`
}

// SSEEvent 实时事件
type SSEEvent struct {
	Type      string      `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// SSEHub 管理所有 SSE 客户端连接
type SSEHub struct {
	mu      sync.RWMutex
	clients map[chan SSEEvent]struct{}
}

func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[chan SSEEvent]struct{}),
	}
}

// Subscribe 注册新的 SSE 客户端，返回一个通道
func (h *SSEHub) Subscribe() chan SSEEvent {
	ch := make(chan SSEEvent, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe 移除客户端
func (h *SSEHub) Unsubscribe(ch chan SSEEvent) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

// Broadcast 向所有客户端广播事件
func (h *SSEHub) Broadcast(event SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.clients {
		select {
		case ch <- event:
		default:
			// 客户端太慢，跳过
		}
	}
}

// BroadcastLog 广播一条日志事件
func (h *SSEHub) BroadcastLog(level, module, msg string) {
	pushGlobalLog(level, module, msg)
	h.Broadcast(SSEEvent{
		Type:      "log",
		Timestamp: time.Now().UnixMilli(),
		Data: map[string]string{
			"level":  level,
			"module": module,
			"msg":    msg,
		},
	})
}

// BroadcastStatus 广播状态变更
func (h *SSEHub) BroadcastStatus(data interface{}) {
	h.Broadcast(SSEEvent{
		Type:      "status",
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	})
}

// SSEHandler 处理 SSE 连接
func (h *SSEHub) SSEHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", 500)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := h.Subscribe()
	defer h.Unsubscribe(ch)

	// 发送心跳
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	// 发送初始连接事件
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"ok\"}\n\n")
	flusher.Flush()

	for {
		select {
		case event := <-ch:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, string(data))
			flusher.Flush()

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}

// SseLogger 实现 io.Writer 接口，将日志广播到 SSE
type SseLogger struct {
	hub *SSEHub
}

func (l *SseLogger) Write(p []byte) (n int, err error) {
	msg := string(p)
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}
	l.hub.BroadcastLog("info", "system", msg)
	log.Print(msg)
	return len(p), nil
}
