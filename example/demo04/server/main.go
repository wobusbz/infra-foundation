package main

import (
	"context"
	"infra-foundation/cluster"
	"infra-foundation/example/protos"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/protomessage"
	"infra-foundation/session"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"
)

func init() {
	model.RegisterHandler(&protos.C2SLogin{}, C2SLogin)
}

func C2SLogin(s session.Session, pm protomessage.ProtoMessage) {
	err := s.Send(&protos.S2CLogin{Name: "Helloworld client" + strconv.Itoa(int(s.ID()))})
	if err != nil {
		logx.Err.Println(err)
	}
}

type Rank struct {
}

func (r *Rank) Name() string { return "user" }

func (r *Rank) OnInit() error { return nil }

func (r *Rank) OnStart() error { return nil }

func (r *Rank) OnStop() error { return nil }

func (r *Rank) OnConnection(s session.Session) {
	logx.Dbg.Println("[User/OnConnection] ", s.ID())
}

func (r *Rank) OnDisconnection(s session.Session) {
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
	model.Register(&Rank{})

	discovery.RegisterService(os.Args[1], os.Args[2], true, model.HandlersRoutes())

	s := cluster.NewServer()
	if err := s.Listen(os.Args[2]); err != nil {
		panic(err)
	}
	s.Shutdown(context.TODO())
}
