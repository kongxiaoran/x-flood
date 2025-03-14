分布式压测工具


docker login -u tcdept-hf -p Tcdept@427 dockerhubbs.finchina.com

docker build -t x-flood:0.0.3 .

docker tag ghcr.io/open-webui/open-webui:main dockerhubbs.finchina.com/library-hf/open-webui:main
docker push dockerhubbs.finchina.com/library-hf/open-webui:main

docker tag x-flood:0.0.3 dockerhubbs.finchina.com/library-hf/x-flood:0.0.3
docker push dockerhubbs.finchina.com/library-hf/x-flood:0.0.3

docker run -p 2112:2112 -v /本机配置文件地址:/app/config.yml x-flood


部署：
分为主控节点和计算节点。主控节点只接受请求和分发任务、收集任务执行情况。计算节点负责接受主控节点分发的任务并执行，最终将执行结果返回给主控节点。
主控/计算节点使用的是一套代码，是依靠程序执行时的环境变量来区分是担任主控还是计算节点

工作节点环境变量配置：
NODE_ROLE：worker
POD_IP：status.podIP

主控节点环境变量配置：
NODE_ROLE：master
MASTER_URL: 10.10.18.188:21120
POD_IP：status.podIP


工作节点启动时，会向redis中指定key注册自己的信息。并且在运行过程中，一直定时发送心跳，以维持注册状态。主控节点是依靠redis来感知到工作节点的。