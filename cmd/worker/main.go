package main

import (
	"net/http"
	"x-flood/pkg"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {

	//pkg.ReadWorkerConfig("config.yml")

	// 启动文件监听
	//fmt.Println("启动文件监听:", "config.yml")
	//go func() {
	//	pkg.StartFileWatcher("config.yml")
	//}()

	go pkg.WorkerListen("2114")

	http.Handle("/", http.FileServer(http.FS(pkg.WebContent)))
	// 启动 HTTP 服务，暴露 /metrics 端点
	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/workerLoadTest", pkg.CorsMiddleware(http.HandlerFunc(pkg.WorkerLoadTestHandler)))
	http.Handle("/loadTest", pkg.CorsMiddleware(http.HandlerFunc(pkg.MasterLoadTestHandler)))
	http.ListenAndServe(":2113", nil)
}
