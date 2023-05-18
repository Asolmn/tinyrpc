package main

import (
	"encoding/json"
	"fmt"
	"github.com/Asolmn/tinyrpc"
	"github.com/Asolmn/tinyrpc/codec"
	"log"
	"net"
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

	// 客户端创建链接
	conn, _ := net.Dial("tcp", <-addr)

	defer func(conn net.Conn) { // 最后关闭链接
		_ = conn.Close()
	}(conn)

	// 睡眠1秒
	time.Sleep(time.Second)

	// 发送options，进行协议交换
	_ = json.NewEncoder(conn).Encode(tinyrpc.DefaultOption)
	cc := codec.NewGobCodec(conn)

	for i := 0; i < 5; i++ {
		// 设置编解码器的头部信息
		h := &codec.Header{
			ServiceMethod: "Foo.Sum",
			Seq:           uint64(i),
		}
		// 发送消息头h := &codec.Header{}和消息体tinyrpc req ${h.Seq}
		_ = cc.Write(h, fmt.Sprintf("tinyrpc req %d", h.Seq))
		_ = cc.ReadHeader(h) // 需要先读取请求头，否则会因为反序列化时没有安装序列化的顺序，导致解析出错

		// 解析服务器的响应并打印
		var reply string
		_ = cc.ReadBody(&reply)
		log.Println("reply:", reply)
	}

}
