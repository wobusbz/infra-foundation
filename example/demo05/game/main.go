package main

import (
	"context"
	"fmt"
	"infra-foundation/cluster"
	"infra-foundation/example/protos"
	"infra-foundation/localipaddr"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/protomessage"
	"infra-foundation/session"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
	"strings"
)

func init() {
	model.RegisterHandler(&protos.N2MLogin{}, C2SLogin)
}

func C2SLogin(s session.Session, pm protomessage.ProtoMessage) {
	err := s.Send(&protos.S2CLogin{Name: "Client TO ID " + strconv.Itoa(int(s.ID()))})
	if err != nil {
		logx.Err.Println(err)
	}
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
	go func() {
		http.ListenAndServe("0.0.0.0:9008", nil)
	}()
	discovery, err := cluster.NewEtcdServiceDiscovery("XBOX", "localhost:2379")
	if err != nil {
		panic(err)
	}
	defer discovery.Close()
	model.Register(&User{})

	localAddr, err := localipaddr.LocalIPAddr()
	if err != nil {
		panic(err)
	}

	logx.Dbg.Println(model.HandlersRoutes())

	discovery.RegisterService(os.Args[1], fmt.Sprintf("%s:%s", localAddr, strings.Split(os.Args[2], ":")[1]), false, model.HandlersRoutes())

	s := cluster.NewServer()
	if err := s.Listen(os.Args[2]); err != nil {
		panic(err)
	}
	s.Run(context.TODO())
}
