package tinyrpc

import (
	"encoding/json"
	"fmt"
	"github.com/Asolmn/tinyrpc/codec"
	"io"
	"log"
	"net"
	"reflect"
	"sync"
)

// MagicNumber tinyrpc请求标记
const MagicNumber = 0x3bef5c

// Option 协商编解码方式 固定JSON编码
// 通过解析Option，服务端可以直到如何读取需要的信息
type Option struct {
	MagicNumber int        // MagicNumber标记这是一个tinyrpc的请求
	CodecType   codec.Type // 客户端选择不同的编解码器堆正文进行编码
}

/*
Option{MagicNumber: int, CodecType: Type} | Header{ServiceMethod ...} | Body interface{}|
*/

// DefaultOption 创建默认协商信息实例，方便使用
var DefaultOption = &Option{
	MagicNumber: MagicNumber,   // 0x3bef5c
	CodecType:   codec.GobType, // Gob方式编解码器
}

// Server RPC Server
type Server struct {
}

// NewServer 返回一个新的Server
func NewServer() *Server {
	return &Server{}
}

var DefaultServer *Server = NewServer() // Server的默认实例

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
	server.serveCodec(f(conn))
}

// 发生错误时响应argv的占位符
var invalidRequest = struct{}{}

/*
serveCodec主要阶段
读取请求readRequest
处理请求handleRequest
回复请求sendResponse
*/
func (server *Server) serveCodec(cc codec.Codec) {
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
		go server.handleRequest(cc, req, sending, wg)
	}
	// 等待所有请求完成
	wg.Wait()
	// 关闭编解码器
	_ = cc.Close()
}

// request存储通话的所有信息
type request struct {
	h            *codec.Header
	argv, replyv reflect.Value // reflect.Value可以表示任意类型的值的类型
}

// 读取请求头
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

// 读取请求
func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	// 获取请求头
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err
	}

	// 构建一个request实例，并初始化其中的h
	req := &request{h: h}

	// reflect.New()创建一个指向新分配的零值的指针
	// reflect.TypeOf用于获取一个值的类型信息，返回一个reflect.Type类型的值，表示类型信息
	// 创建一个新的空字符串的指针，类型是*string
	req.argv = reflect.New(reflect.TypeOf(""))

	// reflect.Value.Interface()将Value类型的值转换为对应的类型接口
	// Interface()返回一个空接口类型的值，表示argv的实际值，类型为argv的类型
	// 读取body保存到req.argv中
	if err = cc.ReadBody(req.argv.Interface()); err != nil {
		log.Println("rpc server: read argv err:", err)
	}

	// 返回请求信息
	return req, nil
}

// 回复请求
func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()

	// 将请求头和主体写入回复
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error: ", err)
	}
}

// 处理请求
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	defer wg.Done()
	// ELem()返回argv包含的值或者argv指针指向的值
	// 打印请求头部和主体信息
	log.Println(req.h, req.argv.Elem())
	// 设置请求的回应值
	req.replyv = reflect.ValueOf(fmt.Sprintf("tinyrpc resp %d", req.h.Seq))
	// 发送请求
	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
}
