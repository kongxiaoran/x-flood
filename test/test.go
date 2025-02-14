package test

import (
	"fmt"
	"net/http"
	"time"
)

// 处理 HTTP GET 请求的函数
func sleepHandler(w http.ResponseWriter, r *http.Request) {
	// 模拟休眠2秒
	time.Sleep(2 * time.Second)

	// 返回响应
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Request completed after 2 seconds sleep")
}

func main() {
	// 定义路由
	http.HandleFunc("/sleep", sleepHandler)

	// 启动 HTTP 服务器
	fmt.Println("Starting server on :8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("Error starting server: %s\n", err)
	}
}
