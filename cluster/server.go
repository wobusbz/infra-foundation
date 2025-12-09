package cluster

import (
	"context"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudwego/netpoll"

	"infra-foundation/connmannger"
)

type Server interface {
	ModelManager() *model.ModelManager
	ConnManager() *connmannger.ConnManager
	Scheduler() *scheduler.Scheduler
	Listen(addr string) error
	Shutdown(ctx context.Context) error
}

type server struct {
	svrrequest   *ServerRequest
	poll         netpoll.EventLoop
	connManager  *connmannger.ConnManager
	modelManager *model.ModelManager
	scheduler    *scheduler.Scheduler
}

func NewServer() Server {
	s := &server{connManager: connmannger.NewConnManager(), modelManager: model.DefaultModelManager, scheduler: scheduler.NewScheduler()}

	s.svrrequest = NewServerRequest(s)
	defaultNodeAgent.storeServer(s)

	return s
}

func (s *server) ModelManager() *model.ModelManager { return s.modelManager }

func (s *server) ConnManager() *connmannger.ConnManager { return s.connManager }

func (s *server) Scheduler() *scheduler.Scheduler { return s.scheduler }

func (s *server) Listen(addr string) error {
	logx.Inf.Printf("[START] TCP Server listener at Addr: %s is starting", addr)
	ln, err := netpoll.CreateListener("tcp", addr)
	if err != nil {
		return err
	}
	s.poll, err = netpoll.NewEventLoop(
		s.svrrequest.OnRequest,
		netpoll.WithOnPrepare(s.svrrequest.OnPrepare),
		netpoll.WithOnDisconnect(s.svrrequest.OnDisconnect),
	)
	if err != nil {
		return err
	}
	go func() {
		if err = s.poll.Serve(ln); err != nil {
			panic(err)
		}
	}()
	return err
}

func (s *server) Shutdown(ctx context.Context) error {
	cg := make(chan os.Signal, 1)
	signal.Notify(cg, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	<-cg
	xctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	s.scheduler.Stop()
	s.modelManager.Stop()
	s.connManager.Range(func(s session.Session) error { return s.Close() })
	defaultNodeAgent.connManager.Range(func(s session.Session) error { return s.Close() })
	return s.poll.Shutdown(xctx)
}
