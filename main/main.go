package main

import (
	"context"
	"github.com/Asolmn/tinyrpc"
	"github.com/Asolmn/tinyrpc/registry"
	"github.com/Asolmn/tinyrpc/xclient"
	"log"
	"net"
	"net/http"
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

// 启动注册中心
func startRegistry(wg *sync.WaitGroup) {
	l, _ := net.Listen("tcp", ":9000")

	// 使用默认的DefaultServeMux
	registry.HandleHTTP()
	wg.Done()
	_ = http.Serve(l, nil)
}

func startServer(registryAddr string, wg *sync.WaitGroup) {
	var foo Foo
	l, _ := net.Listen("tcp", ":0")
	server := tinyrpc.NewServer()

	// 向服务端注册服务
	_ = server.Register(&foo)

	// 服务端向注册中心发送心跳
	registry.Heartbeat(registryAddr, "tcp@"+l.Addr().String(), 0)
	wg.Done()
	server.Accept(l)

}

// call 调用单个服务实例
func call(registry string) {

	d := xclient.NewTinyRegistryDiscovery(registry, 0)
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
func broadcast(registry string) {

	d := xclient.NewTinyRegistryDiscovery(registry, 0)
	xc := xclient.NewXClient(d, xclient.RandomSelect, nil)

	defer func() { _ = xc.Close() }()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			foo(xc, context.Background(), "broadcast", "Foo.Sum", &Args{Num1: i, Num2: i * i})
			//ctx, _ := context.WithTimeout(context.Background(), time.Second)
			//foo(xc, ctx, "broadcast", "Foo.Sleep", &Args{Num1: i, Num2: i * i})
		}(i)
	}
	wg.Wait()
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

func main() {
	log.SetFlags(0) // 设置日志标志

	registryAddr := "http://localhost:9000/_tinyrpc_/registry"

	var wg sync.WaitGroup

	wg.Add(1)
	go startRegistry(&wg)
	wg.Wait()

	time.Sleep(time.Second)
	wg.Add(2)
	go startServer(registryAddr, &wg)
	go startServer(registryAddr, &wg)
	wg.Wait()

	time.Sleep(time.Second)
	call(registryAddr)
	broadcast(registryAddr)

}
