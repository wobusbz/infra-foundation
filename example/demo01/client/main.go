package main

import (
	"infra-foundation/cluster"
	"infra-foundation/example/protos"
	"math/rand"
	"time"

	"net/http"
)

func main() {
	go func() {
		http.ListenAndServe("0.0.0.0:9009", nil)
	}()
	for range 10 {
		go func() {
			c := cluster.NewTCPClient()
			if err := c.DialConnection("localhost:12381"); err != nil {
				panic(err)
			}
			pb := &protos.C2SLogin{Name: "test client demo01"}
			for {
				if err := c.Send(pb); err != nil {
					panic(err)
				}
				time.Sleep(time.Second)
			}
		}()
		time.Sleep(time.Duration((rand.Int()%5)+1) * time.Second)
	}
	select {}
}
