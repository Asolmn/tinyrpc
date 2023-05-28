package main

import (
	"fmt"
	"github.com/Asolmn/tinyrpc"
	"log"
	"net"
	"sync"
	"time"
)

func startServer(addr chan string) {
	// 设置一个空闲端口，创建一个网络监听器
	l, err := net.Listen("tcp", ":5000")
	if err != nil {
		log.Fatal("network error: ", err)
	}
	// 打印网络地址
	log.Println("start rpc server on", l.Addr())
	// 将网络地址传入addr信道
	addr <- l.Addr().String()
	// 开始监听
	tinyrpc.Accept(l)
}

func main() {
	// 创建一个信道
	addr := make(chan string)
	// 启动服务
	go startServer(addr)

	// 创建客户端连接
	client, _ := tinyrpc.Dial("tcp", <-addr)
	defer func() { _ = client.Close() }()

	time.Sleep(time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {

		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf("tinyrpc req %d", i)

			var reply string
			// 调用Call并发5个RPC同步调用
			if err := client.Call("Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}

			log.Println("reply:", reply)
		}(i)
	}
	wg.Wait()
}
