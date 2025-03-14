package main

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Registry 是一个内存中的服务注册中心
type Registry struct {
	mu    sync.RWMutex
	nodes map[string]map[string]*NodeInfo // key -> nodeID -> nodeInfo
	ttl   time.Duration
}

// NewRegistry 创建一个新的注册中心实例
func NewRegistry(ttl time.Duration) *Registry {
	r := &Registry{
		nodes: make(map[string]map[string]*NodeInfo),
		ttl:   ttl,
	}

	// 启动定期清理过期节点的goroutine
	go r.cleanupLoop()

	return r
}

// Register 注册节点
func (r *Registry) Register(key string, nodeInfo *NodeInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 确保key对应的map存在
	if _, exists := r.nodes[key]; !exists {
		r.nodes[key] = make(map[string]*NodeInfo)
	}

	// 更新节点信息
	r.nodes[key][nodeInfo.ID] = nodeInfo
	return nil
}

// GetNodes 获取指定key下的所有有效节点
func (r *Registry) GetNodes(key string) []NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var nodes []NodeInfo
	if nodeMap, exists := r.nodes[key]; exists {
		for _, node := range nodeMap {
			// 只返回未过期的节点
			if time.Since(node.LastSeen) <= r.ttl {
				nodes = append(nodes, *node)
			}
		}
	}

	return nodes
}

// cleanupLoop 定期清理过期节点
func (r *Registry) cleanupLoop() {
	ticker := time.NewTicker(r.ttl / 2)
	defer ticker.Stop()

	for range ticker.C {
		r.cleanup()
	}
}

// ServeHTTP 处理节点注册请求
func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var nodeInfo NodeInfo
	if err := json.NewDecoder(req.Body).Decode(&nodeInfo); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	key := WorkerNodesKey
	if nodeInfo.Role == "master" {
		key = MasterNodesKey
	}

	if err := r.Register(key, &nodeInfo); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// cleanup 清理所有过期的节点
func (r *Registry) cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for key, nodeMap := range r.nodes {
		for nodeID, node := range nodeMap {
			if time.Since(node.LastSeen) > r.ttl {
				delete(nodeMap, nodeID)
			}
		}
		// 如果该key下没有节点了，删除这个key
		if len(nodeMap) == 0 {
			delete(r.nodes, key)
		}
	}
}