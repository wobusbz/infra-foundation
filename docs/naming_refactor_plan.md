# 命名标准化重构计划

## 现状问题

1. **文件命名混乱**：存在 `c_request.go`、`s_request.go` 等过度缩写；`loglx_test.go` 拼写错误；`http_request.go` 实际内容是 HTTP Server；`work_message.go` 实际是消息队列。
2. **类型名与文件不匹配**：`NetPollConnection` 放在 `netpoll_connection.go`，但与其他连接类型命名风格不一致。
3. **公私混用**：存在 `newNodeAgent`、`storeServer` 等语义不清的私有函数；同时 `NodeAgent` 作为核心类型名不够直观。
4. **变量缩写过多**：`mc`、`hsvr`、`pb2`、`bdata`、`errs` 等缩写降低了代码可读性。
5. **动词不统一**：同是获取节点，有 `getNodeByName`、`getGateNode`、`pick`、`hasRoute` 等多种前缀。

## 重构原则

1. **文件名 = 包内核心职责**：文件名应直接反映内容，杜绝 `c_xxx`、`s_xxx` 这类缩写。
2. **Go 命名规范优先**：
   - 公开类型/函数首字母大写，私有首字母小写。
   - 构造函数统一使用 `NewXxx`（若跨包使用）或 `newXxx`（若仅包内使用）。
   - `Connection` 在 Go 生态中通常缩写为 `Conn`。
3. **语义明确**：
   - `Get` 前缀仅用于“查询并返回对象+error/bool”。
   - `Bind` 用于建立关联关系。
   - `Select` 用于从多个候选中选择一个。
   - `Dispatch` 用于消息/任务分发。
   - `Encode/Decode` 用于序列化，替代语义模糊的 `Marshal/Unmarshal`（在特定上下文中）。
4. **消除无意义缩写**：`bdata` → `data`，`pb2` → `notifyPB`，`mc` → `msgHandler`，`hsvr` → `httpServer`。

## 具体改名映射表

### 一、文件重命名

| 原文件名 | 新文件名 | 理由 |
|----------|----------|------|
| `cluster/c_request.go` | `cluster/client_handler.go` | 消除 `c_` 缩写，实际为 netpoll Client 的事件处理器 |
| `cluster/s_request.go` | `cluster/server_handler.go` | 消除 `s_` 缩写，实际为 netpoll Server 的事件处理器 |
| `cluster/netpoll_connection.go` | `cluster/server_conn.go` | 与 `ClientConn` 对称，突出服务端连接 |
| `cluster/client_connection.go` | `cluster/client_conn.go` | 统一 `Conn` 缩写 |
| `cluster/session_proxy.go` | `cluster/proxy_session.go` | 形容词前置，语义更清晰 |
| `cluster/http_request.go` | `cluster/http_server.go` | 该文件实现的是 HTTP Server，不是 Request |
| `cluster/etcd_service_discovery.go` | `cluster/etcd_discovery.go` | 精简，discovery 已足够表达 |
| `cluster/load_balancer.go` | `cluster/balancer.go` | 包内无需重复 `load_` 前缀 |
| `cluster/service_registry.go` | `cluster/registry.go` | 包内简称即可 |
| `cluster/remote_call.go` | `cluster/router.go` | 实际是路由转发逻辑 |
| `cluster/message_handler.go` | `cluster/cluster_handler.go` | 与 `ServerHandler` 区分，处理集群控制消息 |
| `processor/work_message.go` | `processor/msg_queue.go` | 实际是消息队列/分发器 |
| `logx/loglx_test.go` | `logx/logx_test.go` | 修正拼写错误 |

### 二、类型/结构体重命名

| 原名 | 新名 | 文件 | 理由 |
|------|------|------|------|
| `NetPollConnection` | `ServerConn` | `server_conn.go` | 与 `ClientConn` 对称 |
| `ClientConnection` | `ClientConn` | `client_conn.go` | 统一 `Conn` 缩写 |
| `ServerRequest` | `ServerHandler` | `server_handler.go` | 处理 I/O 事件，不是 Request 实体 |
| `ClientRequest` | `ClientHandler` | `client_handler.go` | 同上 |
| `NodeAgent` | `Node` | `node.go` | `Agent` 是冗余后缀，Node 已足够 |
| `WorkMessage` | `MsgQueue` | `msg_queue.go` | 本质是队列，不是 Message 实体 |
| `PackCodec` | `Codec` | `packet/codec.go` | 包内简称即可 |
| `HTTPRequest` | `HTTPServer` | `http_server.go` | 实际是 HTTP Server |
| `MessageContext` | `ClusterHandler` | `cluster_handler.go` | 处理集群内部控制消息 |
| `NetworkEntities` | `SessionEntity` | `session/session.go` | `Entity` 单数更准确，避免复数歧义 |
| `defaultConnectionSession` | `sessionIDPool` | `session/session.go` | 实际是为 Session 分配 ID 的池 |
| `model` (wrapper) | `modelActor` | `model/model.go` | 避免与接口 `Model` 同名（仅大小写不同），明确其为 Actor 包装器 |
| `ConnManager` | `SessionManager` | `connmanager/connmanager.go` | 管理的是 `session.Session`，不是裸连接 |

### 三、函数重命名

#### cluster 包

| 原名 | 新名 | 所属类型 | 理由 |
|------|------|----------|------|
| `newNodeAgent` | `newNode` | 包级 | 与 `Node` 类型对应 |
| `storeServer` | `bindServer` | `Node` | 语义：将 Node 绑定到 ServerContext |
| `storeNodeConn` | `bindNodeConn` | `Node` | 语义：记录节点连接 |
| `pick` | `selectNode` | `Node` | 从负载均衡器中“选择”一个节点 |
| `getNodeByName` | `nodeBySession` | `Node` | 根据 Session 获取已绑定的节点 |
| `getGateNode` | `gatewayBySession` | `Node` | 根据 Session 获取 Gateway |
| `getServiceByRoute` | `serviceByRoute` | `Node` | 精简 `get` 前缀，Go 惯用法 |
| `hasRoute` | `hasRoute` | `Node` | 保持 |
| `notifyCloseSession` | `broadcastSessionClose` | `Node` | 向各节点广播会话关闭事件 |
| `Marshal` | `encodeRegistry` | `Node` | 明确是编码 ServiceRegistry 数据 |
| `Unmarshal` | `decodeRegistry` | `Node` | 明确是解码并更新 ServiceRegistry |
| `shouldConnectTo` | `shouldConnectTo` | `Node` | 保持 |

#### model 包

| 原名 | 新名 | 所属类型 | 理由 |
|------|------|----------|------|
| `RegisterHandler` | `RegisterMsgHandler` | 包级 | 避免与 Go 通用 `http.Handler` 混淆 |
| `RegisterHandlerFunc` | `RegisterTypedHandler` | 包级 | 强调泛型类型安全 |
| `DispatchLocalAsync` | `Dispatch` | `ModelManager` | 包内默认就是本地异步分发 |
| `DispatchHttpSync` | `DispatchHTTP` | `ModelManager` | Go 惯用全大写缩写，同步已隐含在阻塞调用中 |
| `PostFunc` | `Post` | `modelActor` | 与 `Scheduler.PushTask` 区分，Actor 层用 Post |
| `DoAsync` | `Forward` | `modelActor` | 将任务转发到另一个 Actor 执行 |

#### session 包

| 原名 | 新名 | 所属类型 | 理由 |
|------|------|----------|------|
| `SessionID` | `NextID` | `sessionIDPool` | 更直观：获取下一个可用 ID |

#### scheduler 包

| 原名 | 新名 | 所属类型 | 理由 |
|------|------|----------|------|
| `PushInfiniteTimer` | `ScheduleTimer` | `Scheduler` | `Infinite` 含义晦涩，`ScheduleTimer` 配合 `interval` + `recurring` 更清晰 |

### 四、变量重命名（重点消除缩写）

| 原变量名 | 建议新名 | 出现位置 |
|----------|----------|----------|
| `mc` | `msgHandler` | `server_handler.go`, `client_handler.go` |
| `hsvr` | `httpServer` | `server.go` |
| `svrrequest` | `serverHandler` | `server.go` |
| `pb2` | `notifyPB` | `proxy_session.go` |
| `bdata` | `data` / `payload` / `buf` | 各消息处理函数 |
| `errs` | `errList` | 多处 `errors.Join` 场景 |
| `conn1` | `sender` / `dataConn` | 类型断言后的变量 |

## 执行顺序

1. **文件重命名**：先改文件名，同步修改包内引用（若跨包）。
2. **类型重命名**：使用 IDE/批量替换，逐文件修改类型名和构造函数。
3. **函数重命名**：先改公开函数，再改私有函数；同步修改所有调用点。
4. **变量重命名**：最后清理局部变量缩写。
5. **编译与测试**：每完成一个包执行 `go build ./...`，全部完成后执行 `go test ./...`。

## 风险控制

- **Protobuf 生成文件不动**：`clusterpb/*.pb.go` 保持原样，避免破坏 protobuf 编译。
- **外部接口尽量稳定**：`Server` 接口、`Session` 接口、`Model` 接口的核心方法保持不变，仅调整实现层命名。
- **逐步替换**：每次只改一个包，编译通过后再进行下一个，避免一次性改动过大导致难以定位错误。
