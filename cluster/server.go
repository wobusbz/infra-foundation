package cluster

import (
	"context"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/config"
	"infra-foundation/connmanager"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/packet"
	"infra-foundation/processor"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/cloudwego/netpoll"
)

type ServerContext interface {
	ConnManager() *connmanager.SessionManager
	ModelManager() *model.ModelManager
	Scheduler() *scheduler.Scheduler
	WorkMessage() *processor.MsgQueue
}

type Server interface {
	ServerContext
	ClusterNode() *Node
	Listen(addr string) error
	ListenWeb(addr string)
	Run(ctx context.Context)
	Shutdown(ctx context.Context) error
}

type server struct {
	serverHandler  *ServerHandler
	poll         netpoll.EventLoop
	connManager  *connmanager.SessionManager
	modelManager *model.ModelManager
	scheduler    *scheduler.Scheduler
	workMessage  *processor.MsgQueue
	httpServer     *HTTPServer
	node           *Node
}

func NewServer() Server {
	s := &server{
		connManager:  connmanager.NewSessionManager(),
		modelManager: model.DefaultModelManager,
		scheduler:    scheduler.NewScheduler(),
		workMessage:  processor.NewMsgQueue(),
		node:         newNode(),
	}

	s.node.bindServer(s)

	s.node.SetConnectionFactory(func(addr, id, name string, frontend bool) error {
		conn := NewClientConn(s)
		if err := conn.DialConnection(addr); err != nil {
			return fmt.Errorf("[Server] failed to connect to %s: %w", addr, err)
		}
		sid, _ := strconv.Atoi(id)
		conn.BindID(int64(sid))
		return conn.Conn.SendTypePb(packet.Connection, &clusterpb.N2MOnConnection{ID: id, Name: name, Frontend: frontend})
	})

	s.serverHandler = NewServerHandler(s)
	s.httpServer = &HTTPServer{modelManager: s.modelManager}

	return s
}

func (s *server) ModelManager() *model.ModelManager { return s.modelManager }

func (s *server) ConnManager() *connmanager.SessionManager { return s.connManager }

func (s *server) Scheduler() *scheduler.Scheduler { return s.scheduler }

func (s *server) WorkMessage() *processor.MsgQueue { return s.workMessage }

func (s *server) ClusterNode() *Node { return s.node }

func (s *server) Listen(addr string) error {
	logx.Inf.Printf("[START] TCP Server listener at Addr: %s is starting", addr)
	ln, err := netpoll.CreateListener("tcp", addr)
	if err != nil {
		return err
	}
	s.poll, err = netpoll.NewEventLoop(
		s.serverHandler.OnRequest,
		netpoll.WithOnPrepare(s.serverHandler.OnPrepare),
		netpoll.WithOnDisconnect(s.serverHandler.OnDisconnect),
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

func (s *server) ListenWeb(addr string) {
	logx.Inf.Printf("[START] HTTP Server listener at Addr: %s is starting", addr)
	s.httpServer.Listen(addr)
}

func (s *server) Run(ctx context.Context) {
	cg := make(chan os.Signal, 1)
	signal.Notify(cg, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	<-cg
	_ = s.Shutdown(ctx)
	s.httpServer.Shutdown(ctx)
}

func (s *server) Shutdown(ctx context.Context) error {
	xctx, cancel := context.WithTimeout(ctx, config.Default.ShutdownTimeout)
	defer cancel()
	s.scheduler.Stop()
	s.modelManager.Stop()
	s.connManager.Range(func(s session.Session) error { return s.Close() })
	s.node.NodeConnManager().Range(func(s session.Session) error { return s.Close() })
	return s.poll.Shutdown(xctx)
}
