package main

import (
	"context"
	"fmt"
	"infra-foundation/cluster"
	"infra-foundation/example/protos"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/session"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strings"
)

func init() {
	model.RegisterTypedHandler(&protos.N2MLogin{}, func(ctx *session.Context, pb *protos.N2MLogin) {
		reply := &protos.S2CLogin{Name: "Client TO ID " + string(ctx.Session.ID())}
		if rand.Int()&1 == 0 {
		}
		err := ctx.Session.Send(reply)
		if err != nil {
			logx.Err.Println(err)
		}
	})
}

type User struct {
}

func (r *User) Name() string { return "user" }

func (r *User) OnInit() error { return nil }

func (r *User) OnStart() error { return nil }

func (r *User) OnStop() error { return nil }

func (r *User) OnDisconnection(s session.Session) {
	logx.Dbg.Println("[User/OnDisconnection] ", s.ID())
}

func main() {
	logx.SetLevel(logx.WarLevel())
	go func() {
		pp := os.Getenv("PPROF_PORT")
		if pp == "" {
			pp = "9008"
		}
		http.ListenAndServe("0.0.0.0:"+pp, nil)
	}()

	model.Register(&User{})

	s := cluster.NewServer()

	localAddr := "127.0.0.1"

	discovery, err := cluster.NewEtcdServiceDiscovery("XBOX", "localhost:2379", s.ClusterNode())
	if err != nil {
		panic(err)
	}
	defer discovery.Close()

	logx.Dbg.Println(model.GetLocalHandlerIDs())
	adver := fmt.Sprintf("%s:%s", localAddr, strings.Split(os.Args[2], ":")[1])
	if err := discovery.RegisterService(os.Args[1], adver, false, model.GetLocalHandlerIDs()); err != nil {
		panic(err)
	}

	if err := s.ListenCluster(os.Args[2]); err != nil {
		panic(err)
	}
	s.Run(context.TODO())
}
