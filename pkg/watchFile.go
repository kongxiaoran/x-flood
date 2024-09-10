package pkg

import (
	"encoding/json"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"io/ioutil"
	"log"
	"os"
	"time"
)

// 读取配置文件
func ReadWorkerConfig(filename string) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return
	}

	if err := json.Unmarshal(data, &GlobalWorkConfig); err != nil {
		return
	}
	fmt.Println("加载工作节点配置成功")
	fmt.Println(GlobalWorkConfig)
}

func StartFileWatcher(configPath string) {
	// 创建一个新的Watcher实例
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	// 使用goroutine来处理接收到的事件
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// 检测事件类型
				if event.Op&fsnotify.Write == fsnotify.Write {
					fmt.Println("文件被修改,重新加载配置文件:", event.Name)
					// 当文件被修改时触发的函数
					ReadWorkerConfig(configPath)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	// 监控文件是否存在，如果文件不存在则不断尝试添加
	go func() {
		for {
			// 检查文件是否存在
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				// 文件不存在，打印警告并等待一段时间
				log.Printf("文件 %s 不存在，等待创建...", configPath)
				time.Sleep(5 * time.Second) // 等待 5 秒后重试
				continue
			}

			// 尝试添加文件到监控列表中
			err := watcher.Add(configPath)
			if err != nil {
				log.Printf("添加文件监控失败: %v", err)
			} else {
				log.Printf("开始监控文件: %s", configPath)
				break
			}
		}
	}()

	// 阻止程序退出
	<-make(chan struct{})
}
