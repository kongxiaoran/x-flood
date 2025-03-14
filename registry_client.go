package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// RegistryClient 是一个用于与注册中心通信的客户端
type RegistryClient struct {
	masterURL string
	nodeInfo  *NodeInfo
	ttl       time.Duration
}

// NewRegistryClient 创建一个新的注册中心客户端实例
func NewRegistryClient(masterURL string, nodeInfo *NodeInfo, ttl time.Duration) *RegistryClient {
	return &RegistryClient{
		masterURL: masterURL,
		nodeInfo:  nodeInfo,
		ttl:       ttl,
	}
}

// Register 向注册中心注册节点
func (c *RegistryClient) Register() error {
	data, err := json.Marshal(c.nodeInfo)
	if err != nil {
		return fmt.Errorf("marshal node info failed: %v", err)
	}

	resp, err := http.Post(fmt.Sprintf("%s/register", c.masterURL), "application/json", bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("register to master failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("register failed with status code: %d", resp.StatusCode)
	}

	return nil
}

// StartHeartbeat 开始定期发送心跳
func (c *RegistryClient) StartHeartbeat() {
	ticker := time.NewTicker(c.ttl / 2)
	go func() {
		for range ticker.C {
			c.nodeInfo.LastSeen = time.Now()
			if err := c.Register(); err != nil {
				fmt.Printf("Failed to send heartbeat: %v\n", err)
			}
		}
	}()
}