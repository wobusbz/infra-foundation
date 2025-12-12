package main

import (
	"infra-foundation/cluster"
	"math/rand"
	"time"

	"net/http"
)

func main() {
	go func() {
		http.ListenAndServe("0.0.0.0:9009", nil)
	}()
	for range 1 {
		go func() {
			c := cluster.NewTCPClient()
			if err := c.DialConnection("127.0.0.1:8002"); err != nil {
				panic(err)
			}
			//pb := &cluster.N2MSend{Plyload: []byte("hello world")}
			go func() {
				for {
					// if err := c.Send(pb); err != nil {
					// 	panic(err)
					// }
				}
			}()
		}()
		time.Sleep(time.Duration((rand.Int()%5)+1) * time.Second)
	}
	select {}
}
