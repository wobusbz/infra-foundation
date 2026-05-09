package main

import (
	"context"
	"fmt"
	"infra-foundation/cluster"
	"infra-foundation/example/protos"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/session"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
)

func init() {
	model.RegisterTypedHandler(&protos.C2SEnterMap{}, func(ctx *session.Context, pb *protos.C2SEnterMap) {
		reply := &protos.S2CEnterMap{Name: "Client TO ID " + string(ctx.Session.ID())}
		err := ctx.Session.Send(reply)
		if err != nil {
			logx.Err.Println(err)
		}
	})
}

type User struct {
	allUsers map[string]struct{}
}

func (r *User) Name() string { return "user" }

func (r *User) OnInit() error { return nil }

func (r *User) OnStart() error { return nil }

func (r *User) OnStop() error { return nil }

func (r *User) OnSessionDisconnected(s session.Identity) {
	logx.Dbg.Println("[User/OnDisconnection] ", s.ID())
}
func (r *User) OnSessionInitialization(s session.Identity) {
	logx.Dbg.Println("[User/OnSessionInitialization] ", s.ID())
}

func main() {
	//logx.SetLevel(logx.WarLevel())
	go func() {
		pp := os.Getenv("PPROF_PORT")
		if pp == "" {
			pp = "9008"
		}
		http.ListenAndServe("0.0.0.0:"+pp, nil)
	}()

	model.Register(&User{})

	s := cluster.NewServer()
	node := s.ClusterNode()

	localAddr := "127.0.0.1"

	discovery, err := cluster.NewEtcdServiceDiscovery("XBOX", "localhost:2379", s.PeerConnector(), func(name, id, addr string, frontend bool, rids []int32) {
		node.SetLocalNode(name, id, addr, frontend, rids)
	})
	if err != nil {
		panic(err)
	}
	defer discovery.Close()

	adver := fmt.Sprintf("%s:%s", localAddr, strings.Split(os.Args[2], ":")[1])
	if err := discovery.RegisterService(os.Args[1], adver, false, model.GetLocalHandlerIDs()); err != nil {
		panic(err)
	}

	if err := s.ListenCluster(os.Args[2]); err != nil {
		panic(err)
	}
	s.Run(context.TODO())
}
