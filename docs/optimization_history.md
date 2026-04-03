# 架构优化历史记录

## 阶段一：Transport 抽象 + 包拆分 + Config/Buffer Pool

### 背景
用户要求站在架构师视角，结合当前分布式实时系统最佳实践，对 `infra-foundation` 进行优先优化。我作为架构师分析出 3 个最优先事项：
1. 统一 Transport 接口
2. 拆分 `cluster` God Package
3. 引入 Config 配置抽象 + Buffer Pool

### 执行过程

#### 1.1 创建 `transport` 包
- 新建 `transport/conn.go`，定义 `WriteCloser` 最小接口。
- 实现 `Conn` 结构体，统一封装心跳、写队列（`chan []byte` 替代 `queue.Queue`）、`PackCodec`、Send/Close 逻辑。
- **修复内存泄漏**：原 `cluster/connection.go` 的 `SendPack` 未调用 `pack.Free()`，`transport.Conn` 已修正。
- `NetPollConnection`、`ClientConnection`、`TCPClient` 全部改为复用 `transport.Conn`。
- 删除 `cluster/connection.go`。

#### 1.2 拆分 `cluster` 包
- 新建 `clusterpb` 包，迁移 `cluster.pb.go` 和 `cluster_meta.pb.go`。
- 新建 `processor` 包，迁移 `work_message.go`。
- 更新所有引用点（`N2MOnConnection`、`M2NOnConnection` 等），编译通过。

#### 1.3 Config + Buffer Pool
- 新建 `config/config.go`，集中管理：心跳参数、队列参数、时间轮参数、etcd 参数、shutdown 超时等。
- `packet/codec.go` 增加 6 级 `sync.Pool`（256/512/1K/4K/16K/64K），`Pack` 不再每次 `make([]byte, total)`。
- `transport.Conn.writeLoop` 发送后安全回收 buffer。
- 将各模块的 Magic Number 全部替换为 `config.Default.*`。

### 阶段一结果
- 编译通过，测试通过。
- 消除了约 150 行重复代码，架构从单层耦合变为四层分离。

---

## 阶段二：Session-Messenger 分离 + Model 优化 + Scheduler 批量消费 + Task 池化

### 背景
在阶段一的基础上，进一步深入解决根因级结构问题：
- `Session` 接口被 `acceptor` 欺骗性实现（无连接却假装有连接）。
- `connmanager` 存在 data race。
- Model 层使用 `sync.Map` + 反射分发消息，性能差。
- Scheduler 单任务消费锁竞争严重。
- WorkMessage 闭包分配造成 GC 压力。

### 执行过程

#### 2.1 修复 `connmanager` 数据竞争
- `Count()` 增加 `RLock` 保护。
- `Range()` 改为在锁内复制 `[]session.Session`，再解锁遍历，避免遍历期间 map 被修改导致 panic。

#### 2.2 Session 与 Messenger 分离
- 在 `session` 包中新增 `Messenger` 接口（`Send/Notify/Close`）。
- 新增 `session.Base`，将 `Send/Notify/Close` 统一委托给注入的 `Messenger`。
- `NetPollConnection`、`ClientConnection`、`TCPClient` 全部改为 `*session.Base + 自定义 Messenger` 模式。
- 三套连接各自的 `Send/Notify/Close` 逻辑集中到对应的 `Messenger` 实现中，彻底消除重复代码。

#### 2.3 重构 `acceptor` 为 `ProxySession`
- 删除 `cluster/acceptor.go`。
- 新建 `cluster/session_proxy.go`，定义 `ProxySession`。
- `ProxySession` 明确为"离线玩家会话代理"，持有独立 `PackCodec`，通过 `proxyMessenger` 将消息转发到 Gate 节点。
- `message_handler.go` 中的 `newAcceptor` 调用点改为 `NewProxySession`。

#### 2.4 清理私有 `sender` 接口
- 删除 `node.go` 中的 `type sender interface`。
- 所有 `type assert` 改为匿名公开接口：
  ```go
  conn.(interface{ SendData([]byte) error })
  ```

#### 2.5 Model 层 handler 优化
- 用 `map[int32]*handler` + `sync.RWMutex` 替代全局 `sync.Map`。
- `IsLocalHandler`、`HandlersRoutes`、`DispatchLocalAsync` 全部直接 map 查找。
- 新增泛型注册函数 `RegisterHandlerFunc[T]`，允许业务层零反射注册带类型的 handler。

#### 2.6 Scheduler 批量取任务
- `runExecutor` 从"每次取 1 个任务"改为"一次性取走当前所有任务 batch"，然后解锁执行。
- 显著降低 `sync.Mutex` 的切换频率。

#### 2.7 WorkMessage Task 池化
- 定义 `workTask` 结构体包装 `fn func()`。
- 使用 `sync.Pool` 复用 `workTask`。
- `runLoop` 执行完毕后回收对象，降低闭包相关的堆分配。

#### 2.8 连接体字段调整
- `NetPollConnection` 和 `ClientConnection` 显式暴露 `Conn *transport.Conn`，所有对 `PackCodec` 的访问改为 `sconn.Conn.PackCodec`，语义更清晰。

### 阶段二结果
- 编译通过，全部测试通过（`go test ./...`）。
- 架构的"肌肉"（实现细节）已接近生产可用水平，核心骨架健康。

---

## 阶段三：请求上下文 Context + 死代码清理 + TimerWheel 锁粒度优化

### 背景
- 消息从网络到业务仍然是裸的 `[]byte` + `session.Session`，缺少 Trace ID、Deadline 等上下文能力。
- `queue/queue.go` 已无人使用，属于死代码。
- `TimerWheel` 使用单把全局锁，定时器数量大时会成为瓶颈。

### 执行过程

#### 3.1 引入请求上下文 `session.Context`
- 在 `session/session.go` 中新增：
  ```go
  type Context struct {
      context.Context
      Session Session
      MsgID   int32
  }
  ```
- 将 `session.HandlerFunc` 签名从 `func(Session, ProtoMessage)` 升级为 `func(*Context, ProtoMessage)`。
- 修改 `model/interface.go`：
  - `RegisterHandler` 和 `RegisterHandlerFunc[T]` 的回调签名同步更新。
  - 泛型注册示例改为 `func(ctx *session.Context, pb *MyProto)`。
- 修改 `model/model_manager.go`：
  - `DispatchLocalAsync` 在投递任务前构造 `session.Context` 并传入 handler。

#### 3.2 清理死代码
- 安全删除 `queue/queue.go` 及整个 `queue` 目录（`transport.Conn` 已改用 `chan []byte`）。

#### 3.3 优化 `TimerWheel` 锁粒度
- 新增 `timerSlot` 结构体，每个 slot 拥有独立的 `sync.Mutex` 和 `*TimerList`。
- `TimerWheel.current` 改为 `atomic.Int64`，供 `plan` 无锁读取。
- `index` map 使用独立的 `sync.RWMutex`（`indexMu`）保护。
- `addTimer`：仅锁目标 slot + 写 indexMu。
- `cancelTimer`：先 RLock indexMu 查找 timer，再锁对应 slot 删除，最后 Lock indexMu 删除。
- `tickerHandler`：只锁当前 slot 扫描并收集任务；对于 recurring timer，先记录到 slice，解锁后再逐个插入目标 slot 并更新 index，避免死锁。
- pendingTasks 的读写使用独立的 `pendingMu` 保护。

### 阶段三结果
- 编译通过，全部测试通过（`go test ./...`）。
- 框架现在支持请求级上下文扩展（TraceID、Deadline 等）。
- 调度器在定时器密集场景下的并发性能显著提升。

---

## 阶段四：协议头扩展 + 连接拓扑显式化 + ServiceRegistry 去重 + Metrics 埋点

### 背景
- 协议头过于简单，缺少 Magic、Version、Checksum，无法快速识别非法连接和数据损坏。
- `NodeAgent.Unmarshal` 使用 `localID > nodeID` 的隐式连接规则，难以理解且强依赖数字 ID。
- `ServiceRegistry.AddNode` 无条件 append，可能重复添加同一节点。
- `LoadBalancer` 纯随机，没有健康检查过滤。
- 框架完全没有 metrics，无法做容量规划和故障排查。

### 执行过程

#### 4.1 协议头扩展
- 修改 `packet/codec.go`：
  - 新协议格式：`[2:Magic] [1:Version] [4:Length] [1:Type] [4:ID] [4:CRC32] [8:SID?] [Payload]`
  - `HeadLength` 从 9 扩展为 **16**，增加了 `0xABCD` Magic、`0x01` Version、CRC32 校验。
  - `NextPacket` / `Unpack1` / `Unpack` 均增加 Magic/Version/CRC 校验，可快速拒绝非法连接和损坏数据。
  - 新增错误类型：`ErrWrongMagic`、`ErrWrongVersion`、`ErrChecksumMismatch`。
- 修改 `config/config.go`：
  - 新增 `ProtocolMagic`、`ProtocolVersion`、`ProtocolEnableChecksum`。
- **修复 bug**：`Unpack` 中 `offset` 因 SID 偏移被重复加了 8，导致 payload 长度计算错误。调试测试已确认修复。

#### 4.2 连接拓扑显式化
- 修改 `config/config.go`：
  - 新增 `ConnectionPolicy` 类型和四个策略常量：`ConnectPolicyAll`、`ConnectPolicyNone`、`ConnectPolicyFrontendToBackend`、`ConnectPolicyBackendToFrontend`。
- 修改 `cluster/node.go`：
  - `NodeAgent` 新增 `connectionPolicy` 字段。
  - 新增 `shouldConnectTo(node *NodeInfo) bool` 方法，根据策略显式决定是否主动连接。
  - `Unmarshal` 中删除 `strconv.ParseInt` 和 `localID > nodeID` 的隐式规则。

#### 4.3 ServiceRegistry 去重 + LoadBalancer 健康检查
- 修改 `cluster/service_registry.go`：
  - `AddNode` 增加去重逻辑：如果节点 ID 已存在，更新 `Addr/Frontend/Routes` 而不是重复 append。
  - 新增 `rebuildRoutesLocked` 方法，在去重或更新时安全重建 `routeID -> name` 映射。
- 修改 `cluster/load_balancer.go`：
  - `LoadBalancer` 新增 `health map[string]bool` 和 `sync.RWMutex`。
  - 新增 `MarkHealthy(nodeID string, healthy bool)` 和 `IsHealthy(nodeID string) bool`。
  - `Pick` 和 `PickFrontend` 增加健康过滤：优先选择健康节点；健康状态未初始化时降级为使用全部节点。

#### 4.4 Metrics & 可观测性埋点
- 新建 `metrics/metrics.go`：
  - 提供 `Counter`（原子计数器）和 `Gauge`（原子仪表盘）。
  - 提供 `CounterOf(name)` / `GaugeOf(name)` 全局注册方法。
  - 提供 `Snapshot()` 获取所有指标快照。
  - 提供 `ServeHTTP` 可直接挂载到 HTTP 路径暴露 JSON 格式指标。
- 修改 `cluster/http_request.go`：
  - 在 `ServeHTTP` 中拦截 `/debug/metrics` 路径，直接返回指标数据。
- 埋点指标见阶段四结束时的详细列表。

### 阶段四结果
- 编译通过，全部测试通过（`go test ./...`）。
- 协议安全性显著提升（Magic/Version/CRC32）。
- 集群连接策略从隐式黑魔法变为显式可配置。
- 服务注册去重 + 负载均衡健康检查使集群行为更健壮。
- 关键路径均已埋点，可通过 `curl http://addr/debug/metrics` 实时查看运行状态。

---

## 阶段五：命名标准化重构

### 背景
经过前四个阶段，架构已经具备生产级基础能力，但命名风格混乱：
- 文件名存在过度缩写（`c_request.go`、`s_request.go`）、拼写错误（`loglx_test.go`）。
- 类型名与文件不匹配（`NetPollConnection` vs `client_connection.go`）。
- 公私混用：`newNodeAgent`、`storeServer` 等语义不清的私有函数；`NodeAgent` 类型名不够直观。
- 变量缩写过多：`mc`、`hsvr`、`pb2`、`bdata`、`conn1` 等缩写降低可读性。
- 动词不统一：`getNodeByName`、`getGateNode`、`pick`、`hasRoute` 等多种前缀。

### 执行过程

#### 5.1 文件重命名

| 原文件名 | 新文件名 | 理由 |
|----------|----------|------|
| `cluster/c_request.go` | `cluster/client_handler.go` | 消除 `c_` 缩写，实际为 netpoll Client 的事件处理器 |
| `cluster/s_request.go` | `cluster/server_handler.go` | 消除 `s_` 缩写 |
| `cluster/netpoll_connection.go` | `cluster/server_conn.go` | 与 `ClientConn` 对称 |
| `cluster/client_connection.go` | `cluster/client_conn.go` | 统一 `Conn` 缩写 |
| `cluster/session_proxy.go` | `cluster/proxy_session.go` | 形容词前置更清晰 |
| `cluster/http_request.go` | `cluster/http_server.go` | 实际实现的是 HTTP Server |
| `cluster/etcd_service_discovery.go` | `cluster/etcd_discovery.go` | 精简命名 |
| `cluster/load_balancer.go` | `cluster/balancer.go` | 包内无需重复前缀 |
| `cluster/service_registry.go` | `cluster/registry.go` | 精简命名 |
| `cluster/remote_call.go` | `cluster/router.go` | 实际是路由转发逻辑 |
| `cluster/message_handler.go` | `cluster/cluster_handler.go` | 与 `ServerHandler` 区分，处理集群控制消息 |
| `processor/work_message.go` | `processor/msg_queue.go` | 实际是消息队列 |
| `logx/loglx_test.go` | `logx/logx_test.go` | 修正拼写错误 |
| `connmanager/connmanager.go` | `connmanager/session_manager.go` | 管理的是 Session，不是裸连接 |

#### 5.2 类型/结构体重命名

| 原名 | 新名 |
|------|------|
| `NetPollConnection` | `ServerConn` |
| `ClientConnection` | `ClientConn` |
| `ServerRequest` | `ServerHandler` |
| `ClientRequest` | `ClientHandler` |
| `NodeAgent` | `Node` |
| `WorkMessage` | `MsgQueue` |
| `PackCodec` | `Codec` |
| `HTTPRequest` | `HTTPServer` |
| `MessageContext` | `ClusterHandler` |
| `NetworkEntities` | `SessionEntity` |
| `defaultConnectionSession` | `sessionIDPool` |
| `model` (wrapper) | `modelActor` |
| `ConnManager` | `SessionManager` |

#### 5.3 函数重命名（动词统一）

| 原名 | 新名 | 理由 |
|------|------|------|
| `newNodeAgent` | `newNode` | 与类型对应 |
| `storeServer` | `bindServer` | 语义：绑定 ServerContext |
| `storeNodeConn` | `bindNodeConn` | 语义：记录节点连接 |
| `pick` | `selectNode` | 从负载均衡器中"选择"节点 |
| `getNodeByName` | `nodeBySession` | 根据 Session 获取已绑定的节点 |
| `getGateNode` | `gatewayBySession` | 根据 Session 获取 Gateway |
| `getServiceByRoute` | `serviceByRoute` | 精简 `get` 前缀，Go 惯用法 |
| `notifyCloseSession` | `broadcastSessionClose` | 向各节点广播会话关闭事件 |
| `Marshal` | `encodeRegistry` | 明确是编码 ServiceRegistry 数据 |
| `Unmarshal` | `decodeRegistry` | 明确是解码并更新 ServiceRegistry |
| `RegisterHandler` | `RegisterMsgHandler` | 避免与 Go 通用 `http.Handler` 混淆 |
| `RegisterHandlerFunc` | `RegisterTypedHandler` | 强调泛型类型安全 |
| `DispatchLocalAsync` | `Dispatch` | 包内默认就是本地异步分发 |
| `DispatchHttpSync` | `DispatchHTTP` | Go 惯用全大写缩写 |
| `PostFunc` | `Post` | 与 Scheduler 的 `Push` 区分，Actor 层用 Post |
| `DoAsync` | `Forward` | 将任务转发到另一个 Actor 执行 |
| `PushInfiniteTimer` | `ScheduleTimer` | 含义更清晰 |
| `SessionID` (on pool) | `NextID` | 更直观：获取下一个可用 ID |

#### 5.4 变量重命名（消除无意义缩写）

| 原变量名 | 新名 |
|----------|------|
| `bdata` | `data` / `buf` / `payload`（根据上下文） |
| `pb2` | `notifyPB` |
| `mc` | `clusterHandler` / `msgHandler` |
| `hsvr` | `httpServer` |
| `svrrequest` | `serverHandler` |
| `conn1` | `sender` |
| `ctxKeyConn` / `ctxKeyConnection` | `connContextKey` / `connCtxKey` |

### 阶段五结果
- 编译通过，全部测试通过（`go test ./...`）。
- 命名风格统一，代码可读性显著提升。
- 文件名、类型名、函数名、变量名符合 Go 社区惯用法。

---

## 最终架构总评

经过五个阶段的重构与优化，`infra-foundation` 已经从原型演化为**分层清晰、性能优化到位、具备生产级基础能力**的分布式实时通信服务器框架。

### 架构分层（最终版）

| 层级 | 包名 | 职责 | 核心类型 |
|------|------|------|----------|
| 网络传输层 | `transport` | TCP 连接抽象、心跳、写队列、编解码 | `Conn`, `WriteCloser` |
| 协议层 | `packet` | 二进制协议编解码 | `Codec`, `Packet` |
| 会话抽象层 | `session` | Session 接口、Messenger 委托、请求上下文 | `Base`, `SessionEntity`, `Context` |
| 消息处理层 | `processor` | 多队列消息分发 | `MsgQueue` |
| 集群层 | `cluster` | 服务发现、负载均衡、路由、节点代理 | `Node`, `Registry`, `Balancer`, `Router`, `ProxySession` |
| 业务模型层 | `model` | Actor-like Mailbox，零反射消息分发 | `ModelManager`, `modelActor` |
| 调度层 | `scheduler` | 时间轮定时器 | `Scheduler`, `TimerWheel` |
| 可观测性 | `metrics` | 指标收集与暴露 | `Counter`, `Gauge` |
| 工具 | `config`, `logx`, `pcall`, `retry`, `localipaddr` | 配置、日志、工具函数 | - |

### 命名规范总结

1. **文件命名**：无缩写、无歧义、无拼写错误，文件名与主类型一致（`server_conn.go` 存放 `ServerConn`）。
2. **类型命名**：公开类型首字母大写，私有实现用显式后缀（`modelActor` 而非 `model`）。
3. **函数命名**：
   - `NewXxx` 公开构造函数，`newXxx` 私有构造函数。
   - `Get` 仅用于可能失败的查询；`Bind` 用于建立关联；`Select` 用于选择；`Dispatch` 用于分发。
   - Go 惯用缩写：`Conn` 替代 `Connection`，`HTTP` 替代 `Http`。
4. **变量命名**：消除 `bdata`、`pb2`、`mc` 等无意义缩写，使用语义完整的名称。

### 待完善项（非阻塞，后续可选）

1. **协议头预留 TraceID**：当前 `Context` 已引入，但协议二进制头中尚未分配 TraceID 字段。后续可在 HeadLength 中再增加 8 字节 TraceID。
2. **LoadBalancer 策略扩展**：目前只有随机策略，可补充一致性哈希（用于 Session Sticky）。
3. **metrics 直方图（Histogram）**：当前只有 Counter/Gauge，如需统计延迟分布，可引入直方图。
4. **etcd 健康检查联动**：当前 `LoadBalancer.MarkHealthy` 需要业务层或外部心跳机制主动调用，未来可将 netpoll 心跳超时与 LoadBalancer 健康状态自动联动。
5. **集成测试**：当前单元测试覆盖有限，建议补充集成测试验证集群行为。

---

**优化至此完成。**
