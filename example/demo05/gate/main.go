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
	"time"
)

func init() {
	model.RegisterHandler(&protos.C2SLogin{}, C2SLogin)
}

func C2SLogin(s session.Session, pm protomessage.ProtoMessage) {
	s.BindUID(time.Now().Unix())
	err := s.Send(&protos.N2MLogin{Name: "Helloworld client" + strconv.Itoa(int(s.ID()))})
	if err != nil {
		logx.Err.Println(err)
	}
}

type User struct {
}

func (u *User) Name() string { return "user" }

func (u *User) OnInit() error { return nil }

func (u *User) OnStart() error { return nil }

func (u *User) OnStop() error { return nil }

func (u *User) OnDisconnection(s session.Session) {
	logx.Dbg.Println("[User/OnDisconnection] ", s.ID())
}

func main() {
	go func() {
		http.ListenAndServe("0.0.0.0:9009", nil)
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

	discovery.RegisterService(os.Args[1], fmt.Sprintf("%s:%s", localAddr, strings.Split(os.Args[2], ":")[1]), true, model.HandlersRoutes())

	s := cluster.NewServer()
	if err := s.Listen(os.Args[2]); err != nil {
		panic(err)
	}
	s.Run(context.TODO())
}
