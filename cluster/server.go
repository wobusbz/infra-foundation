package cluster

import (
	"context"
	"errors"
	"fmt"
	"infra-foundation/clusterpb"
	"infra-foundation/config"
	"infra-foundation/logx"
	"infra-foundation/model"
	"infra-foundation/protocol"
	"infra-foundation/queue"
	"infra-foundation/scheduler"
	"infra-foundation/session"
	"math"
	"os"
	"os/signal"

	"syscall"
	"time"

	"github.com/cloudwego/netpoll"
)

type ServerOption func(*ServerOptions)

type ServerOptions struct {
	ConnMgr    ClientStore
	Dispatcher ModelDispatcher
	Executor   TaskExecutor
	TaskQueue  *queue.TaskQueue
	Config     *config.Config
}

func WithModelManager(mm ModelDispatcher) ServerOption {
	return func(o *ServerOptions) {
		o.Dispatcher = mm
	}
}

type server struct {
	clientHandler  *ClientHandler
	clusterHandler *ClusterHandler
	clientPoll     netpoll.EventLoop
	clusterPoll    netpoll.EventLoop
	clientMgr      ClientStore
	peerMgr        PeerStore
	dispatcher     ModelDispatcher
	executor       TaskExecutor
	msgQueue       *queue.TaskQueue
	config         *config.Config
	httpServer     *HTTPServer
	node           *Node
	peerLifecycle  *PeerLifecycle
	peerConnector  *PeerConnector
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
		connMgr = session.NewManager[session.Session]()
	}

	dispatcher := opts.Dispatcher
	if dispatcher == nil {
		dispatcher = model.DefaultModelManager
	}

	executor := opts.Executor
	if executor == nil {
		executor = scheduler.NewScheduler()
	}

	msgQueue := opts.TaskQueue
	if msgQueue == nil {
		msgQueue = queue.NewTaskQueue()
	}

	clientMgr := connMgr
	peerMgr := session.NewManager[NodeConn]()

	s := &server{
		clientMgr:  clientMgr,
		peerMgr:    peerMgr,
		dispatcher: dispatcher,
		executor:   executor,
		msgQueue:   msgQueue,
		config:     opts.Config,
		node:       newNode(clientMgr, peerMgr, dispatcher),
		errCh:      make(chan error, 2),
	}

	s.node.SetConnectionFactory(s.connectToPeer)

	s.peerConnector = NewPeerConnector(
		s.node.router.registry,
		s.peerMgr,
		s.clientMgr,
		s.node.router.sessionBinder,
		s.connectToPeer,
		s.node.LocalNode,
	)

	lifecycle := NewFrontendLifecycle(dispatcher, clientMgr, s.node, &session.DefaultIDPool)
	s.clientHandler = NewClientHandler(dispatcher, msgQueue, s.node, lifecycle)
	s.peerLifecycle = NewPeerLifecycle(s.peerMgr, s.peerMgr, s.executor, s.node.LoadBalancer(), &session.DefaultIDPool)
	s.clusterHandler = NewClusterHandler(clientMgr, peerMgr, dispatcher, executor, msgQueue, s.node, s.peerLifecycle)
	s.httpServer = &HTTPServer{dispatcher: dispatcher, errCh: s.errCh}

	return s
}

func (s *server) connectToPeer(addr, id, name string, frontend bool) error {
	logx.Dbg.Printf("[Server/connectToPeer] connecting to %s (id=%s, name=%s, frontend=%v)", addr, id, name, frontend)
	var conn *PeerConn
	var err error
	for i := range math.MaxInt64 {
		if i > 0 {
			logx.Dbg.Printf("[Server/connectToPeer] retry %d connecting to %s", i, addr)
			time.Sleep(time.Second)
		}
		conn, err = NewOutboundPeerConn(s.peerLifecycle, addr, s.clusterHandler.Router(), s.msgQueue)
		if err != nil {
			logx.War.Printf("[Server/connectToPeer] dial %s failed (attempt %d): %v", addr, i+1, err)
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
	logx.Dbg.Printf("[Server/connectToPeer] Conn packet sent to %s", addr)
	return nil
}

func (s *server) ClusterNode() *Node { return s.node }

func (s *server) PeerConnector() *PeerConnector { return s.peerConnector }

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
	var errs = make([]error, 0, 6)
	if s.clusterPoll != nil {
		errs = append(errs, s.clusterPoll.Shutdown(xctx))
	}
	if s.clientPoll != nil {
		errs = append(errs, s.clientPoll.Shutdown(xctx))
	}

	errs = append(errs, s.clientMgr.Range(func(sess session.Session) error { return sess.Close() }))
	errs = append(errs, s.peerMgr.Range(func(peer NodeConn) error { return peer.Close() }))

	errs = append(errs, s.dispatcher.Stop())
	s.executor.Stop()
	s.msgQueue.Close()

	if s.httpServer != nil {
		errs = append(errs, s.httpServer.Shutdown(xctx))
	}
	return errors.Join(errs...)
}
