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

type Server interface {
	ConnMgr() *session.Manager
	ModelManager() *model.ModelManager
	Scheduler() *scheduler.Scheduler
	TaskQueue() *queue.TaskQueue
	ClusterNode() *Node
	ListenClient(addr string) error
	ListenCluster(addr string) error
	ListenWeb(addr string)
	Run(ctx context.Context)
	Shutdown(ctx context.Context) error
}

type ServerOption func(*serverOptions)

type serverOptions struct {
	connMgr      *session.Manager
	modelManager *model.ModelManager
	scheduler    *scheduler.Scheduler
	msgQueue     *queue.TaskQueue
	config       *config.Config
}

func WithConnMgr(cm *session.Manager) ServerOption {
	return func(o *serverOptions) {
		o.connMgr = cm
	}
}

func WithModelManager(mm *model.ModelManager) ServerOption {
	return func(o *serverOptions) {
		o.modelManager = mm
	}
}

func WithScheduler(sch *scheduler.Scheduler) ServerOption {
	return func(o *serverOptions) {
		o.scheduler = sch
	}
}

func WithWorkMsg(wm *queue.TaskQueue) ServerOption {
	return func(o *serverOptions) {
		o.msgQueue = wm
	}
}

func WithConfig(cfg *config.Config) ServerOption {
	return func(o *serverOptions) {
		o.config = cfg
	}
}

type server struct {
	clientHandler  *ClientHandler
	clusterHandler *ClusterHandler
	clientPoll     netpoll.EventLoop
	clusterPoll    netpoll.EventLoop
	connMgr        *session.Manager
	modelManager   *model.ModelManager
	scheduler      *scheduler.Scheduler
	msgQueue       *queue.TaskQueue
	config         *config.Config
	httpServer     *HTTPServer
	node           *Node
}

var _ Server = (*server)(nil)

func NewServer(options ...ServerOption) Server {
	opts := &serverOptions{
		config: config.NewDefault(),
	}
	for _, opt := range options {
		opt(opts)
	}

	connMgr := opts.connMgr
	if connMgr == nil {
		connMgr = session.NewManager()
	}

	modelManager := opts.modelManager
	if modelManager == nil {
		modelManager = model.DefaultModelManager
	}

	sch := opts.scheduler
	if sch == nil {
		sch = scheduler.NewScheduler()
	}

	msgQueue := opts.msgQueue
	if msgQueue == nil {
		msgQueue = queue.NewTaskQueue()
	}

	s := &server{
		connMgr:      connMgr,
		modelManager: modelManager,
		scheduler:    sch,
		msgQueue:     msgQueue,
		config:       opts.config,
		node:         newNode(connMgr, modelManager, sch, msgQueue),
	}

	s.node.SetConnectionFactory(func(addr, id, name string, frontend bool) error {
		logx.Dbg.Printf("[Server/connectionFactory] connecting to %s (id=%s, name=%s, frontend=%v)", addr, id, name, frontend)
		conn := NewOutboundPeerConn(s)

		var err error
		for i := range 3 {
			if i > 0 {
				logx.Dbg.Printf("[Server/connectionFactory] retry %d connecting to %s", i, addr)
				time.Sleep(time.Second)
			}
			if err = conn.DialConnection(addr); err != nil {
				logx.War.Printf("[Server/connectionFactory] dial %s failed (attempt %d): %v", addr, i+1, err)
				continue
			}
			break
		}
		if err != nil {
			return fmt.Errorf("failed to connect to %s after retries: %w", addr, err)
		}
		conn.BindID(session.SessionID(id))
		if err := conn.Conn.SendTypePb(int8(protocol.ClusterHandshake), &clusterpb.N2MOnConnection{ID: id, Name: name, Frontend: frontend}); err != nil {
			_ = conn.Close()
			return fmt.Errorf("send conn packet to %s: %w", addr, err)
		}
		logx.Dbg.Printf("[Server/connectionFactory] Conn packet sent to %s", addr)
		return nil
	})

	s.clientHandler = NewClientHandler(s)
	s.clusterHandler = NewClusterHandler(s)
	s.httpServer = &HTTPServer{modelManager: s.modelManager}

	return s
}

func (s *server) ModelManager() *model.ModelManager { return s.modelManager }

func (s *server) ConnMgr() *session.Manager { return s.connMgr }

func (s *server) Scheduler() *scheduler.Scheduler { return s.scheduler }

func (s *server) TaskQueue() *queue.TaskQueue { return s.msgQueue }

func (s *server) ClusterNode() *Node { return s.node }

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
		return err
	}
	go func() {
		if err = s.clientPoll.Serve(ln); err != nil {
			panic(err)
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
		return err
	}
	go func() {
		if err = s.clusterPoll.Serve(ln); err != nil {
			panic(err)
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
	if err := s.httpServer.Shutdown(ctx); err != nil {
		logx.Err.Printf("[Server/Run] http shutdown error: %v", err)
	}
}

func (s *server) Shutdown(ctx context.Context) error {
	xctx, cancel := context.WithTimeout(ctx, s.config.ShutdownTimeout)
	defer cancel()
	s.scheduler.Stop()
	s.modelManager.Stop()
	s.connMgr.Range(func(s session.Session) error { return s.Close() })
	s.node.PeerMgr().Range(func(s session.Session) error { return s.Close() })
	if s.clusterPoll != nil {
		_ = s.clusterPoll.Shutdown(xctx)
	}
	if s.clientPoll != nil {
		_ = s.clientPoll.Shutdown(xctx)
	}
	return nil
}
