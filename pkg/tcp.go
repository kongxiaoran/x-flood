package pkg

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"
)

func TcpConnectWorkNode() {
	for _, worker := range GlobalWorkConfig.Workers {
		go ConnectWorkNode(&worker)
	}
}

func ConnectWorkNode(worker *WorkerInfo) {

	// 1. 连接到工作节点，建立TCP连接
	conn, err := net.Dial("tcp", worker.Host+":"+strconv.Itoa(worker.TcpPort))
	if err != nil {
		fmt.Println("Error connecting:", err)
		reconnectWorker(worker) // 处理断线重连
	} else {
		worker.Conn = conn
	}
	fmt.Println("Connected to worker server")
	sendDataToWorker(worker, "Hello from master server")

	go func() {
		for {
			time.Sleep(time.Second * 2)
			sendDataToWorker(worker, "Hello from master server")
		}
	}()

	// 2. 启动一个goroutine，持续接收来自work服务器的数据
	stopCh := make(chan struct{})
	go func(nodeInfo *WorkerInfo, stop <-chan struct{}) {
		defer nodeInfo.Conn.Close() // 确保连接在退出时关闭
		for {
			select {
			case <-stop:
				return // 结束goroutine
			default:
				message, err := bufio.NewReader(nodeInfo.Conn).ReadString('\n')
				if err != nil {
					log.Println("Error reading message:", err)
					reconnectWorker(nodeInfo) // 处理断线重连
					return                    // 结束该goroutine
				}
				log.Print("Message from" + nodeInfo.Name + " : " + message)
			}
		}
	}(worker, stopCh)
}

// 新增的函数，服务端发送数据到工作节点
func sendDataToWorker(worker *WorkerInfo, data string) {
	if worker.Conn != nil {
		_, err := worker.Conn.Write([]byte(data + "\n"))
		if err != nil {
			log.Printf("Failed to send data to %s: %v", worker.Name, err)
		} else {
			log.Printf("Data sent to worker %s: %s", worker.Name, data)
		}
	} else {
		log.Printf("No connection to worker %s,准备尝试重连", worker.Name)
		reconnectWorker(worker)
	}
}

func reconnectWorker(nodeInfo *WorkerInfo) {
	maxRetries := 100                // 最大重试次数
	retryInterval := 5 * time.Second // 每次重试的间隔
	currentRetry := 0                // 当前的重试次数

	for currentRetry < maxRetries {
		// 尝试重新连接
		conn, err := net.Dial("tcp", nodeInfo.Host+":"+strconv.Itoa(nodeInfo.TcpPort))
		if err == nil {
			nodeInfo.Conn = conn
			fmt.Println("Reconnected to server")
			return
		}
		log.Println("尝试重连到服务器:"+nodeInfo.Name+" 失败，第", currentRetry, "次重试，等待", retryInterval, "秒后重试")
		// 如果连接失败，等待一段时间后重试
		currentRetry++
		time.Sleep(retryInterval)
	}
	log.Println("Failed to reconnect to server after", maxRetries, "attempts for ", nodeInfo.Name)
}

func WorkerListen(port string) {
	// 开始监听指定端口
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Error starting TCP listener on port %s: %v", port, err)
	}
	defer listener.Close()

	log.Printf("Listening on TCP port %s", port)

	for {
		// 等待并接受客户端连接
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}

		// 处理连接
		go HandleConnection(conn)
	}
}

// 处理TCP连接的函数
func HandleConnection(conn net.Conn) {
	defer conn.Close()
	log.Printf("Client connected from %s", conn.RemoteAddr())

	// 使用 bufio 读取客户端发送的消息
	reader := bufio.NewReader(conn)
	for {
		message, err := reader.ReadString('\n')
		if err != nil {
			log.Printf("Error reading from connection: %v", err)
			return
		}

		// 打印接收到的消息
		log.Printf("Received message: %s", message)
	}
}
