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
	"time"
)

func init() {
	model.RegisterTypedHandler(&protos.C2SLogin{}, func(ctx *session.Context, pb *protos.C2SLogin) {
		ctx.Session.BindUID(time.Now().Unix())
		ctx.Session.Send(&protos.N2MLogin{Name: "Helloworld client" + string(ctx.Session.ID())})
	})
}

type User struct{}

func (u *User) Name() string   { return "user" }
func (u *User) OnInit() error  { return nil }
func (u *User) OnStart() error { return nil }
func (u *User) OnStop() error  { return nil }
func (u *User) OnDisconnection(s session.Session) {
	logx.Dbg.Println("[User/OnDisconnection] ", s.ID())
}

func main() {
	go func() {
		http.ListenAndServe("0.0.0.0:9009", nil)
	}()
	model.Register(&User{})
	s := cluster.NewServer()
	localAddr := "127.0.0.1"
	discovery, err := cluster.NewEtcdServiceDiscovery("XBOX", "localhost:2379", s.ClusterNode())
	if err != nil {
		panic(err)
	}
	defer discovery.Close()
	if err := discovery.RegisterService(os.Args[1], fmt.Sprintf("%s:%s", localAddr, strings.Split(os.Args[2], ":")[1]), true, model.GetLocalHandlerIDs()); err != nil {
		panic(err)
	}
	if err := s.Listen(os.Args[2]); err != nil {
		panic(err)
	}
	s.Run(context.TODO())
}
