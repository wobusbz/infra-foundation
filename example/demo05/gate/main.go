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
	"strconv"
	"strings"
	"time"
)

func init() {
	model.RegisterTypedHandler(&protos.C2SLogin{}, func(ctx *session.Context, pb *protos.C2SLogin) {
		ctx.Session.BindUid(strconv.FormatInt(time.Now().Unix(), 10))
		logx.Dbg.Println("C2SLogin: ", ctx.Session.ID())
		ctx.Session.Send(&protos.S2CLogin{Name: "Helloworld client" + string(ctx.Session.ID())})
	})
}

type User struct{}

func (u *User) Name() string   { return "user" }
func (u *User) OnInit() error  { return nil }
func (u *User) OnStart() error { return nil }
func (u *User) OnStop() error  { return nil }
func (u *User) OnSessionDisconnected(s session.Session) {
	logx.Dbg.Println("[User/OnDisconnection] ", s.ID())
}
func (u *User) OnSessionInitialization(s session.Session) {
	logx.Dbg.Println("[User/OnSessionInitialization] ", s.ID())
}

func main() {
	//logx.SetLevel(logx.WarLevel())
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
	clientPortStr := strings.Split(os.Args[2], ":")[1]
	clientPortNum, _ := strconv.Atoi(clientPortStr)
	clusterPortStr := strconv.Itoa(clientPortNum + 1000)
	if err := discovery.RegisterService(os.Args[1], fmt.Sprintf("%s:%s", localAddr, clusterPortStr), true, model.GetLocalHandlerIDs()); err != nil {
		panic(err)
	}
	if err := s.ListenClient(os.Args[2]); err != nil {
		panic(err)
	}
	if err := s.ListenCluster(":" + clusterPortStr); err != nil {
		panic(err)
	}
	s.Run(context.TODO())
}
