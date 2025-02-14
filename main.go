package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"context"
	vegeta "github.com/tsenart/vegeta/lib"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
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

// Redis配置
var (
	redisClient *redis.Client
	ctx         = context.Background()
	// 节点信息的TTL时间
	nodeTTL = 30 * time.Second
	// 当前节点的角色
	nodeRole string
	// 当前节点的ID
	nodeID string
)

// Redis中使用的键
const (
	WorkerNodesKey = "x-flood:workers" // Hash存储所有worker节点
	MasterNodesKey = "x-flood:masters" // Hash存储所有master节点
)

// 节点信息结构
type NodeInfo struct {
	ID       string    `json:"id"`
	Role     string    `json:"role"`
	URL      string    `json:"url"`
	LastSeen time.Time `json:"last_seen"`
}

func initRedis() {
	redisHost := os.Getenv("REDIS_HOST")
	if redisHost == "" {
		redisHost = "localhost:6379"
	}

	redisClient = redis.NewClient(&redis.Options{
		Addr:     redisHost,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})

	// 从环境变量获取节点角色和ID
	nodeRole = os.Getenv("NODE_ROLE")
	if nodeRole == "" {
		nodeRole = "worker"
	}

	nodeID = os.Getenv("POD_NAME")
	if nodeID == "" {
		nodeID = fmt.Sprintf("%s-%s", nodeRole, getEnv("POD_IP", time.Now().Format("20060102150405")))
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
	nodeInfo := NodeInfo{
		ID:       nodeID,
		Role:     nodeRole,
		URL:      fmt.Sprintf("http://%s:%s", getEnv("POD_IP", "localhost"), getEnv("PORT", "2112")),
		LastSeen: time.Now(),
	}

	key := WorkerNodesKey
	if nodeRole == "master" {
		key = MasterNodesKey
	}

	// 定期更新节点存活状态
	go func() {
		ticker := time.NewTicker(nodeTTL / 2)
		for range ticker.C {
			// 更新 LastSeen 时间
			nodeInfo.LastSeen = time.Now()
			nodeData, _ := json.Marshal(nodeInfo)

			// 更新当前节点的状态
			redisClient.HSet(ctx, key, nodeID, nodeData)

			// 清理过期的节点
			cleanExpiredNodes(key)
		}
	}()
}

// 清理过期节点
func cleanExpiredNodes(key string) {
	result := redisClient.HGetAll(ctx, key)
	if result.Err() != nil {
		return
	}

	for id, value := range result.Val() {
		var nodeInfo NodeInfo
		if err := json.Unmarshal([]byte(value), &nodeInfo); err != nil {
			continue
		}

		// 如果节点超过 TTL 时间没有更新，则删除该节点
		if time.Since(nodeInfo.LastSeen) > nodeTTL {
			redisClient.HDel(ctx, key, id)
		}
	}
}

func main() {
	// 初始化Redis连接
	initRedis()

	// 注册节点
	registerNode()

	// 根据节点角色启动不同的处理器
	if nodeRole == "master" {
		http.Handle("/loadTest", corsMiddleware(http.HandlerFunc(masterLoadTestHandler)))
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
		http.Error(w, "No available workers", http.StatusServiceUnavailable)
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
	var workers []NodeInfo

	// 从Redis获取所有worker节点
	result := redisClient.HGetAll(ctx, WorkerNodesKey)
	if result.Err() != nil {
		return workers
	}

	for _, value := range result.Val() {
		var nodeInfo NodeInfo
		if err := json.Unmarshal([]byte(value), &nodeInfo); err != nil {
			continue
		}
		workers = append(workers, nodeInfo)
		// 检查节点是否存活
		//if time.Since(nodeInfo.LastSeen) < nodeTTL {
		//	workers = append(workers, nodeInfo)
		//}
	}

	return workers
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
		metrics := attackTarget(attacker, target, targetConfig.Rate, targetConfig.Duration)
		metricsList = append(metricsList, metrics)
	}
	return metricsList
}

func convertHeader(header map[string]string) map[string][]string {
	// 将单一键值对的 map 转换为 Vegeta 所需的 map[string][]string 类型
	converted := make(map[string][]string)
	for key, value := range header {
		converted[key] = []string{value}
	}
	return converted
}

func attackTarget(attacker *vegeta.Attacker, target vegeta.Target, rate int, duration int) CustomMetrics {
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
