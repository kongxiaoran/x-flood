package main

import (
	"fmt"
	"github.com/fsnotify/fsnotify"
	"log"
)

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
					readWorkerConfig(configPath)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Println("error:", err)
			}
		}
	}()

	// 添加需要监控的文件或目录
	err = watcher.Add(ConfigPath)
	if err != nil {
		log.Fatal(err)
	}
	// 阻止程序退出
	<-make(chan struct{})
}
