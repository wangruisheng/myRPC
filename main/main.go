package main

import (
	"encoding/json"
	"fmt"
	"log"
	"myRPC"
	"myRPC/codec"
	"net"
	"time"
)

// 实现一个简易的客户端
// 在 startServer 中使用了信道 addr，确保服务端端口监听成功，客户端再发起请求。
// 客户端首先发送 Option 进行协议交换，接下来发送消息头 h := &codec.Header{}，和消息体 geerpc req ${h.Seq}。
// 最后解析服务端的响应 reply，并打印出来。
func main() {
	addr := make(chan string)
	go startServer(addr)

	// 下面的代码相当于一个简单的客户端
	conn, _ := net.Dial("tcp", <-addr)
	defer func() {
		_ = conn.Close()
	}()

	time.Sleep(time.Second)
	// 发送操作，向conn中发送编码好的数据
	_ = json.NewEncoder(conn).Encode(myRPC.DefaultOption)
	cc := codec.NewGobCodec(conn)
	// 发送请求，接受响应
	for i := 0; i < 5; i++ {
		h := &codec.Header{
			ServiceMethod: "Foo.Sum",
			Seq:           uint64(i),
		}
		// 向 conn 中写入请求
		_ = cc.Write(h, fmt.Sprintf("geerpc req %d", h.Seq))
		_ = cc.ReadHeader(h)
		var reply string
		_ = cc.ReadBody(&reply)
		log.Println("reply:", reply)
	}
}

// 启动服务器，并让服务器选择一个 ip端口 进行监听
func startServer(addr chan string) {
	// 选择一个空闲的 ip+端口 进行监听
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		log.Fatal("network error:", err)
	}
	log.Println("start rpc server on", l.Addr())
	addr <- l.Addr().String()
	myRPC.Acccept(l)
}
