package pcall

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type TestObj struct{}

func (TestObj) Add(a, b int) {
	fmt.Println(a, b)
}

func (TestObj) Fail(a int) (int, error) {
	return 0, errors.New("fail")
}

func TestPcall_Success(t *testing.T) {
	obj := TestObj{}
	m, _ := reflect.TypeFor[TestObj]().MethodByName("Add")
	args := []reflect.Value{reflect.ValueOf(obj), reflect.ValueOf(int(1)), reflect.ValueOf(int(2))}
	Pcall0(m, args)
	var arrs = []int{1, 2}
	t.Log(arrs[:len(arrs)-1])
}

func TestPcall_Fail(t *testing.T) {
	// obj := TestObj{}
	// m, _ := reflect.TypeOf(obj).MethodByName("Fail")
	// args := []reflect.Value{reflect.ValueOf(obj), reflect.ValueOf(1)}
	// ret, err := Pcall(m, args)
	// if err == nil || err.Error() != "fail" {
	// 	t.Fatalf("expected error 'fail', got %v", err)
	// }
	// if ret != nil {
	// 	t.Fatalf("expected nil ret, got %v", ret)
	// }
}

type Type byte

const (
	Heartbeat Type = 0x01 + iota
	Handshake
	HandshakeAck
	Data
	InternalData
	ClientData
	BindSession
	DisSession
	Invalid
)
