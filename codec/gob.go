package codec

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

// Gob类型的编解码器
type GobCodec struct {
	conn io.ReadWriteCloser // 链接实例
	buf  *bufio.Writer      // 缓冲Writer
	dec  *gob.Decoder       // 反序列化
	enc  *gob.Encoder       // 序列化
}

// 检查GobCodec实例是否具有Codec接口的所有方法
var _ Codec = (*GobCodec)(nil)

// 通过构建函数传入conn
// 通常是通过TCP或者Unix建立socket得到的链接实例
// 返回一个新的GobCodec实例
func NewGobCodec(conn io.ReadWriteCloser) Codec {
	// 创建一个缓冲写入器，写入目标为conn
	buf := bufio.NewWriter(conn)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		dec:  gob.NewDecoder(conn), // 创建新解码器
		enc:  gob.NewEncoder(buf),  // 创建新编码器
	}
}

// 读取rpc请求的头部信息
func (c *GobCodec) ReadHeader(h *Header) error {
	return c.dec.Decode(h) // 反序列化头部信息
}

// 读取rpc请求的主体信息
func (c *GobCodec) ReadBody(body interface{}) error {
	// 将主体的Gob编码解码为Go中的数据类型
	return c.dec.Decode(body) // 反序列化主体信息
}

// 将rpc响应写入连接
func (c *GobCodec) Write(h *Header, body interface{}) (err error) {
	defer func() {
		// 将编码写入到buf中
		_ = c.buf.Flush()
		if err != nil {
			_ = c.Close()
		}
	}()

	// 序列号头部
	// Encode(h)，对头部信息进行Gob编码
	if err := c.enc.Encode(h); err != nil {
		log.Println("rpc codec: gob error encoding header:", err)
		return err
	}

	// 序列号主体
	// Encode(body)，对主体信息进行Gob编码
	if err := c.enc.Encode(body); err != nil {
		log.Println("rpc codec: gob error encoding body:", err)
		return err
	}
	return nil
}

// 关闭编解码器
func (c *GobCodec) Close() error {
	return c.conn.Close()
}
