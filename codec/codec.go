// 我们将和消息编解码相关的代码都放到 codec 子目录中
package codec

import "io"

// 抽象出【消息头】
// ServiceMethod 是服务名和方法名，通常与 Go 语言中的结构体和方法相映射。
// Seq 是请求的序号，也可以认为是某个请求的 ID，用来区分不同的请求。
// Error 是错误信息，客户端置为空，服务端如果如果发生错误，将错误信息置于 Error 中。
type Header struct {
	ServiceMethod string // format "Service.Method"
	Seq           uint64 // sequence number chosen by client
	Error         string
}

// 抽象出对【消息体】进行编解码的接口 Codec，抽象出接口是为了实现不同的 Codec 实例
type Codec interface {
	// Codec 接口嵌入了 io.Closer 接口,这种嵌入式接口的作用是使 Codec 接口继承了 io.Closer 接口的所有方法,在go中称为接口的组合
	io.Closer
	// 解码 header
	ReadHeader(*Header) error
	// 解码 body
	ReadBody(interface{}) error
	// 编码
	Write(*Header, interface{}) error
}

type NewCodecFunc func(closer io.ReadWriteCloser) Codec

// 定义 Type 类型
type Type string

// 我们定义了 2 种 Codec，Gob 和 Json，但是实际代码中只实现了 Gob 一种，
// 事实上，2 者的实现非常接近，甚至只需要把 gob 换成 json 即可
const (
	GobType  Type = "application/gob"
	JsonType Type = "application/json" // not implemented
)

// 建立 类型 和 处理方法 的映射函数
var NewCodecFuncMap map[Type]NewCodecFunc

func init() {
	NewCodecFuncMap = make(map[Type]NewCodecFunc)
	NewCodecFuncMap[GobType] = NewGobCodec
}
