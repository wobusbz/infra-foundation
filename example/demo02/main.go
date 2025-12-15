package main

import (
	"fmt"
	"infra-foundation/cluster"
	"infra-foundation/logx"
	"math/rand"
	"os"
)

func main() {
	discovery, err := cluster.NewEtcdServiceDiscovery("XBOX", "localhost:2379")
	if err != nil {
		panic(err)
	}
	logx.Dbg.Println(discovery.RegisterService(os.Args[1], fmt.Sprintf("127.0.0.1:%d", 1000+rand.Int()%4), true, nil))
}
