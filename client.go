package tinyrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Asolmn/tinyrpc/codec"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Call 为call操作的实例
// call指的是客户端应用程序向服务器应用程序发送请求并等待响应的操作
type Call struct {
	Seq           uint64      // 请求序列号
	ServiceMethod string      // <service>.<method>
	Args          interface{} // 函数参数
	Reply         interface{} // 函数的回复
	Error         error       // 如果发生错误，则进行设置
	Done          chan *Call  // 调用结束时，使用call.done()通知调用方
}

func (call *Call) done() {
	call.Done <- call
}

// Client Client表示RPC客户端。
// 可能有多个未完成的调用与一个客户端相关
// 一个客户端可能有多个未完成的调用，并且一个客户端可能被多个程序同时使用。
// 同时执行多个goroutine。
type Client struct {
	cc      codec.Codec      // 消息编解码器，与服务端类似，用于序列化将要发送出去的请求, 以及反序列化接受到的响应
	opt     *Option          // 协商信息
	sending sync.Mutex       // 发送互斥锁，保证请求的有序发送，防止多个请求报文混淆
	header  codec.Header     // 每个请求的消息头
	mu      sync.Mutex       // Client实例互斥
	seq     uint64           // 用于给发送的请求编号
	pending map[uint64]*Call // 存储未处理完的请求

	// closing, shutdown任意一个值为true，则表示Client处于不可用状态
	// closing是用户主动关闭，即调用Close()
	// shutdown则是一般有错误发生
	closing  bool
	shutdown bool
}

// 检查Client是否有Closer方法
var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("connection is shut down")

// Close 关闭链接
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()

	// 若已经关闭,返回ErrShutdown
	if client.closing {
		return ErrShutdown
	}

	client.closing = true
	return client.cc.Close()
}

// IsAvailable 如果客户端确实工作，则IsAvailable返回true
func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()

	return !client.shutdown && !client.closing
}

// 将call添加到client.pending中，并更新client.seq
func (client *Client) registerCall(call *Call) (uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()

	// 检查关闭和错误情况
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}

	// 设置call请求编号
	call.Seq = client.seq
	// 添加到未处理完的请求队列中，以请求编号作为键
	client.pending[call.Seq] = call
	client.seq++
	// 返回call的请求编号和nil
	return call.Seq, nil
}

// 根据seq，从client.pending中移除对应的call，并返回
func (client *Client) removeCall(seq uint64) *Call {
	client.mu.Lock()
	defer client.mu.Unlock()

	// 从处理未完成队列中移除
	call := client.pending[seq]
	delete(client.pending, seq)
	return call
}

// 服务端或者客户端发生错误时调用
// 将shutdown设置为true，且将错误信息通知所有pending状态的call
func (client *Client) terminateCalls(err error) {
	client.sending.Lock()
	defer client.sending.Unlock()

	client.mu.Lock()
	defer client.mu.Unlock()

	client.shutdown = true
	// 通知pending中的所有call
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
}

// receive 接受响应
func (client *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header

		if err = client.cc.ReadHeader(&h); err != nil { // 读取请求头
			break
		}

		call := client.removeCall(h.Seq)

		switch {
		case call == nil: // call不存在
			err = client.cc.ReadBody(h.Error)
		case h.Error != "": // call存在，但服务端处理错误，即h.Error不为空
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done() // 通知调用方
		default: // call存在，服务端处理正常，所以需要从body中读取reply的值
			err = client.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body " + err.Error())
			}
			call.done() // // 通知调用方
		}
	}
	// 通知所有pending中的call错误信息
	client.terminateCalls(err)
}

// send 发送请求
func (client *Client) send(call *Call) {
	client.sending.Lock()
	defer client.sending.Unlock()

	// 注册call
	seq, err := client.registerCall(call)
	if err != nil {
		call.Error = err
		call.done()
		return
	}

	// 准备请求头
	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = ""

	// Write设置header与body并发送
	if err := client.cc.Write(&client.header, call.Args); err != nil {
		// 如果发送失败，都要将call移除pending队列
		call := client.removeCall(seq)
		// call可能为nil，通常意味着Write部分失败，客户端已收到响应并进行处理
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

// parseOptions 实现Option为可选参数
func parseOptions(opts ...*Option) (*Option, error) {
	// 如果没有Option信息
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	// 判断是否只接收到一个Option信息
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}

	// 获取Option，并设置tinyrpc请求
	opt := opts[0]
	opt.MagicNumber = DefaultOption.MagicNumber
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType
	}
	return opt, nil
}

// Go 异步调用函数。
// 它返回表示调用的Call结构。
func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	if done == nil { // 检查done通道为空
		done = make(chan *Call, 10)
	} else if cap(done) == 0 { // 检查done通道的缓存区间是否为0
		log.Panic("rpc client: done channel is unbuffered")
	}

	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	// 发送call
	client.send(call)
	return call
}

// Call 调用命名函数，等待它完成，
// 并返回其错误状态
func (client *Client) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	call := client.Go(serviceMethod, args, reply, make(chan *Call, 1))
	select {
	case <-ctx.Done():
		client.removeCall(call.Seq)
		return errors.New("rpc client: call failed: " + ctx.Err().Error())
	case call := <-call.Done:
		return call.Error
	}
}

// NewClient 创建Client实例，同时进行一开始的协议交换
func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	// 生成NewGobCodec实例
	var f codec.NewCodecFunc = codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec error:", err)
		return nil, err
	}

	// json方式格式化Option信息，进行协议交换
	// 发送option给server
	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client: options error: ", err)
		_ = conn.Close()
		return nil, err
	}

	// 根据传入的网络连接conn，创建Client对应的gob编解码器cc
	// 返回完成编解码器与序列号，pending队列初始化的Client
	// newClientCodec(NewGobCodec(conn), opt)
	// f(conn)，会返回一个初始化好的GobCodec实例指针
	return newClientCodec(f(conn), opt), nil
}

// 指定Client的编解码器，还有初始化pending队列以及初始化序列号
func newClientCodec(cc codec.Codec, option *Option) *Client {
	client := &Client{
		seq:     1, // seq以1开头，0表示无效调用
		cc:      cc,
		pending: make(map[uint64]*Call),
	}
	go client.receive()
	return client
}

// clientResult 存储NewClient执行结果
type clientResult struct {
	client *Client
	err    error
}

type newClientFunc func(conn net.Conn, opt *Option) (client *Client, err error)

func dialTimeout(newClient newClientFunc, network, address string, opts ...*Option) (client *Client, err error) {

	opt, err := parseOptions(opts...) // 解析Option
	if err != nil {
		return
	}
	conn, err := net.DialTimeout(network, address, opt.ConnectTimeout) // 建立连接
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			_ = conn.Close()
		}
	}()

	ch := make(chan clientResult)

	// 通过子协程创建执行NewClient或NewHTTPClient，执行完成后，通过信道ch发送结果
	go func() {
		client, err := newClient(conn, opt)
		ch <- clientResult{client: client, err: err}
	}()

	// 如果连接超时时间设置为0，则直接返回NewClient的执行结果
	if opt.ConnectTimeout == 0 {
		result := <-ch
		return result.client, result.err
	}

	select {
	case <-time.After(opt.ConnectTimeout): // time.After信道先收到消息，说明NewClient执行超时
		return nil, fmt.Errorf("rpc client: connect timeout: expect within %s", opt.ConnectTimeout)
	case result := <-ch: // 从ch信道获取NewClient执行的结果
		return result.client, result.err
	}
}

// Dial 连接到指定网络地址的RPC服务器
func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	return dialTimeout(NewClient, network, address, opts...)
}

// NewHTTPClient 通过HTTP作为传输协议新建客户端
func NewHTTPClient(conn net.Conn, opt *Option) (*Client, error) {
	// 发起CONNECT请求
	_, _ = io.WriteString(conn, fmt.Sprintf("CONNECT %s HTTP/1.0\n\n", defaultRPCPath))

	// 获得响应，检查状态码
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
	if err == nil && resp.Status == connected {
		// 通过HTTP CONNECT请求建立连接后，后续通信过程交给NewClient
		return NewClient(conn, opt)
	}
	if err == nil {
		err = errors.New("unexpected HTTP response: " + resp.Status)
	}
	return nil, err
}

// DialHTTP 连接到指定的网络地址的HTTP RPC服务器
// 监听默认的HTTP RPC路径
func DialHTTP(network, address string, opts ...*Option) (*Client, error) {
	return dialTimeout(NewHTTPClient, network, address, opts...)
}

// XDial 调用不同的函数连接到RPC服务器
// 根据第一个参数rpcAddr
// rpcAddr是一种通用格式（protocol@addr）表示rpc服务器
// 例如:http@localhost:5000, tcp@localhost:5000
func XDial(rpcAddr string, opts ...*Option) (*Client, error) {
	// 以@作为分割
	parts := strings.Split(rpcAddr, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("rcp client err: wrong format '%s', expect protocol@addr", rpcAddr)
	}
	protocol, addr := parts[0], parts[1]
	switch protocol {
	case "http":
		return DialHTTP("tcp", addr, opts...)
	default:
		return Dial(protocol, addr, opts...)
	}
}
