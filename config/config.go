package config

import (
	"runtime"
	"strconv"
	"time"
)

type Config struct {
	NetPollHeartbeatInterval     time.Duration
	NetPollHeartbeatTimeoutCount int32
	ClientHeartbeatInterval      time.Duration
	ClientHeartbeatFailCount     int32
	TCPClientHeartbeatInterval   time.Duration

	ShutdownTimeout time.Duration

	ReaderQLen           int
	ReaderQNum           int64
	ReaderQTimeout       time.Duration
	ReaderQWarnThreshold int

	SchedulerTick    time.Duration
	SchedulerSlotNum int
	SchedulerTaskCap int

	EtcdTTL         int64
	EtcdDialTimeout time.Duration
	EtcdOpTimeout   time.Duration

	TransportWriteQueueSize    int
	TransportWriteQueueMode    WriteQueueMode
	TransportWriteQueueTimeout time.Duration

	ProtocolMagic          uint16
	ProtocolVersion        byte
	ProtocolEnableChecksum bool

	ConnectionPolicy ConnectionPolicy
}

type ConnectionPolicy int

const (
	ConnectPolicyAll ConnectionPolicy = iota
	ConnectPolicyNone
	ConnectPolicyFrontendToBackend
	ConnectPolicyBackendToFrontend
	ConnectPolicyByServicePriority
)

type WriteQueueMode int

const (
	WriteQueueModeDrop WriteQueueMode = iota
	WriteQueueModeBlock
	WriteQueueModeBlockWithTimeout
)

func ShouldConnectByPriority(localName, localID, targetName, targetID string) bool {
	localNum, _ := strconv.ParseInt(localID, 10, 64)
	targetNum, _ := strconv.ParseInt(targetID, 10, 64)
	return localNum < targetNum
}

func NewDefault() *Config {
	readerQNum := int64(runtime.NumCPU() * 2)
	if readerQNum <= 0 {
		readerQNum = 2
	}

	return &Config{
		NetPollHeartbeatInterval:     time.Second * 5,
		NetPollHeartbeatTimeoutCount: 2,
		ClientHeartbeatInterval:      time.Second * 3,
		ClientHeartbeatFailCount:     3,
		TCPClientHeartbeatInterval:   time.Second * 3,
		ShutdownTimeout:              time.Second * 5,
		ReaderQLen:                   1 << 14,
		ReaderQNum:                   readerQNum * 2,
		ReaderQTimeout:               time.Second * 10,
		ReaderQWarnThreshold:         15000,
		SchedulerTick:                time.Second,
		SchedulerSlotNum:             1024,
		SchedulerTaskCap:             4096,
		EtcdTTL:                      5 * 2,
		EtcdDialTimeout:              5 * time.Second,
		EtcdOpTimeout:                5 * 6 * time.Second,
		TransportWriteQueueSize:      4096,
		TransportWriteQueueMode:      WriteQueueModeDrop,
		TransportWriteQueueTimeout:   time.Second,
		ProtocolMagic:                0xABCD,
		ProtocolVersion:              0x02, // 0x02: string sid format
		ProtocolEnableChecksum:       true,
		ConnectionPolicy:             ConnectPolicyByServicePriority,
	}
}

var Default = NewDefault()
