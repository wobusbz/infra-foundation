package config

import "time"

// Config 集中管理框架内所有可调参数，避免 Magic Number 散落各处。
// 参考文章中的灵活性需求：一个好的框架应该能通过配置而非硬编码来适应不同环境。
type Config struct {
	// Network & Heartbeat
	NetPollHeartbeatInterval     time.Duration
	NetPollHeartbeatTimeoutCount int32
	ClientHeartbeatInterval      time.Duration
	ClientHeartbeatFailCount     int32
	TCPClientHeartbeatInterval   time.Duration

	// Server Lifecycle
	ShutdownTimeout time.Duration

	// WorkMessage Queue
	ReaderQLen           int
	ReaderQNum           int64
	ReaderQTimeout       time.Duration
	ReaderQWarnThreshold int

	// Scheduler
	SchedulerTick    time.Duration
	SchedulerSlotNum int
	SchedulerTaskCap int

	// Etcd Service Discovery
	EtcdTTL         int64
	EtcdDialTimeout time.Duration
	EtcdOpTimeout   time.Duration

	// Transport Write Queue
	TransportWriteQueueSize int

	// Protocol
	ProtocolMagic        uint16
	ProtocolVersion      byte
	ProtocolEnableChecksum bool

	// Cluster Connection Policy
	ConnectionPolicy ConnectionPolicy
}

// ConnectionPolicy 定义节点间的主动连接策略。
type ConnectionPolicy int

const (
	// ConnectPolicyAll 表示本节点会主动连接所有其他节点（除了自己）。
	ConnectPolicyAll ConnectionPolicy = iota
	// ConnectPolicyNone 表示本节点不会主动连接任何节点，完全等待被动连接。
	ConnectPolicyNone
	// ConnectPolicyFrontendToBackend 表示只有 Frontend 节点会主动连接 Backend 节点。
	ConnectPolicyFrontendToBackend
	// ConnectPolicyBackendToFrontend 表示只有 Backend 节点会主动连接 Frontend 节点。
	ConnectPolicyBackendToFrontend
)

// NewDefault 返回一套适用于大多数场景的默认配置。
func NewDefault() *Config {
	return &Config{
		NetPollHeartbeatInterval:     time.Second * 5,
		NetPollHeartbeatTimeoutCount: 2,
		ClientHeartbeatInterval:      time.Second * 3,
		ClientHeartbeatFailCount:     3,
		TCPClientHeartbeatInterval:   time.Second * 3,
		ShutdownTimeout:              time.Second * 5,
		ReaderQLen:                   1 << 9,
		ReaderQNum:                   int64(1 << 3),
		ReaderQTimeout:               time.Second * 3,
		ReaderQWarnThreshold:         400,
		SchedulerTick:                time.Second,
		SchedulerSlotNum:             1024,
		SchedulerTaskCap:             4096,
		EtcdTTL:                      5,
		EtcdDialTimeout:              5 * time.Second,
		EtcdOpTimeout:                5 * time.Second,
		TransportWriteQueueSize:      256,
		ProtocolMagic:                0xABCD,
		ProtocolVersion:              0x01,
		ProtocolEnableChecksum:       true,
		ConnectionPolicy:             ConnectPolicyAll,
	}
}

// Default 是全局默认配置实例，便于在框架内部快速引用。
// 在正式生产环境中，建议通过读取配置文件后覆盖此变量，或显式注入到 NewServer() 中。
var Default = NewDefault()
