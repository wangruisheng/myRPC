package codec

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

// 定义对 Gob 消息体编码的方式

// 定义 Gob 消息体
type GobCodec struct {
	conn io.ReadWriteCloser // conn 是由构建函数传入，通常是通过 TCP 或者 Unix 建立 socket 时得到的链接实例
	buf  *bufio.Writer      // buf 是为了防止阻塞而创建的带缓冲的 Writer，一般这么做能提升性能。
	dec  *gob.Decoder       // gob的Decoder
	enc  *gob.Encoder       // gob的Encoder
}

// var _ Codec = (*GobCodec)(nil) 是一种声明，用于确保类型 GobCodec 实现了 Codec 接口。这种声明通常称为接口断言。
// 在这个声明中，_ 是一个占位符，表示我们不关心具体的值。Codec 是一个接口类型，而 (*GobCodec)(nil) 则是一个空指针，表示 GobCodec 类型的空值。
// 通过这个声明，编译器会在编译时检查 GobCodec 类型是否实现了 Codec 接口。如果 GobCodec 没有实现 Codec 接口的所有方法，编译器会在编译时产生错误。
// 这样做的好处是可以在编译时捕获到一些错误，而不是在运行时才发现接口未被正确实现的问题。
var _ Codec = (*GobCodec)(nil)

func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriter(conn)
	return &GobCodec{
		conn: conn,
		buf:  buf,
		// 将 conn 传入的数据解码
		dec: gob.NewDecoder(conn),
		// 编码好的数据写入 buf
		enc: gob.NewEncoder(buf),
	}
}

// 接着实现 ReadHeader、ReadBody、Write 和 Close 方法。(实现 Codec 接口)
func (c *GobCodec) Close() error {
	return c.conn.Close()
}

// ReadHeader 传入的是 Header 接口体的指针 h，最后会将解码后的值赋给 h
func (c *GobCodec) ReadHeader(h *Header) error {
	return c.dec.Decode(h)
}

// ReadHeader 传入的是指针 body，最后会将解码后的值赋给 body
func (c *GobCodec) ReadBody(body interface{}) error {
	return c.dec.Decode(body)
}

// 注意这里给返回值命名了，所以直接给err赋值即可，不用return
func (c *GobCodec) Write(h *Header, body interface{}) (err error) {
	defer func() {
		// 将 buf当中的编码值（响应response） 刷新到 conn 当中
		_ = c.buf.Flush()
		if err != nil {
			_ = c.Close()
		}
	}()
	// newEncode的时候传入了一个 writebuf，所以encode的时候会写入这个buf
	if err := c.enc.Encode(h); err != nil {
		log.Println("rpc codec: gob error encoding header:", err)
		return err
	}
	if err := c.enc.Encode(body); err != nil {
		log.Println("rpc codec: gob error encoding body:", err)
		return err
	}
	return nil
}
