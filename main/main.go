package main

import (
	"context"
	"github.com/Asolmn/tinyrpc"
	"github.com/Asolmn/tinyrpc/xclient"
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

func (f Foo) Sleep(args Args, reply *int) error {
	time.Sleep(time.Second * time.Duration(args.Num1))
	*reply = args.Num1 + args.Num2
	return nil
}

// foo 便于Call或者Broadcast之后统一打印成功或者失败的日志
func foo(xc *xclient.XClient, ctx context.Context, typ, serviceMethod string, args *Args) {
	var reply int
	var err error

	// 匹配不同的类型
	switch typ {
	case "call":
		err = xc.Call(ctx, serviceMethod, args, &reply)
	case "broadcast":
		err = xc.Broadcaset(ctx, serviceMethod, args, &reply)
	}

	// 打印日志
	if err != nil {
		log.Printf("%s %s error: %v", typ, serviceMethod, err)
	} else {
		log.Printf("%s %s success: %d + %d = %d", typ, serviceMethod, args.Num1, args.Num2, reply)
	}
}

func startServer(addr chan string) {
	// 注册Foo到Server
	var foo Foo

	// 设置一个空闲端口，创建一个网络监听器
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal("network error: ", err)
	}
	log.Println("start rpc server on", l.Addr()) // 打印网络地址

	// 创建一个server，不使用DefaultServer
	server := tinyrpc.NewServer()
	// 注册service
	if err := server.Register(&foo); err != nil {
		log.Fatal("register error: ", err)
	}

	addr <- l.Addr().String()
	server.Accept(l)

	/*
	 HTTP方式连接
	*/
	//tinyrpc.HandleHTTP()
	//addr <- l.Addr().String() // 将网络地址传入addr信道
	//_ = http.Serve(l, nil)    // 监听l上的HTTP请求

}

// call 调用单个服务实例
func call(addr1, addr2 string) {
	addrList := []string{"tcp@" + addr1, "tcp@" + addr2}

	d := xclient.NewMultiServerDiscovery(addrList)
	xc := xclient.NewXClient(d, xclient.RandomSelect, nil)

	defer func() { _ = xc.Close() }()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			foo(xc, context.Background(), "call", "Foo.Sum", &Args{Num1: i, Num2: i * i})
		}(i)
	}
	wg.Wait()
}

// broadcast 调用所有服务实例
func broadcast(addr1, addr2 string) {
	addrList := []string{"tcp@" + addr1, "tcp@" + addr2}

	d := xclient.NewMultiServerDiscovery(addrList)
	xc := xclient.NewXClient(d, xclient.RandomSelect, nil)

	defer func() { _ = xc.Close() }()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			foo(xc, context.Background(), "broadcast", "Foo.Sum", &Args{Num1: i, Num2: i * i})
			ctx, _ := context.WithTimeout(context.Background(), time.Second)
			foo(xc, ctx, "broadcast", "Foo.Sleep", &Args{Num1: i, Num2: i * i})
		}(i)
	}
	wg.Wait()
}

func main() {
	log.SetFlags(0) // 设置日志标志
	ch1 := make(chan string)
	ch2 := make(chan string)

	go startServer(ch1)
	go startServer(ch2)

	addr1 := <-ch1
	addr2 := <-ch2

	time.Sleep(time.Second)

	call(addr1, addr2)
	broadcast(addr1, addr2)
}
