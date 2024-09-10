package pkg

import (
	"embed"
	"net/http"
)

//go:embed html/index.html
var WebContent embed.FS

func CorsMiddleware(next http.Handler) http.Handler {
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
