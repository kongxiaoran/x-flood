package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	vegeta "github.com/tsenart/vegeta/lib"
	"io/ioutil"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// 定义 Prometheus 指标
var (
	requestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vegeta_requests_total",
			Help: "Total number of HTTP requests made by Vegeta.",
		},
		[]string{"status", "endpoint"},
	)

	latencyHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "vegeta_request_latency_seconds",
			Help:    "Histogram of latencies for requests.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"endpoint"},
	)

	qpsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vegeta_qps",
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

var workerConfig WorkerConfig

// 定义配置文件结构
type WorkerConfig struct {
	Workers []string `json:"workers"`
}

//go:embed html/index.html
var content embed.FS

func main() {

	readWorkerConfig("/app/config.yml")

	// 启动文件监听
	fmt.Println("启动文件监听:", "/app/config.yml")
	go func() {
		StartFileWatcher("/app/config.yml")
	}()

	http.Handle("/", http.FileServer(http.FS(content)))
	// 启动 HTTP 服务，暴露 /metrics 端点
	http.Handle("/metrics", promhttp.Handler())

	http.Handle("/workerLoadTest", corsMiddleware(http.HandlerFunc(workerLoadTestHandler)))
	http.Handle("/loadTest", corsMiddleware(http.HandlerFunc(masterLoadTestHandler)))
	http.Handle("/test", corsMiddleware(http.HandlerFunc(test)))

	http.ListenAndServe(":2112", nil)
}

func test(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	time.Sleep(40 * time.Millisecond)
	// 模拟写入到http.ResponseWriter
	fmt.Fprintf(&buf, "Hello, %s", "world")
	w.Write([]byte(buf.String()))
}

// 读取配置文件
func readWorkerConfig(filename string) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}

	if err := json.Unmarshal(data, &workerConfig); err != nil {
		return
	}
	fmt.Println("加载工作节点配置成功")
	fmt.Println(workerConfig)
}

// 主节点：接收负载测试请求，分发给多个工作节点，汇总结果
func masterLoadTestHandler(w http.ResponseWriter, r *http.Request) {
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

	// 分发任务给工作节点
	var allMetrics []CustomMetrics

	// 使用 waitgroup 来等待所有 goroutines 完成
	var wg sync.WaitGroup
	resultChan := make(chan WorkerResult, len(workerConfig.Workers))

	// 分割配置
	workerConfigs := splitConfig(config.Targets, len(workerConfig.Workers))

	for i, worker := range workerConfig.Workers {
		wg.Add(1)
		go func(worker string, workerConfig []TargetConfig) {
			defer wg.Done()
			metrics, err := sendLoadTestToWorker(worker, LoadTestConfig{
				Targets: workerConfig,
			})
			resultChan <- WorkerResult{Worker: worker, Metrics: metrics, Error: err}
		}(worker, workerConfigs[i])
	}
	// 等待所有 goroutines 完成
	wg.Wait()

	// 收集所有结果
	for range workerConfig.Workers {
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

// 将负载测试请求发送到工作节点
func sendLoadTestToWorker(workerURL string, config LoadTestConfig) ([]CustomMetrics, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post(workerURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("worker returned status %d", resp.StatusCode)
	}

	var metrics []CustomMetrics
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		return nil, err
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
