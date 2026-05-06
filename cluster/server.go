package cluster

import (
	"context"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"os"
	"os/signal"

	"syscall"
	"time"

	"github.com/cloudwego/netpoll"
)

type ServerOption func(*ServerOptions)

type ServerOptions struct {
	ConnMgr      *session.Manager
	ModelManager *model.ModelManager
	Scheduler    *scheduler.Scheduler
	TaskQueue    *queue.TaskQueue
	Config       *config.Config
}

func WithConnMgr(cm *session.Manager) ServerOption {
	return func(o *ServerOptions) {
		o.ConnMgr = cm
	}
}

func WithModelManager(mm *model.ModelManager) ServerOption {
	return func(o *ServerOptions) {
		o.ModelManager = mm
	}
}

func WithScheduler(sch *scheduler.Scheduler) ServerOption {
	return func(o *ServerOptions) {
		o.Scheduler = sch
	}
}

func WithTaskQueue(wm *queue.TaskQueue) ServerOption {
	return func(o *ServerOptions) {
		o.TaskQueue = wm
	}
}

func WithConfig(cfg *config.Config) ServerOption {
	return func(o *ServerOptions) {
		o.Config = cfg
	}
}

type server struct {
	clientHandler  *ClientHandler
	clusterHandler *ClusterHandler
	clientPoll     netpoll.EventLoop
	clusterPoll    netpoll.EventLoop
	clientMgr      *session.Manager
	peerMgr        *session.Manager
	modelManager   *model.ModelManager
	scheduler      *scheduler.Scheduler
	msgQueue       *queue.TaskQueue
	config         *config.Config
	httpServer     *HTTPServer
	node           *Node
	errCh          chan error
}

func NewServer(options ...ServerOption) *server {
	opts := &ServerOptions{
		Config: config.NewDefault(),
	}
	for _, opt := range options {
		opt(opts)
	}

	connMgr := opts.ConnMgr
	if connMgr == nil {
		connMgr = session.NewManager()
	}

	modelManager := opts.ModelManager
	if modelManager == nil {
		modelManager = model.DefaultModelManager
	}

	sch := opts.Scheduler
	if sch == nil {
		sch = scheduler.NewScheduler()
	}

	msgQueue := opts.TaskQueue
	if msgQueue == nil {
		msgQueue = queue.NewTaskQueue()
	}

	clientMgr := connMgr
	peerMgr := session.NewManager()

	s := &server{
		clientMgr:    clientMgr,
		peerMgr:      peerMgr,
		modelManager: modelManager,
		scheduler:    sch,
		msgQueue:     msgQueue,
		config:       opts.Config,
		node:         newNode(peerMgr),
		errCh:        make(chan error, 2),
	}

	s.node.SetConnectionFactory(func(addr, id, name string, frontend bool) error {
		logx.Dbg.Printf("[Server/connectionFactory] connecting to %s (id=%s, name=%s, frontend=%v)", addr, id, name, frontend)
		var conn *PeerConn
		var err error
		for i := range 3 {
			if i > 0 {
				logx.Dbg.Printf("[Server/connectionFactory] retry %d connecting to %s", i, addr)
				time.Sleep(time.Second)
			}
			conn, err = NewOutboundPeerConn(peerMgr, sch, s.node, addr, s.clusterHandler.MessageHandler)
			if err != nil {
				logx.War.Printf("[Server/connectionFactory] dial %s failed (attempt %d): %v", addr, i+1, err)
				continue
			}
			break
		}
		if err != nil {
			return fmt.Errorf("failed to connect to %s after retries: %w", addr, err)
		}
		conn.BindID(session.SessionID(id))
		if err := conn.SendTypePb(int8(protocol.ClusterHandshake), &clusterpb.N2MOnHandshake{ID: id, Name: name, Frontend: frontend}); err != nil {
			_ = conn.Close()
			return fmt.Errorf("send conn packet to %s: %w", addr, err)
		}
		logx.Dbg.Printf("[Server/connectionFactory] Conn packet sent to %s", addr)
		return nil
	})

	s.clientHandler = NewClientHandler(clientMgr, modelManager, msgQueue, s.node)
	s.clusterHandler = NewClusterHandler(clientMgr, peerMgr, modelManager, sch, msgQueue, s.node)
	s.httpServer = &HTTPServer{modelManager: s.modelManager, errCh: s.errCh}

	return s
}

func (s *server) ClusterNode() *Node { return s.node }

func (s *server) Errors() <-chan error { return s.errCh }

func (s *server) ListenClient(addr string) error {
	logx.Inf.Printf("[START] Client TCP listener at Addr: %s is starting", addr)
	ln, err := netpoll.CreateListener("tcp", addr)
	if err != nil {
		return err
	}
	s.clientPoll, err = netpoll.NewEventLoop(
		s.clientHandler.OnRequest,
		netpoll.WithOnPrepare(s.clientHandler.OnPrepare),
		netpoll.WithOnDisconnect(s.clientHandler.OnDisconnect),
	)
	if err != nil {
		ln.Close()
		return err
	}
	go func() {
		if err = s.clientPoll.Serve(ln); err != nil {
			select {
			case s.errCh <- fmt.Errorf("client listener: %w", err):
			default:
			}
		}
	}()
	return nil
}

func (s *server) ListenCluster(addr string) error {
	logx.Inf.Printf("[START] Cluster TCP listener at Addr: %s is starting", addr)
	ln, err := netpoll.CreateListener("tcp", addr)
	if err != nil {
		return err
	}
	s.clusterPoll, err = netpoll.NewEventLoop(
		s.clusterHandler.OnRequest,
		netpoll.WithOnPrepare(s.clusterHandler.OnPrepare),
		netpoll.WithOnDisconnect(s.clusterHandler.OnDisconnect),
	)
	if err != nil {
		ln.Close()
		return err
	}
	go func() {
		if err = s.clusterPoll.Serve(ln); err != nil {
			select {
			case s.errCh <- fmt.Errorf("cluster listener: %w", err):
			default:
			}
		}
	}()
	return nil
}

func (s *server) ListenWeb(addr string) {
	logx.Inf.Printf("[START] HTTP Server listener at Addr: %s is starting", addr)
	s.httpServer.Listen(addr)
}

func (s *server) Run(ctx context.Context) {
	cg := make(chan os.Signal, 1)
	signal.Notify(cg, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	<-cg
	if err := s.Shutdown(ctx); err != nil {
		logx.Err.Printf("[Server/Run] shutdown error: %v", err)
	}
}

func (s *server) Shutdown(ctx context.Context) error {
	xctx, cancel := context.WithTimeout(ctx, s.config.ShutdownTimeout)
	defer cancel()
	s.msgQueue.Close()
	s.scheduler.Stop()
	s.modelManager.Stop()
	s.clientMgr.Range(func(s session.Session) error { return s.Close() })
	s.peerMgr.Range(func(s session.Session) error { return s.Close() })
	if s.clusterPoll != nil {
		_ = s.clusterPoll.Shutdown(xctx)
	}
	if s.clientPoll != nil {
		_ = s.clientPoll.Shutdown(xctx)
	}
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(xctx); err != nil {
			logx.Err.Printf("[Server/Shutdown] http shutdown error: %v", err)
		}
	}
	return nil
}
