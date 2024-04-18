package myRPC

import (
	"fmt"
	"reflect"
	"testing"
)

type Foo int

type Args struct {
	Num1, Num2 int
}

// 非导出方法
func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

// 这不是导出方法
func (f Foo) sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func _assert(condition bool, msg string, v ...interface{}) {
	if !condition {
		panic(fmt.Sprintf("assert failed: "+msg, v...))
	}
}

func TestNewService(t *testing.T) {
	var foo Foo
	s := newService(&foo)
	_assert(len(s.method) == 1, "wrong service Method, except 1, but got %d", len(s.method))
	mType := s.method["Sum"]
	_assert(mType != nil, "wrong Method, Sum should't nil")
}

func TestMethodType_Calls(t *testing.T) {
	var foo Foo
	s := newService(&foo)
	mType := s.method["Sum"]

	argv := mType.newArgv()
	replyv := mType.newReplyv()
	// 将 reflect.ValueOf(Args{Num1:1,Num2:2}) 赋给 argv
	argv.Set(reflect.ValueOf(Args{Num1: 1, Num2: 3}))
	err := s.call(mType, argv, replyv)
	fmt.Println(*replyv.Interface().(*int))
	fmt.Println(mType.NumCalls())
	_assert(err == nil && *replyv.Interface().(*int) == 4 && mType.NumCalls() == 1, "failed to call Foo.Sum")

}
