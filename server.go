package tinyrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Asolmn/tinyrpc/codec"
	"io"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

// MagicNumber tinyrpc请求标记
const MagicNumber = 0x3bef5c

// Option 协商编解码方式 固定JSON编码
// 通过解析Option，服务端可以直到如何读取需要的信息
type Option struct {
	MagicNumber    int        // MagicNumber标记这是一个tinyrpc的请求
	CodecType      codec.Type // 客户端选择不同的编解码器堆正文进行编码
	ConnectTimeout time.Duration
	HandleTimeout  time.Duration
}

/*
Option{MagicNumber: int, CodecType: Type} | Header{ServiceMethod ...} | Body interface{}|
*/

// DefaultOption 创建默认协商信息实例，方便使用
var DefaultOption = &Option{
	MagicNumber:    MagicNumber,      // 0x3bef5c
	CodecType:      codec.GobType,    // Gob方式编解码器
	ConnectTimeout: time.Second * 10, // 连接超时时间
}

// Server RPC Server
type Server struct {
	serviceMap sync.Map
}

// NewServer 返回一个新的Server
func NewServer() *Server {
	return &Server{}
}

var DefaultServer *Server = NewServer() // Server的默认实例

// ServeConn 在单个连接上运行服务器
// ServeConn阻塞，为连接提供服务，直到客户端挂断
func (server *Server) ServeConn(conn io.ReadWriteCloser) {

	defer func() { _ = conn.Close() }()
	var opt Option

	// 通过json.NewDecoder反序列化得到Option实例
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server: options error:", err)
		return
	}
	// 检查是否是tinyrpc的请求标记
	if opt.MagicNumber != MagicNumber {
		log.Printf("rpc server: invalid magic number %x", opt.MagicNumber)
		return
	}

	// 根据CodeType类型得到对应的消息编解码器
	// f 为一个func(io.ReadWriteCloser) Codec
	// 在codec.go中通过init()函数，已经将CodecType="application/gob"类型初始化
	// 实际返回的gob.go中的NewGobCodec函数
	var f codec.NewCodecFunc = codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Printf("rpc server: invalid codec type %s", opt.CodecType)
		return
	}
	// f(conn)返回一个GobCodec实例,等价于直接调用NewGobCodec(conn)
	server.serveCodec(f(conn), &opt)
}

// 发生错误时响应argv的占位符
var invalidRequest = struct{}{}

/*
serveCodec 主要阶段
读取请求readRequest
处理请求handleRequest
回复请求sendResponse
*/
func (server *Server) serveCodec(cc codec.Codec, opt *Option) {
	sending := new(sync.Mutex) // 确保发送完整的响应
	wg := new(sync.WaitGroup)  // 等待，直到所有请求都得到处理

	for {
		// 读取请求
		req, err := server.readRequest(cc)
		if err != nil { // 请求错误
			if req == nil {
				break
			}
			// 设置请求头的错误
			req.h.Error = err.Error()
			// 发送回复，附带编解码器，请求头，错误响应，
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		// 处理请求是并发的，但是回复请求必须是逐个发送，所以需要使用锁进行保证
		go server.handleRequest(cc, req, sending, wg, opt.HandleTimeout)
	}
	// 等待所有请求完成
	wg.Wait()
	// 关闭编解码器
	_ = cc.Close()
}

// request 存储通话的所有信息
type request struct {
	h            *codec.Header // 请求头
	argv, replyv reflect.Value // reflect.Value可以表示任意类型的值的类型
	mtype        *methodType   // 方法实例
	svc          *service      // 服务实例
}

// readRequestHeader 读取请求头
func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	// 读取请求的头部信息，将信息反序列化到h中
	if err := cc.ReadHeader(&h); err != nil {
		// io.EOF错误表示已达文件或者流的末尾
		// io.ErrUnexpectedEOF错误表示已经意外地到达文件或者流的末尾
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server: read header error:", err)
		}
		return nil, err
	}
	// 返回请求头和空错误
	return &h, nil
}

// readRequest 读取请求
func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	// 获取请求头
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}

	// 构建一个request实例，并初始化其中的h
	req := &request{h: h}

	// 通过请求头中的服务名.方法名，获取service和method实例
	req.svc, req.mtype, err = server.findService(h.ServiceMethod)
	if err != nil {
		return req, err
	}

	// 创建两个入参实例
	req.argv = req.mtype.newArgv()
	req.replyv = req.mtype.newReplyv()

	// 确保argvi是一个指针,ReadBody需要一个指针作为参数
	argvi := req.argv.Interface()
	if req.argv.Type().Kind() != reflect.Ptr { // 判断argv是否为指针类型
		// 如果argv不等于指针类型，则通过Addr返回一个持有指向argv的指针的Value封装
		// 然后再转为空接口类型
		argvi = req.argv.Addr().Interface()
	}

	// 将请求报文反序列化为第一个入参argv
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server: read body err: ", err)
		return req, err
	}

	// 返回请求信息
	return req, nil
}

// sendResponse 回复请求
func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()

	// 将请求头和主体写入回复
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error: ", err)
	}
}

// handleRequest 处理请求
// 整个过程拆分为called和sent两个阶段
// called 信道接受消息，代表处理没有超时
// time.After()先于called，则处理已经超时
// called和sent都将被阻塞
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {
	defer wg.Done()

	called := make(chan struct{})
	sent := make(chan struct{})

	go func() {
		// 调用req.svc.method(req.argv, req.replyv)
		err := req.svc.call(req.mtype, req.argv, req.replyv)
		called <- struct{}{} // 通知信道，方法已经调用
		if err != nil {      // 如果发生错误，设置错误信息，并发送回client
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, invalidRequest, sending)
			sent <- struct{}{} // 通知发送信道
			return
		}
		server.sendResponse(cc, req.h, req.replyv.Interface(), sending) // 执行正确调用，发送给client
		sent <- struct{}{}                                              // 通知发送信道，sendResponse已执行
	}()

	if timeout == 0 { // 如果超时设置为0，直接通过called和sent信道，结束生命周期
		<-called
		<-sent
		return
	}

	select {
	case <-time.After(timeout): // 处理超时，发送错误信息给client
		req.h.Error = fmt.Sprintf("rpc server: request handle timeout: expect within %s", timeout)
		server.sendResponse(cc, req.h, invalidRequest, sending)
	case <-called: // 成功执行
		<-sent // 通知发送信道
	}
}

// findService 通过ServiceMethod从serviceMap中找到对应的service
// 返回对应的service实例与method实例还有错误信息
func (server *Server) findService(serviceMethod string) (svc *service, mtype *methodType, err error) {
	// 获得服务名与方法名分割位置的下标
	dot := strings.LastIndex(serviceMethod, ".")
	if dot < 0 {
		err = errors.New("rpc server: service/method request ill-formed: " + serviceMethod)
		return
	}

	// 提取服务名和方法名
	serviceName, methodName := serviceMethod[:dot], serviceMethod[dot+1:]
	// 从serviceMap中找到对应的service实例
	svci, ok := server.serviceMap.Load(serviceName)
	if !ok {
		err = errors.New("rpc server: can't find service " + serviceName)
		return
	}

	// 类型断言转换为service实例
	svc = svci.(*service)
	// 通过方法名获得对应的方法
	mtype = svc.method[methodName]
	if mtype == nil {
		err = errors.New("rpc server: can't find method " + methodName)
	}

	return
}

// Accept 接受网络监听器上的连接并提供请求
func (server *Server) Accept(lis net.Listener) {
	for { // 循环等待socket连接建立
		conn, err := lis.Accept()
		if err != nil {
			log.Println("rpc server: accept error", err)
			return
		}
		// 开启子协程，处理过程交给ServerConn
		go server.ServeConn(conn)
	}
}

// Accept 接受监听器上的连接并提供请求
func Accept(lis net.Listener) { DefaultServer.Accept(lis) }

// Register method在server中发布
// 满足以下条件的receiver值
// 导出类型的导出方法
// 两个参数，均为导出类型
// 第二个参数是指针
// 一个返回值，类型为error
func (server *Server) Register(rcvr interface{}) error {
	// 生成service实例
	s := newService(rcvr)

	// 将service实例添加到服务器中
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service already defined: " + s.name)
	}
	return nil
}

// Register 在DefaultServer中发布receiver的方法
func Register(rcvr interface{}) error { return DefaultServer.Register(rcvr) }

const (
	connected        = "200 Connected to tinyrpc"
	defaultRPCPath   = "/_tinyrpc_"
	defaultDebugPath = "/debug/tinyrpc"
)

func (server *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "CONNECT" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, "405 must CONNECT\n")
		return
	}

	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Print("rpc hijacking ", req.RemoteAddr, ":", err.Error())
		return
	}

	_, _ = io.WriteString(conn, "HTTP/1.0"+connected+"\n\n")
	server.ServeConn(conn)
}

func (server *Server) HandleHTTP() {
	http.Handle(defaultRPCPath, server)
}

func HandleHTTP() {
	DefaultServer.HandleHTTP()
}
