# infra-foundation 架构优化方案

## 项目现状

本项目是一个基于 Go 的分布式游戏/实时通信服务器基础框架，采用模块化 + Actor-like 架构设计。

### 已完成的优化（阶段一至阶段四）

1. **Transport 层抽象**：提取 `transport.Conn` 统一封装心跳、写队列、编解码等公共网络能力。
2. **包结构拆分**：将 God Package `cluster` 拆分为 `transport`、`clusterpb`、`processor`、`cluster` 四层。
3. **配置化**：引入 `config.Config` 集中管理所有 Magic Number（心跳、队列、时间轮、etcd、协议头等）。
4. **Buffer Pool**：`packet.PackCodec` 引入分级 `sync.Pool`，降低高并发下的 GC 压力。
5. **Session/Messenger 分离**：`session.Base` 统一委托 `Send/Notify/Close` 给 `Messenger`，消除了三套连接实现中的重复代码。
6. **ProxySession**：将 `acceptor` 重构为语义明确的离线会话代理，不再欺骗性地实现 `Session`。
7. **Model Handler 优化**：用 `map[int32]*handler` + `RWMutex` 替代全局 `sync.Map`；新增泛型注册接口 `RegisterHandlerFunc[T]`。
8. **Scheduler 批量消费**：`runExecutor` 从单任务消费改为整批消费，减少锁竞争。
9. **WorkMessage Task 池化**：引入 `workTask` + `sync.Pool`，降低闭包分配开销。
10. **Data Race 修复**：修复 `connmanager.Count()` 和 `Range()` 的并发安全问题。
11. **请求上下文**：引入 `session.Context`，支持 TraceID、Deadline 等扩展。
12. **TimerWheel 锁粒度优化**：每个 slot 独立锁，消除全局串行瓶颈。
13. **协议头扩展**：增加 Magic（0xABCD）、Version、CRC32 校验，提升安全性。
14. **连接拓扑显式化**：用 `config.ConnectionPolicy` 替代隐式的 `localID > nodeID` 规则。
15. **ServiceRegistry 去重**：`AddNode` 更新而非重复追加，安全重建路由映射。
16. **LoadBalancer 健康检查**：支持 `MarkHealthy`，优先选择健康节点，支持无健康状态时的降级。
17. **Metrics 埋点**：新建 `metrics` 包，在连接管理、消息队列、Model 分发、定时器、网络发送等关键路径埋点，通过 `/debug/metrics` 暴露 JSON 指标。

---

## 核心架构分层

| 层级 | 包名 | 职责 |
|------|------|------|
| 网络传输层 | `transport` | 统一封装 TCP 连接的写队列、心跳、编解码 |
| 协议层 | `packet` | 二进制协议（Magic/Version/CRC32/Length/Type/ID/SID/Payload）|
| 会话抽象层 | `session` | `Session` 接口 + `Messenger` 委托 + `Context` 请求上下文 |
| 消息处理层 | `processor` | 多队列工作池，保证 Session 级消息顺序 |
| 集群层 | `cluster` | 服务发现（etcd）、负载均衡、路由、节点代理、离线代理 |
| 业务模型层 | `model` | Actor-like Mailbox，零反射消息分发 |
| 调度层 | `scheduler` | 时间轮定时器，slot 级锁粒度 |
| 可观测性 | `metrics` | 原子计数器/仪表盘，HTTP JSON 暴露 |

---

## 附录：关键设计原则

1. **分层清晰**：`transport`（网络）→ `packet`（协议）→ `session`（会话抽象）→ `processor`（消息分发）→ `model`（业务 Actor）。
2. **零反射**：业务层通过 `RegisterHandlerFunc[T]` 直接注册带类型的 handler，框架内部不依赖反射分发消息。
3. **配置优先**：所有可调参数通过 `config.Config` 注入，杜绝 Magic Number。
4. **池化降 GC**：`packet` 打包缓冲区、`workTask` 均通过 `sync.Pool` 分级复用。
5. **显式优于隐式**：连接策略、健康状态、节点去重均以显式 API/配置表达，拒绝黑魔法。
