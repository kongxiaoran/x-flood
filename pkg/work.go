package pkg

import (
	"encoding/json"
	"fmt"
	vegeta "github.com/tsenart/vegeta/lib"
	"net/http"
	"strconv"
	"time"
)

type TaskProcess struct {
	Method   string            `json:"method"`
	URL      string            `json:"url"`
	Body     string            `json:"body,omitempty"` // Body 可为空
	Header   map[string]string `json:"header,omitempty"`
	Rate     int               `json:"rate"`
	Duration int               `json:"duration"`
}

// 工作节点：接收负载测试请求，执行测试并返回结果
func WorkerLoadTestHandler(w http.ResponseWriter, r *http.Request) {
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
	metricsList := StartLoadTest(config)

	// 将测试结果返回主节点
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metricsList); err != nil {
		http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
	}
	fmt.Println("worker test completed")
}

func AttackTarget(attacker *vegeta.Attacker, target vegeta.Target, rate int, duration int) CustomMetrics {
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
	sum := 0
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
			sum++
			//sendDataToMaster(MasterConn, "本次任务 完成请求次数:"+strconv.Itoa(sum))

			// 记录每个接口的状态码、延迟、QPS
			status := fmt.Sprintf("%d", res.Code)
			requestCount.WithLabelValues(status, target.URL).Inc()

			latency := res.Latency.Seconds()
			latencyHistogram.WithLabelValues(target.URL).Observe(latency)

			requestsPerSecond++
		}
	}
}

// 根据外部传入的配置启动负载测试
func StartLoadTest(config LoadTestConfig) []CustomMetrics {
	attacker := vegeta.NewAttacker()
	metricsList := []CustomMetrics{}

	for i, targetConfig := range config.Targets {
		target := vegeta.Target{
			Method: targetConfig.Method,
			URL:    targetConfig.URL,
			Body:   []byte(targetConfig.Body),
			Header: convertHeader(targetConfig.Header),
		}
		metrics := AttackTarget(attacker, target, targetConfig.Rate, targetConfig.Duration)
		metricsList = append(metricsList, metrics)
		sendDataToMaster(MasterConn, "测试 工作节点任务执行："+strconv.Itoa(i))
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
