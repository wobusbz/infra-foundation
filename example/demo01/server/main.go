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
	"strconv"
)

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
	s := cluster.NewServer()
	if err := s.Listen("0.0.0.0:12381"); err != nil {
		panic(err)
	}
	logx.Dbg.Println("Rank")
	model.Register(&Rank{})
	model.RegisterHandler(&protos.C2SLogin{}, func(s session.Session, pm protomessage.ProtoMessage) {
		s.Send(&protos.S2CLogin{Name: "Helloworld client" + strconv.Itoa(int(s.ID()))})
	})
	s.Shutdown(context.TODO())
}
