package main

import (
	"github.com/Asolmn/tinyrpc"
	"log"
	"net"
	"sync"
	"time"
)

type Foo int

type Args struct {
	Num1, Num2 int
}

func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func startServer(addr chan string) {
	// 注册Foo到Server
	var foo Foo
	if err := tinyrpc.Register(&foo); err != nil {
		log.Fatal("register error: ", err)
	}

	// 设置一个空闲端口，创建一个网络监听器
	l, err := net.Listen("tcp", ":5000")
	if err != nil {
		log.Fatal("network error: ", err)
	}

	log.Println("start rpc server on", l.Addr()) // 打印网络地址
	addr <- l.Addr().String()                    // 将网络地址传入addr信道
	tinyrpc.Accept(l)                            // 开始监听
}

func main() {
	log.SetFlags(0)           // 设置日志标志
	addr := make(chan string) // 创建一个信道
	go startServer(addr)      // 启动服务

	// 创建客户端连接
	client, _ := tinyrpc.Dial("tcp", <-addr)
	defer func() { _ = client.Close() }()

	time.Sleep(time.Second)

	// 发送请求和接受响应
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {

		wg.Add(1)

		go func(i int) {
			defer wg.Done()
			args := &Args{Num1: i, Num2: i * i}

			var reply int
			if err := client.Call("Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}

			log.Printf("%d + %d = %d:", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait()
}
