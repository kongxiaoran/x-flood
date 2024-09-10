分布式压测工具


docker build -t x-flood:0.0.1 .


docker tag x-flood:0.0.1 10.15.98.150/library-hf/x-flood:0.0.1
docker push 10.15.98.150/library-hf/x-flood:0.0.1

docker run -p 2112:2112 -v /本机配置文件地址:/app/config.yml x-flood