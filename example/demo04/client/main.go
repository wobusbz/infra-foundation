package main

import (
	"context"
	"infra-foundation/packet"
	"log"
	"time"

	"github.com/cloudwego/netpoll"
)

func main() {
	conn, err := netpoll.NewDialer().DialConnection("tcp", "192.168.110.67:12381", time.Second)
	if err != nil {
		panic(err)
	}
	var b = make([]byte, 2048)
	codec := packet.NewPackCodec()
	conn.SetOnRequest(func(ctx context.Context, connection netpoll.Connection) error {
		pks, err := codec.Unpack(b)
		if err != nil {
			return err
		}
		for _, pk := range pks {
			log.Println(pk)
		}
		return nil
	})
	bdata, err := codec.Pack(packet.Heartbeat, 0, 0, nil)
	if err != nil {
		panic(err)
	}
	for {
		time.Sleep(time.Second)
		conn.Write(bdata)
	}
}
