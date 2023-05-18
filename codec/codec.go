package codec

import "io"

// 头部信息
type Header struct {
	ServiceMethod string // 服务名和方法名
	Seq           uint64 // 请求序列号
	Error         string
}

// 编解码器的接口，抽象出接口实现不同的编解码器实例
type Codec interface {
	io.Closer                         // 关闭编解码器
	ReadHeader(*Header) error         // 读取rpc请求的头部信息
	ReadBody(interface{}) error       // 读取rpc请求的主体信息
	Write(*Header, interface{}) error // 将rpc响应写入连接
}

// 抽象出Codec的构造函数,客户端和服务端可以通过Codec的Type获取构造函数，从而创建Codec实例
type NewCodecFunc func(io.ReadWriteCloser) Codec

type Type string

const ( // 编码方式
	GobType  Type = "application/gob"
	JsonType Type = "application/json"
)

// NewCodecFunMap["application/gob"] = NewGobCodec
var NewCodecFuncMap map[Type]NewCodecFunc

func init() {
	// 根据Type的不同设置对应的实例
	NewCodecFuncMap = make(map[Type]NewCodecFunc)
	NewCodecFuncMap[GobType] = NewGobCodec
}
