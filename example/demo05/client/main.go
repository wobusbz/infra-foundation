package main

import (
	"infra-foundation/cluster"
	"infra-foundation/example/protos"
	"infra-foundation/logx"
	"infra-foundation/protomessage"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var sendCount uint64

func main() {
	const connNum = 1000
	const msgNum = 10000
	var connCount atomic.Int64

	exitC := make(chan struct{}, 1)

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		var last uint64
		for {
			select {
			case <-exitC:
				return
			case <-ticker.C:
				current := atomic.LoadUint64(&sendCount)
				qps := current - last
				last = current
				logx.Dbg.Printf("[QPS统计] ConnCount = %d, QPS = %d, 总请求数 = %d\n", connCount.Load(), qps, current)
			}
		}
	}()

	var wg sync.WaitGroup

	for range connNum {
		wg.Go(func() {
			c := cluster.NewTCPClient()
			if err := c.DialConnection(os.Args[1]); err != nil {
				panic(err)
			}
			connCount.Add(1)
			c.RegisterHandler(&protos.S2CLogin{}, func(cc *cluster.TCPClient, pb protomessage.ProtoMessage) {})
			for range msgNum {
				select {
				case <-exitC:
					return
				default:
				}
				time.Sleep(time.Duration(100) * time.Millisecond)
				atomic.AddUint64(&sendCount, 1)
				c.Send(&protos.C2SLogin{Name: "C2SLogin"})
			}
		})
		time.Sleep(time.Duration(rand.IntN(100)+100) * time.Millisecond)
	}
	wg.Wait()
	logx.Dbg.Println("所有连接任务完成")
	// cg := make(chan os.Signal, 1)
	// signal.Notify(cg, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	// <-cg
	close(exitC)
}
