package main

import (
	"infra-foundation/cluster"
)

func main() {
	discovery, err := cluster.NewEtcdServiceDiscovery("XBOX", "localhost:2379")
	if err != nil {
		panic(err)
	}
	_ = discovery
	// discovery.RegisterService(os.Args[1], os.Args[2])
	select {}
}
