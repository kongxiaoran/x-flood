package pkg

import (
	vegeta "github.com/tsenart/vegeta/lib"
	"net"
)

var GlobalWorkConfig WorkerConfig

var MasterConn net.Conn

// 定义接收的负载测试配置结构
type LoadTestConfig struct {
	Targets []TargetConfig `json:"targets"`
}

type TargetConfig struct {
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Body     string            `json:"body,omitempty"` // Body 可为空
	Header   map[string]string `json:"header,omitempty"`
	Rate     int               `json:"rate"`
	Duration int               `json:"duration"`
}

type CustomMetrics struct {
	Metrics  vegeta.Metrics `json:"metrics"`
	Target   string         `json:"target"`
	Rate     int            `json:"rate"`
	Duration int            `json:"duration"`
	Time     float64        `json:"time"`
}

type WorkerResult struct {
	Worker  string
	Metrics []CustomMetrics
	Error   error
}

// 定义配置文件结构
type WorkerConfig struct {
	Workers []WorkerInfo `json:"workers"`
}

type WorkerInfo struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	TcpPort int    `json:"tcpPort"`
	Conn    net.Conn
}

// 平均分割配置
func SplitConfig(config []TargetConfig, numWorkers int) [][]TargetConfig {
	chunkSize := len(config) / numWorkers
	remainder := len(config) % numWorkers
	var chunks [][]TargetConfig

	start := 0
	for i := 0; i < numWorkers; i++ {
		end := start + chunkSize
		if i < remainder {
			end++
		}
		chunks = append(chunks, config[start:end])
		start = end
	}

	return chunks
}
