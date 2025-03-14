package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	vegeta "github.com/tsenart/vegeta/lib"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// 定义 Prometheus 指标
var (
	requestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "x_flood_requests_total",
			Help: "Total number of HTTP requests made by X-Flood.",
		},
		[]string{"status", "endpoint"},
	)

	latencyHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "x_flood_request_latency_seconds",
			Help:    "Histogram of latencies for requests.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"endpoint"},
	)

	qpsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "x_flood_qps",
			Help: "Requests per second (QPS) for each endpoint.",
		},
		[]string{"endpoint"},
	)
)

// 定义接收的负载测试配置结构
type LoadTestConfig struct {
	Targets []TargetConfig `json"targets"`
}

type TargetConfig struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Body            string            `json:"body,omitempty"` // Body 可为空
	Header          map[string]string `json:"header,omitempty"`
	Rate            int               `json:"rate"`
	Duration        int               `json:"duration"`
	CheckReturnCode bool              `json:"checkReturnCode"`
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

// 平均分割配置
func splitConfig(config []TargetConfig, numWorkers int) [][]TargetConfig {
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

func init() {
	// 注册 Prometheus 指标
	prometheus.MustRegister(requestCount)
	prometheus.MustRegister(latencyHistogram)
	prometheus.MustRegister(qpsGauge)
}

//go:embed html/index.html
var content embed.FS

var (
	// 节点信息的TTL时间
	nodeTTL = 30 * time.Second
	// 当前节点的角色
	nodeRole string
	// 当前节点的ID
	nodeID string
	// 服务注册中心实例
	registry *Registry
)

// 节点类型常量
const (
	WorkerNodesKey = "workers" // 存储所有worker节点
	MasterNodesKey = "masters" // 存储所有master节点
)

// 节点信息结构
type NodeInfo struct {
	ID       string    `json:"id"`
	Role     string    `json:"role"`
	URL      string    `json:"url"`
	LastSeen time.Time `json:"last_seen"`
}

func initRegistry() {
	// 从环境变量获取节点角色和ID
	nodeRole = os.Getenv("NODE_ROLE")
	if nodeRole == "" {
		nodeRole = "worker"
	}

	nodeID = os.Getenv("POD_NAME")
	if nodeID == "" {
		nodeID = fmt.Sprintf("%s-%s", nodeRole, getEnv("POD_IP", time.Now().Format("20060102150405")))
	}

	// 只有 master 节点创建注册中心
	if nodeRole == "master" {
		registry = NewRegistry(nodeTTL)
		// 添加注册中心的 HTTP 处理器
		http.Handle("/register", corsMiddleware(registry))
	}
}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// 注册节点到Redis
func registerNode() {
	nodeInfo := &NodeInfo{
		ID:       nodeID,
		Role:     nodeRole,
		URL:      fmt.Sprintf("http://%s:%s", getEnv("POD_IP", "localhost"), getEnv("PORT", "2112")),
		LastSeen: time.Now(),
	}

	if nodeRole == "master" {
		// master节点不需要注册自己
		return
	}

	// worker节点通过HTTP注册到master
	masterURL := getEnv("MASTER_URL", "")
	if masterURL == "" {
		fmt.Println("Error: MASTER_URL environment variable is required for worker nodes")
		os.Exit(1)
	}
	// 确保masterURL包含协议前缀
	if !strings.HasPrefix(masterURL, "http://") && !strings.HasPrefix(masterURL, "https://") {
		masterURL = "http://" + masterURL
	}
	// 创建注册客户端
	client := NewRegistryClient(masterURL, nodeInfo, nodeTTL)
	// 注册并开始心跳
	if err := client.Register(); err != nil {
		fmt.Printf("Failed to register with master: %v\n", err)
		os.Exit(1)
	}
	client.StartHeartbeat()
}

func main() {
	// 初始化注册中心
	initRegistry()

	// 注册节点
	registerNode()

	// 根据节点角色启动不同的处理器
	if nodeRole == "master" {
		http.Handle("/loadTest", corsMiddleware(http.HandlerFunc(masterLoadTestHandler)))
		http.Handle("/workers", corsMiddleware(http.HandlerFunc(getWorkersHandler)))
	} else {
		http.Handle("/workerLoadTest", corsMiddleware(http.HandlerFunc(workerLoadTestHandler)))
	}

	http.Handle("/test", corsMiddleware(http.HandlerFunc(test)))

	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/", http.FileServer(http.FS(content)))

	port := os.Getenv("PORT")
	if port == "" {
		port = "2112"
	}

	http.ListenAndServe(":"+port, nil)
}

func test(w http.ResponseWriter, _ *http.Request) {
	var buf bytes.Buffer
	time.Sleep(40 * time.Millisecond)
	fmt.Fprintf(&buf, "Hello, %s", "world")
	w.Write(buf.Bytes())
}

// 主节点：接收负载测试请求，分发给多个工作节点，汇总结果
func masterLoadTestHandler(w http.ResponseWriter, r *http.Request) {
	if nodeRole != "master" {
		http.Error(w, "This node is not a master", http.StatusForbidden)
		return
	}

	fmt.Println("Received load test request. Start task")
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析请求体中的 JSON
	var config LoadTestConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// 获取可用的worker节点
	workers := getAvailableWorkers()
	if len(workers) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "没有可用的工作节点"})
		return
	}

	// 分割任务
	workerConfigs := splitConfig(config.Targets, len(workers))

	var wg sync.WaitGroup
	resultChan := make(chan WorkerResult, len(workers))

	// 分发任务给worker
	for i := range workers {
		wg.Add(1)
		go func(worker NodeInfo, workerConfig []TargetConfig) {
			defer wg.Done()
			metrics, err := sendLoadTestToWorker(worker.URL, LoadTestConfig{
				Targets: workerConfig,
			})
			resultChan <- WorkerResult{Worker: worker.ID, Metrics: metrics, Error: err}
		}(workers[i], workerConfigs[i])
	}

	// 收集所有结果
	var allMetrics []CustomMetrics

	// 等待所有 goroutines 完成
	wg.Wait()

	// 收集所有结果
	for range workers {
		result := <-resultChan
		if result.Error != nil {
			http.Error(w, fmt.Sprintf("Failed to contact worker %s: %v", result.Worker, result.Error), http.StatusInternalServerError)
			return
		}
		allMetrics = append(allMetrics, result.Metrics...)
	}

	// 返回汇总的测试结果
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(allMetrics); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
	fmt.Println("task end")
}

// 获取可用的worker节点
func getAvailableWorkers() []NodeInfo {
	// 从注册中心获取所有可用的worker节点
	return registry.GetNodes(WorkerNodesKey)
}

// 将负载测试请求发送到工作节点
func sendLoadTestToWorker(workerURL string, config LoadTestConfig) ([]CustomMetrics, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(workerURL+"/workerLoadTest", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 读取错误响应内容以便调试
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("worker returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var metrics []CustomMetrics
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		// 读取响应内容以便调试
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to decode response: %v, response body: %s", err, string(bodyBytes))
	}

	return metrics, nil
}

// 工作节点：接收负载测试请求，执行测试并返回结果
func workerLoadTestHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received load test request from master node")
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析请求体中的 JSON
	var config LoadTestConfig
	if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}
	fmt.Println(config)
	// 执行负载测试
	metricsList := startLoadTest(config)

	// 将测试结果返回主节点
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metricsList); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
	fmt.Println("work test completed")
}

// 根据外部传入的配置启动负载测试
func startLoadTest(config LoadTestConfig) []CustomMetrics {
	attacker := vegeta.NewAttacker()
	metricsList := []CustomMetrics{}

	for _, targetConfig := range config.Targets {
		target := vegeta.Target{
			Method: targetConfig.Method,
			URL:    targetConfig.URL,
			Body:   []byte(targetConfig.Body),
			Header: convertHeader(targetConfig.Header),
		}
		metrics := attackTarget(attacker, target, targetConfig.Rate, targetConfig.Duration, targetConfig)
		metricsList = append(metricsList, metrics)
	}
	return metricsList
}

func convertHeader(header map[string]string) map[string][]string {
	converted := make(map[string][]string)
	for key, value := range header {
		converted[key] = []string{value}
	}
	// 如果没有设置 Content-Type，且有请求体，则默认设置为 application/json
	if _, hasContentType := converted["Content-Type"]; !hasContentType {
		converted["Content-Type"] = []string{"application/json"}
	}
	return converted
}

func attackTarget(attacker *vegeta.Attacker, target vegeta.Target, rate int, duration int, targetConfig TargetConfig) CustomMetrics {
	// 如果是 POST 请求，添加 Content-Type header
	if target.Method == "POST" && target.Body != nil {
		if target.Header == nil {
			target.Header = make(map[string][]string)
		}
		// 设置 Content-Type 为 application/json
		target.Header["Content-Type"] = []string{"application/json"}
	}

	// 创建 Targeter
	targeter := vegeta.NewStaticTargeter(target)

	// 统计接口的 QPS
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var metrics vegeta.Metrics
	requestsPerSecond := 0

	startTime := time.Now()
	// 使用 Attack 返回的通道
	results := attacker.Attack(targeter, vegeta.Rate{Freq: rate, Per: time.Second}, time.Duration(duration)*time.Second, "LoadTest")

	for {
		select {
		case <-ticker.C:
			qpsGauge.WithLabelValues(target.URL).Set(float64(requestsPerSecond))
			requestsPerSecond = 0
		case res, ok := <-results:
			if !ok {
				fmt.Println("Attack finished for", target.URL)
				metrics.Close()
				metrics.Latencies.Total = metrics.Latencies.Total / 1000000
				metrics.Latencies.Mean = metrics.Latencies.Mean / 1000000
				metrics.Latencies.P50 = metrics.Latencies.P50 / 1000000
				metrics.Latencies.P95 = metrics.Latencies.P95 / 1000000
				metrics.Latencies.P99 = metrics.Latencies.P99 / 1000000
				metrics.Latencies.Max = metrics.Latencies.Max / 1000000

				myMetrics := CustomMetrics{
					Metrics:  metrics,
					Target:   target.URL,
					Rate:     rate,
					Duration: duration,
					Time:     time.Since(startTime).Seconds(),
				}

				return myMetrics
			}
			// 如果需要检查returncode，解析响应体并检查returncode字段
			if targetConfig.CheckReturnCode && res.Code == http.StatusOK {
				var response struct {
					ReturnCode int `json:"returncode"`
				}
				response.ReturnCode = -1

				if err := json.Unmarshal(res.Body, &response); err != nil {
					fmt.Printf("Failed to parse response body: %v\n", err)
					res.Code = http.StatusBadRequest
				} else if response.ReturnCode != 0 {
					res.Code = http.StatusBadRequest // 将非0的returncode视为请求失败
					fmt.Printf("接口返回的returncode非0: %d\n", response.ReturnCode)
				}
			}

			metrics.Add(res)

			// 记录每个接口的状态码、延迟、QPS
			status := fmt.Sprintf("%d", res.Code)
			requestCount.WithLabelValues(status, target.URL).Inc()

			latency := res.Latency.Seconds()
			latencyHistogram.WithLabelValues(target.URL).Observe(latency)

			requestsPerSecond++
		}
	}
}

// 获取所有工作节点信息的处理器
func getWorkersHandler(w http.ResponseWriter, r *http.Request) {
	if nodeRole != "master" {
		http.Error(w, "This node is not a master", http.StatusForbidden)
		return
	}

	workers := getAvailableWorkers()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(workers); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")                   // 允许所有来源，生产环境中应限制特定来源
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS") // 允许的方法
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")       // 允许的请求头

		// 如果是 OPTIONS 请求，直接返回
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// 继续处理非 OPTIONS 请求
		next.ServeHTTP(w, r)
	})
}
