package pkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
)

// 主节点：接收负载测试请求，分发给多个工作节点，汇总结果
func MasterLoadTestHandler(w http.ResponseWriter, r *http.Request) {
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
	resultChan := make(chan WorkerResult, len(GlobalWorkConfig.Workers))

	// 分割配置
	workerConfigs := SplitConfig(config.Targets, len(GlobalWorkConfig.Workers))

	for i, worker := range GlobalWorkConfig.Workers {
		wg.Add(1)
		go func(workerHost WorkerInfo, workerConfig []TargetConfig) {
			defer wg.Done()
			metrics, err := SendLoadTestToWorker(workerHost, LoadTestConfig{
				Targets: workerConfig,
			})
			resultChan <- WorkerResult{Worker: worker.Name, Metrics: metrics, Error: err}
		}(worker, workerConfigs[i])
	}
	// 等待所有 goroutines 完成
	wg.Wait()

	// 收集所有结果
	for range GlobalWorkConfig.Workers {
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
func SendLoadTestToWorker(worker WorkerInfo, config LoadTestConfig) ([]CustomMetrics, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	resp, err := http.Post("http://"+worker.Host+":"+strconv.Itoa(worker.Port)+"/workerLoadTest", "application/json", bytes.NewBuffer(body))
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
