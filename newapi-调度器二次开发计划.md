# new-api 调度器二次开发计划

合并时间：2026-07-02
项目版本：v1.0.0-rc.15
源码路径：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15`
目标文件：`C:/Users/liuyun/Desktop/新建文件夹/newapi-调度器二次开发计划.md`

## 1. 结论

这个功能可以做，但不能简单理解成把 `RetryTimes` 改成 3。new-api 当前的 `retry` 语义更接近“第几次重试对应第几个优先级层级”，而你想要的是“同优先级内，同一渠道连续失败达到阈值后禁用，再换同级渠道，同级耗尽后再降级”。这是两套不同的调度语义。

推荐方案：新增一层 `ChannelScheduler` 高级调度器，并通过配置开关灰度启用。不要把所有状态直接塞进 `controller/relay.go`，也不要复用 `RetryTimes` 表示“每个渠道重试几次”。

推荐落地顺序：

1. 先做独立调度日志和观察模式，不改变现有调度行为。
2. 再新增候选渠道列表能力，把当前“随机拿一个渠道”扩展成“按优先级分桶后的候选集”。
3. 新增 `ChannelScheduler`，默认关闭，通过配置开关接入。
4. 渠道临时禁用使用明确字段 `auto_disabled_until`，不要只塞进 `other_info`。
5. 独立调度日志第一版放主库 `DB`，不要放 `LOG_DB`，避免 ClickHouse 迁移和查询复杂度。
6. 前端 API 做一套；调度器操作面板放到 WebUI 左侧“渠道”入口对应的渠道管理页面里，调度日志放到 WebUI 左侧“使用日志”下面，作为独立日志页面。
7. 前端设计优先复用现有按钮、表格、筛选栏、弹窗、抽屉、颜色和布局，不重新设计一套视觉系统。

本计划只描述设计和实施路径，不包含源码修改。

## 2. 两份计划书的优点与合并取舍

原计划书的优点：

- A/B/C/D 的例子清楚，能直接说明你要的行为。
- 对当前请求转发、渠道选择、自动禁用、日志入口的描述比较直观。
- 风险提醒接地气，尤其是生产环境备份、流式请求、不要混改两套前端。
- 第一阶段建议“观察版”，这个方向很稳，适合作为真正开发的起点。

补全版计划书的优点：

- 补充了官方文档依据和项目规则，包括 Go + Gin + GORM、前端构建、数据库兼容和环境变量。
- 明确指出首次渠道选择在 `middleware.Distribute`，不能只改 `controller/relay.go` 或 `model.GetRandomSatisfiedChannel`。
- 补全了候选渠道列表 API、配置项、自动恢复、并发一致性、缓存路径和非缓存路径。
- 对两套前端、Dockerfile 构建、`LOG_SQL_DSN` 和 ClickHouse 风险说明更完整。
- 测试计划更细，覆盖 `MEMORY_CACHE_ENABLED=true/false`、auto 分组、手动禁用、流式请求等边界。

本合并版保留原计划书的可读性和例子，同时保留补全版的源码锚点、官方文档依据、工程边界和阶段拆分。

## 3. 官方文档与项目规则依据

已核对官方文档：

- New API 功能介绍：`https://docs.newapi.pro/en/docs/guide/wiki/basic-concepts/features-introduction`
- New API 技术架构：`https://docs.newapi.pro/en/docs/guide/wiki/basic-concepts/technical-architecture`
- 本地开发与部署：`https://docs.newapi.pro/en/docs/installation/development/local-development`
- 环境变量配置：`https://docs.newapi.pro/en/docs/installation/config-maintenance/environment-variables`

从官方文档和当前源码可以确认：

- new-api 后端是 Go + Gin + GORM。
- 项目有独立前端构建产物，并由 Go embed 提供静态文件。
- 数据库默认可用 SQLite，生产可使用 MySQL、PostgreSQL，并可能配置 Redis。
- 日志库可能通过 `LOG_SQL_DSN` 独立配置，且可能指向 ClickHouse。
- 渠道、分组、日志、系统设置已经是现有后台能力，二次开发应在这些体系内扩展。

已读取项目规则：

- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/AGENTS.md`
- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/CLAUDE.md`
- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default/AGENTS.md`

关键约束：

- 后端必须兼容 SQLite、MySQL、PostgreSQL。
- 新增 JSON marshal/unmarshal 应使用项目 `common/json.go` 包装或现有 `common.MapToJsonStr` 这类工具，避免直接引入新的 `encoding/json` 调用习惯。
- default 前端使用 React 19、TypeScript、TanStack Router、React Query、i18next。
- default 前端包管理和脚本优先 Bun。
- 改动 TS/TSX 后必须跑 `bun run typecheck` 或 `bun run build:check`。
- 不要改项目受保护品牌、模块路径、元数据。

## 4. 目标功能定义

以你的例子为准：

- A 渠道：priority=3
- B 渠道：priority=3
- C 渠道：priority=2
- D 渠道：priority=1

期望行为：

1. 先从最高优先级 priority=3 开始。
2. priority=3 里先选择 A。
3. A 连续失败达到 3 次后，自动禁用 A 2 小时。
4. 写入一条独立调度日志，记录失败原因、失败时间、渠道信息、请求信息和禁用到期时间。
5. 不立刻降到 C，而是继续尝试同优先级的 B。
6. B 连续失败达到 3 次后，也自动禁用 2 小时并写日志。
7. priority=3 没有可用渠道后，再降级到 priority=2 的 C。
8. C 也按同样规则处理，最后才到 D。

顺序可以理解为：

```text
priority=3: A 尝试 1 -> A 尝试 2 -> A 尝试 3
  A 达到阈值，auto disabled 2 小时，写调度日志
priority=3: B 尝试 1 -> B 尝试 2 -> B 尝试 3
  B 达到阈值，auto disabled 2 小时，写调度日志
priority=2: C 尝试 1 -> C 尝试 2 -> C 尝试 3
priority=1: D 尝试 1 -> D 尝试 2 -> D 尝试 3
```

成功条件：任一渠道成功即结束，不再继续降级。

失败条件：所有候选渠道都耗尽，返回最后一次可对用户解释的错误。

不应重试的情况：请求体错误、模型映射错误、参数转换错误、明确设置 `SkipRetry` 的错误、状态码命中 always skip retry 的错误，以及已经向客户端输出不可回滚内容的流式请求。

## 5. 现有请求调度链路

当前主链路可以简化为：

```text
middleware.Distribute
  -> service.CacheGetRandomSatisfiedChannel(retry=0)
  -> middleware.SetupContextForSelectedChannel
  -> controller.Relay
      -> relaycommon.GenRelayInfo
      -> for retry <= common.RetryTimes
          -> getChannel
          -> relay handler
          -> processChannelError
          -> shouldRetry
```

关键源码位置：

- 初始渠道选择在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/middleware/distributor.go:32`、`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/middleware/distributor.go:135`。
- 渠道上下文写入在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/middleware/distributor.go:443`。
- relay 主循环在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/controller/relay.go:181`、`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/controller/relay.go:191`。
- 重试取新渠道在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/controller/relay.go:293`。
- 失败处理和自动禁用入口在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/controller/relay.go:357`。
- 是否重试在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/controller/relay.go:325`。

必须注意：第一次请求的渠道已经由 `middleware.Distribute` 选好，后续 retry 才在 `controller/relay.go:getChannel` 里重新选择。所以高级调度器如果要完全控制 A/B/C/D 顺序，不能只改 `model.GetRandomSatisfiedChannel`，还要明确接管“首次选择”和“后续选择”的一致性。

## 6. 当前优先级与重试语义

### 6.1 当前已有能力

内存缓存路径：

- 初始化缓存并按 priority 倒序排序：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/channel_cache.go:26`、`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/channel_cache.go:71`。
- 根据 retry 选择第 N 个优先级：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/channel_cache.go:137`、`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/channel_cache.go:154`。
- 同优先级内按权重随机：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/channel_cache.go:158`、`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/channel_cache.go:195`。

数据库直查路径：

- priority 字段在 ability 上也有：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/ability.go:23`。
- 取 priority 列表并按 retry 选择：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/ability.go:63`、`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/ability.go:88`。
- 查询目标优先级渠道：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/ability.go:93`、`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/ability.go:108`。

自动分组路径：

- `service.RetryParam` 在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/service/channel_select.go:14`。
- auto 分组会把 retry 拆成 group 内 priority retry：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/service/channel_select.go:49`。
- 普通分组调用 `model.GetRandomSatisfiedChannel`：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/service/channel_select.go:157`。

### 6.2 当前行为与目标行为差异

| 项目 | 当前 new-api | 目标行为 |
| --- | --- | --- |
| 高优先级优先 | 已支持 | 继续复用 |
| 同优先级内选择 | 按权重随机拿一个 | 失败达到阈值后排除该渠道，再选同级其他渠道 |
| retry 语义 | retry 下标对应优先级层级 | 每个渠道可尝试 N 次，再同级换渠道 |
| 失败后禁用 | 符合禁用条件时立即 auto disabled | 该渠道本轮连续失败达到阈值后临时禁用 |
| 禁用时长 | 无明确到期字段 | 需要禁用到期时间，默认 7200 秒 |
| 失败日志 | 通用 logs，type=error | 独立 `channel_scheduler_logs` |
| auto 分组 | 每个分组消耗 priority retry | 同级耗尽和降级要与 auto 分组状态兼容 |

## 7. 推荐后端架构

推荐新增一层 `ChannelScheduler`。

它不负责真正发请求，而是负责回答这些问题：

1. 当前请求应该选哪个渠道。
2. 当前渠道已经失败几次。
3. 是否达到禁用阈值。
4. 同优先级是否还有可用渠道。
5. 是否允许降级到下一优先级。
6. 是否需要写调度日志。
7. 是否应该终止重试并返回错误。

建议新增或改造的后端文件：

| 文件 | 类型 | 作用 |
| --- | --- | --- |
| `service/channel_scheduler.go` | 新增 | 高级调度器核心逻辑 |
| `service/channel_scheduler_test.go` | 新增 | 调度算法单元测试 |
| `model/channel_scheduler_log.go` | 新增 | 独立调度日志表模型和查询函数 |
| `controller/channel_scheduler_log.go` | 新增 | 调度日志 API |
| `router/api-router.go` | 修改 | 注册调度日志路由 |
| `model/channel.go` | 修改 | 增加临时禁用到期字段 |
| `model/channel_cache.go` | 修改 | 增加“按优先级返回候选渠道列表”的能力 |
| `model/ability.go` | 修改 | 非内存缓存模式也要支持候选列表 |
| `model/main.go` | 修改 | AutoMigrate 新表和新字段 |
| `controller/relay.go` | 修改 | 接入新调度器，但尽量少改原逻辑 |
| `controller/channel.go` | 修改 | 渠道编辑接口补充每渠道调度配置 |
| `controller/channel_scheduler.go` | 新增 | 调度器状态、配置、手动恢复 API |
| `model/option.go` | 修改 | 增加调度器配置项 |

`ChannelScheduler` 的核心职责：

- 读取候选渠道列表。
- 按 priority 从高到低分桶。
- 在同 priority 内复用现有权重策略选择渠道。
- 维护单次请求内的失败次数。
- 判断同渠道是否继续重试、同级是否还有其他渠道、是否降级。
- 达到阈值时触发临时禁用和写独立调度日志。
- 尊重现有 `shouldRetry`、`service.ShouldDisableChannel`、`auto_ban`、channel affinity、auto group 的限制。

第一版建议采用“单次请求内计数”。它容易理解，风险较低，不需要处理跨节点的全局失败计数。未来如果要做全局连续失败计数，再考虑 Redis 原子计数和 TTL。

## 8. 候选渠道列表能力

当前模型层主要提供“拿一个渠道”的能力。高级调度器需要“拿一组候选渠道”。建议新增类似方法：

- `model.GetSatisfiedChannels(group, model, requestPath) ([]*Channel, error)`
- `model.GetSatisfiedChannelBuckets(group, model, requestPath) ([]ChannelPriorityBucket, error)`

要求：

- 内存缓存开启时走 `model/channel_cache.go`，复用 `filterChannelsByRequestPath`。
- 内存缓存关闭时走 `model/ability.go`，复用 ability 的 enabled、group、model、priority、weight。
- 两条路径返回顺序一致：priority DESC，同 priority 内保持可权重选择。
- 不要只改缓存路径，否则 `MEMORY_CACHE_ENABLED=false` 时行为会回退。
- Advanced Custom 渠道依赖 `requestPath` 过滤，这个过滤不能丢。

## 9. 临时禁用 2 小时设计

### 9.1 推荐字段

给 `channels` 表增加明确字段：

- `auto_disabled_until`
- Go 字段建议：`AutoDisabledUntil *int64`
- JSON：`auto_disabled_until`
- GORM：`bigint;default:0;index`

推荐原因：

- 查询快。
- 含义明确。
- 不需要每次解析 `other_info` JSON。
- SQLite、MySQL、PostgreSQL 都容易兼容。
- 方便前端展示“自动禁用到什么时候”。

不推荐只把到期时间塞进 `other_info`。虽然改动小，但查询和筛选会麻烦，三种数据库的 JSON 查询差异也会增加维护成本。

### 9.2 禁用逻辑

达到阈值后：

1. 确认 `service.ShouldDisableChannel(err)` 为 true。
2. 确认渠道 `auto_ban` 为 true，或配置允许忽略该开关。
3. 设置 `status = common.ChannelStatusAutoDisabled`。
4. 设置 `auto_disabled_until = now + SchedulerAutoDisableSeconds`，默认 7200 秒。
5. 将脱敏后的失败原因写入 `other_info.status_reason`。
6. 将禁用时间写入 `other_info.status_time`。
7. 更新 `ability.enabled=false`。
8. 更新内存缓存。
9. 写入 `channel_scheduler_logs`。

### 9.3 恢复逻辑

恢复规则：

1. 只恢复 `status=auto_disabled` 且 `auto_disabled_until > 0` 且 `auto_disabled_until <= now` 的渠道。
2. 不恢复手动禁用渠道。
3. 不恢复 `auto_disabled_until=0` 的老式 auto disabled 渠道，避免误启旧状态。
4. 恢复后清空 `auto_disabled_until`。
5. 恢复后更新 `ability.enabled=true`。
6. 多节点部署时，建议每次选候选渠道前做轻量恢复，或由主节点定时恢复后刷新缓存。

### 9.4 每渠道调度参数

全局配置只作为默认值。每个渠道还需要自己的调度参数，方便 A、B、C、D 采用不同策略：

| 字段 | 类型建议 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `scheduler_enabled` | bool | true | 该渠道是否参与高级调度 |
| `scheduler_retry_times` | int | null | 当前渠道连续失败几次后触发临时禁用；空值表示使用全局默认 |
| `scheduler_auto_disable_seconds` | int | null | 当前渠道连续失败后禁用多久；空值表示使用全局默认 |
| `scheduler_auto_recover_enabled` | bool | true | 到期后是否允许自动恢复 |
| `scheduler_manual_restore_allowed` | bool | true | 是否允许管理员在 WebUI 手动恢复 |
| `scheduler_last_disabled_until` | int64 | 0 | 最近一次调度器临时禁用到期时间，可与 `auto_disabled_until` 同步展示 |

推荐优先级：渠道级配置 > 全局配置 > 代码默认值。比如 A 渠道可以设置失败 3 次禁用 2 小时，B 渠道可以设置失败 2 次禁用 30 分钟，C 渠道可以关闭自动恢复，只允许人工恢复。

这些字段可以直接放在 `channels` 表，或者把低频配置放入结构化 JSON。第一版更推荐明确字段，原因和 `auto_disabled_until` 一样：查询、筛选、展示、迁移都更简单。

## 10. 独立调度日志表

建议新增表名：`channel_scheduler_logs`。

建议新增：

- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/channel_scheduler_log.go`
- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/controller/channel_scheduler_log.go`

字段建议：

| 字段 | 类型建议 | 说明 |
| --- | --- | --- |
| `id` | int | 主键 |
| `created_at` | int64 | 记录时间 |
| `request_id` | string | 本次请求 ID |
| `user_id` | int | 用户 ID |
| `username` | string | 用户名 |
| `token_id` | int | token ID |
| `token_name` | string | token 名称 |
| `group` | string | 使用分组 |
| `model_name` | string | 原始模型名 |
| `channel_id` | int | 渠道 ID |
| `channel_name` | string | 渠道名 |
| `channel_type` | int | 渠道类型 |
| `priority` | int64 | 触发时 priority |
| `attempt_count` | int | 本渠道失败次数 |
| `disable_duration_seconds` | int | 禁用时长 |
| `disabled_until` | int64 | 禁用到期时间 |
| `status_code` | int | 上游或内部状态码 |
| `error_code` | string | new-api error code |
| `error_type` | string | 错误类型 |
| `reason` | text | 脱敏后的失败原因 |
| `used_channels` | text | JSON 字符串，记录本次请求路径 |
| `metadata` | text | JSON 字符串，预留扩展 |

第一版建议放主库 `DB`，不要放 `LOG_DB`。原因：

- `LOG_DB` 可能通过 `LOG_SQL_DSN` 使用 ClickHouse，相关入口在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/main.go:222`、`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/main.go:389`。
- ClickHouse 需要手写建表、TTL 和查询适配，第一版成本明显增加。
- 调度日志更接近运营排错和渠道状态审计，和渠道表同库便于关联。

## 11. 配置项建议

配置分两层：全局默认配置放入现有 option 体系，每渠道覆盖配置放入渠道表或渠道扩展结构。

全局配置涉及：

- `common/constants.go`
- `model/option.go`
- default 前端渠道管理页
- classic 前端渠道管理页，如需要

全局默认配置：

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `AdvancedChannelSchedulerEnabled` | false | 高级调度器总开关，默认关闭 |
| `SchedulerObservationOnly` | true | 只记录日志，不改变调度行为 |
| `SchedulerChannelFailureThreshold` | 3 | 单渠道失败阈值 |
| `SchedulerAutoDisableSeconds` | 7200 | 临时禁用 2 小时 |
| `SchedulerAllowPriorityFallback` | true | 同级耗尽后是否降级 |
| `SchedulerLogEnabled` | true | 是否写独立日志 |
| `SchedulerRespectAutoBan` | true | 是否尊重渠道 `auto_ban` |
| `SchedulerRetrySameChannel` | true | 是否允许同渠道连续重试 |
| `SchedulerMaxAttemptsPerRequest` | 12 | 单请求最大尝试次数，防止等待时间失控 |
| 流式请求 | 随 `SchedulerEnabled` 生效 | 开启高级调度器后同时覆盖流式和非流式请求 |
| `SchedulerEnableForTaskRelay` | false | 第一版不建议覆盖任务类 relay |

不要复用 `RetryTimes` 表示“每个渠道重试次数”。可以继续保留 `RetryTimes` 给旧调度器，新调度器使用自己的阈值和总尝试上限。

每渠道覆盖配置：

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `scheduler_enabled` | true | 当前渠道是否参与高级调度 |
| `scheduler_retry_times` | null | 当前渠道连续失败阈值，空值使用 `SchedulerChannelFailureThreshold` |
| `scheduler_auto_disable_seconds` | null | 当前渠道禁用时长，空值使用 `SchedulerAutoDisableSeconds` |
| `scheduler_auto_recover_enabled` | true | 当前渠道是否允许到期自动恢复 |
| `scheduler_manual_restore_allowed` | true | 当前渠道是否允许管理员手动恢复 |

WebUI 上要把这些做成独立控件：重试次数用数字输入，禁用时长用数字输入加单位选择或固定秒数输入，自动恢复用开关，手动恢复用按钮。不要把它们藏在一个 JSON 文本框里。

现有选项体系参考位置：

- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/option.go:48`
- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/option.go:305`
- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/model/option.go:521`

## 12. API 设计

建议新增路由组：`/api/channel_scheduler`

| 方法 | 路径 | 权限 | 作用 |
| --- | --- | --- | --- |
| GET | `/api/channel_scheduler/logs` | Admin | 分页查看调度日志 |
| GET | `/api/channel_scheduler/logs/stat` | Admin | 查看失败次数、渠道禁用次数统计 |
| GET | `/api/channel_scheduler/disabled` | Admin | 当前临时禁用渠道列表 |
| GET | `/api/channel_scheduler/config` | Root | 查看全局调度器配置 |
| PUT | `/api/channel_scheduler/config` | Root | 保存全局调度器配置 |
| GET | `/api/channel_scheduler/channel/:id/config` | Admin | 查看某个渠道的调度配置和当前禁用状态 |
| PUT | `/api/channel_scheduler/channel/:id/config` | Root | 保存某个渠道的重试次数、禁用时长、自动恢复设置 |
| POST | `/api/channel_scheduler/restore/:id` | Root | 手动恢复某个临时禁用渠道 |
| DELETE | `/api/channel_scheduler/logs` | Root | 清理历史调度日志 |

权限建议：

- 查看日志用 Admin 起步。
- 修改全局配置、渠道级配置、手动恢复、清理日志用 Root。
- 普通用户不建议直接查看调度日志，因为日志会暴露渠道名、上游错误、分组、模型和运营策略。

## 13. 调度算法伪流程

```text
if AdvancedChannelSchedulerEnabled == false:
  使用旧逻辑

session = NewChannelSchedulerSession(group, model, requestPath)
session.LoadCandidates()

while session.HasNext() and totalAttempts < SchedulerMaxAttemptsPerRequest:
  channel = session.SelectNextChannel()
  channelRetryTimes = ResolveChannelRetryTimes(channel)
  channelDisableSeconds = ResolveChannelDisableSeconds(channel)
  SetupContextForSelectedChannel(channel)
  err = relayOnce(channel)

  if err == nil:
    session.RecordSuccess(channel)
    return success

  if !shouldRetry(err):
    session.RecordTerminalFailure(channel, err)
    return err

  session.RecordFailure(channel, err)

  if session.ChannelFailureCount(channel) >= channelRetryTimes:
    if SchedulerRespectAutoBan && channel.AutoBan:
      disabledUntil = now + channelDisableSeconds
      TemporarilyDisableChannel(channel, disabledUntil)
      RecordChannelSchedulerLog(channel, err, disabledUntil)
    session.RemoveChannelFromCurrentBucket(channel)

  if current priority bucket still has channel:
    continue same priority

  if SchedulerAllowPriorityFallback:
    move to next lower priority
  else:
    break

return lastErr
```

关键实现点：

- 同一渠道是否连续尝试由 `SchedulerRetrySameChannel` 和渠道级 `scheduler_retry_times` 决定；渠道未设置时使用全局 `SchedulerChannelFailureThreshold`。
- 禁用时长先使用渠道级 `scheduler_auto_disable_seconds`，渠道未设置时使用全局 `SchedulerAutoDisableSeconds`。
- 如果 `SchedulerRetrySameChannel=false`，可以变成 A 失败一次后 B，B 失败一次后 A，再按累计阈值禁用。这更像负载均衡，但不是你当前描述的需求。
- 每个请求必须有总尝试上限，否则高优先级有很多渠道时等待时间会失控。
- 流式请求随高级调度器总开关启用；但已向客户端写出不可回滚内容后，仍不得换渠道重试。

## 14. 数据一致性与并发注意事项

1. 并发请求同时把同一渠道禁用时，重复禁用必须是幂等的。
2. 使用内存缓存时，`model.UpdateChannelStatus` 会更新缓存和 ability；新增 `auto_disabled_until` 后也要同步缓存字段。
3. 使用 Redis 或多节点时，单次请求内计数只能保证本请求内准确，不等于全局失败计数。
4. 如果未来要做全局连续失败计数，应使用 Redis 原子计数并设置 TTL，但第一版不建议加。
5. 自动恢复时只能恢复调度器临时禁用的渠道，不能恢复手动禁用或老版本无到期时间的 auto disabled。
6. 多 key 渠道第一版按 channel 禁用最稳；key 级调度另开阶段。
7. 请求路径过滤不能丢，Advanced Custom 渠道依赖 `requestPath` 过滤。
8. channel affinity 失败后跳过 retry 的规则不能绕过，现有入口在 `service.ShouldSkipRetryAfterChannelAffinityFailure(c)`。
9. 不可重试错误不能被调度器强行重试，`shouldRetry` 仍然是重试资格判断。
10. 已经向客户端输出内容的流式响应，不能再换渠道重试，否则用户可能收到不完整或重复内容。
11. 每渠道覆盖配置要有明确回退规则：渠道值为空时用全局默认，不要把 0 同时当作“关闭”和“未设置”。

## 15. 两套前端怎么做

new-api 当前有两套前端：

- default：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default`
- classic：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/classic`

后端 API 只需要做一套，但页面要不要两套都做，取决于实际使用哪套 WebUI。

### 15.1 default 前端

建议优先做 default。用户要求调度器操作面板放在 WebUI 左侧“渠道”入口里，所以 default 不再把调度器配置入口放到系统设置页。

- 当前前端规则集中在 `web/default/AGENTS.md`。
- default 使用 TypeScript，适合新增类型化 API 和日志表。
- 日志页已有 section registry，可以扩展新 section。

渠道管理入口：

- 左侧导航当前已有“Channels”，入口在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default/src/hooks/use-sidebar-data.ts`。
- 渠道页入口在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default/src/routes/_authenticated/channels/index.tsx` 和 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default/src/features/channels/index.tsx`。
- 当前渠道页标题右侧已经有 retry badge，并链接到系统设置的 routing reliability。新方案应把这个入口改成渠道页内的“调度器设置”入口。

渠道页建议做三块：

1. 渠道列表：保留现有 `ChannelsTable`、筛选、批量操作、行操作。
2. 调度器设置：放在渠道页顶部按钮或页内 tab 中，复用 `SectionPageLayout.Actions`、`Button`、`Badge`、`Tooltip`、`Dialog` 或 `Drawer`。
3. 临时禁用渠道：可以作为渠道页内 tab，也可以作为渠道页右上角按钮打开的面板，展示当前被调度器禁用的渠道。

渠道级操作按钮：

- “调度设置”：每个渠道行操作里新增按钮，打开渠道级调度设置弹窗或抽屉。
- “手动恢复”：仅当渠道处于调度器临时禁用状态且允许手动恢复时显示。
- “启用自动恢复 / 关闭自动恢复”：渠道级开关，保存到 `scheduler_auto_recover_enabled`。
- “使用全局默认”：把该渠道的重试次数、禁用时长恢复为空值。
- “查看调度日志”：跳转到 `/usage-logs/scheduler`，并带上 `channel_id` 筛选。

渠道级设置项：

- 自动重试次数：`scheduler_retry_times`，数字输入。
- 连续失败后禁用多久：`scheduler_auto_disable_seconds`，数字输入，建议展示成分钟/小时。
- 是否参与高级调度：`scheduler_enabled`，开关。
- 是否自动恢复：`scheduler_auto_recover_enabled`，开关。
- 是否允许手动恢复：`scheduler_manual_restore_allowed`，开关。

使用日志入口：

- 用户要求 log 面板放在 WebUI 左侧“使用日志”下面，并做成独立页面。
- default 现有使用日志 section 在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default/src/features/usage-logs/section-registry.tsx`。
- 新增 `scheduler` section，路由复用 `/usage-logs/$section`，目标地址为 `/usage-logs/scheduler`。
- 左侧导航在 `use-sidebar-data.ts` 的 General 分组中，把“调度日志”放在 “Usage Logs” 后面，URL 指向 `/usage-logs/scheduler`，并保证权限为 Admin。

调度日志页面建议改动：

- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default/src/features/usage-logs/section-registry.tsx`
- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default/src/features/usage-logs/index.tsx`
- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default/src/features/usage-logs/api.ts`
- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/default/src/features/usage-logs/types.ts`
- 新增 scheduler log table、filter bar、columns，优先复用现有 `UsageLogsTable`、`common-logs-filter-bar`、`common-logs-header-actions` 的布局和交互。
- 新增字段展示：渠道名、priority、attempt_count、禁用时长、disabled_until、reason、used_channels。

视觉约束：

- default 复用现有 `SectionPageLayout`、`Tabs`、`Button`、`Badge`、`Tooltip`、`Dialog`、`Drawer`、表格和筛选栏。
- 图标优先用现有 lucide-react 图标，比如 `Radio`、`FileText`、`Settings2`、`RotateCcw`、`Clock`。
- 不新增独立颜色系统，不新增与现有按钮风格冲突的大面积自定义样式。
- 所有新增文案必须走 i18n。

default 验收命令：

```bash
cd web/default
bun run typecheck
bun run build
```

### 15.2 classic 前端

classic 建议作为第二阶段。如果当前部署主题是 classic，需要补页面；如果当前部署主题是 default，classic 可以先只保证构建不坏。

建议改动：

- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/classic/src/App.jsx`
- `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/classic/src/components/layout/SiderBar.jsx`
- 渠道管理页在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/classic/src/pages/Channel/index.jsx` 和 `components/table/channels`。
- 调度器设置放到渠道管理页里，复用 `CardPro`、Semi UI `Button`、`Modal`、`Form`、`Table`。
- 使用日志页在 `C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/web/classic/src/pages/Log/index.jsx` 和 `components/table/usage-logs`。
- 新增 `pages/SchedulerLog` 或 `components/table/scheduler-logs`，并在左侧 `SiderBar.jsx` 的“使用日志”下面新增“调度日志”菜单。
- 新增独立 hook，不要硬塞进 `useUsageLogsData.jsx`，避免字段和筛选逻辑互相污染。

classic 验收命令：

```bash
cd web/classic
bun run build
```

### 15.3 如何判断实际使用哪套

源码显示：

- `main.go` embed 两套产物：`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/main.go:38`、`C:/Users/liuyun/Desktop/newapi-v1.0.0-rc.15/main.go:44`。
- 主题由 `common.GetTheme()` 决定，default/classic 两套文件系统在 `router/web-router.go` 切换。
- Dockerfile 会构建 default 和 classic 两套 dist。

因此：后端 API 必须统一；前端可以先做 default，但最终构建前两套都要验证。

## 16. 推荐实施阶段表

| 阶段 | 目标 | 是否可单独上线 | 风险 |
| --- | --- | --- | --- |
| 1 | 新增独立调度日志表、查询 API、观察日志 | 是 | 低 |
| 2 | 新增候选渠道列表和调度器单测，默认关闭 | 是 | 中 |
| 3 | 接入 relay 主请求链路，通过开关启用 | 是 | 中高 |
| 4 | 增加临时禁用字段和 2 小时自动恢复 | 是 | 中高 |
| 5 | default 渠道管理页增加调度器设置、渠道级按钮、临时禁用渠道面板 | 是 | 中 |
| 6 | default 使用日志下新增独立调度日志页面 | 是 | 中 |
| 7 | classic 前端补齐或确认不需要 | 是 | 中 |
| 8 | 本地 Docker、灰度、生产备份、上线观察 | 必须 | 高 |

每个阶段都应该可以独立合并。最重要的是第 1 阶段：只新增日志和查询，不改变生产调度行为。等观察清楚真实错误分布、日志量和敏感信息脱敏情况后，再进入真正的调度策略。

## 17. 第一阶段详细任务

第一阶段建议做“观察版”：

1. 新增 `channel_scheduler_logs` 模型和迁移。
2. 新增 `model.RecordChannelSchedulerLog`。
3. 新增 Admin 查询 API。
4. 在现有 `processChannelError` 附近写观察日志。
5. default 前端在“使用日志”下面增加只读调度日志页，路径建议 `/usage-logs/scheduler`。
6. 不改变任何渠道选择、禁用、重试行为。

这样即使出现问题，也只影响新增日志，不影响生产请求成功率。

## 18. 测试计划

后端单测：

- A/B priority=3，C priority=2，D priority=1。
- A 失败 2 次不禁用。
- A 第 3 次失败后临时禁用 7200 秒。
- A 禁用后优先选择同级 B。
- B 禁用后才降级到 C。
- `auto_ban=false` 不自动禁用，但应记录日志。
- 不可重试错误不继续尝试。
- `SchedulerMaxAttemptsPerRequest` 生效。
- `MEMORY_CACHE_ENABLED=true/false` 两条路径都通过。
- auto 分组开启跨分组重试时，不破坏 group 内 priority 消耗逻辑。
- 手动禁用不会被自动恢复。
- 渠道级 `scheduler_retry_times` 覆盖全局失败阈值。
- 渠道级 `scheduler_auto_disable_seconds` 覆盖全局禁用时长。
- `scheduler_auto_recover_enabled=false` 时，到期后不自动恢复，只能手动恢复。
- `scheduler_manual_restore_allowed=false` 时，WebUI 不显示或禁用手动恢复按钮。

后端建议命令：

```bash
go test ./service ./model ./controller ./setting/operation_setting
go test ./...
```

前端验证：

```bash
cd web/default
bun run typecheck
bun run build

cd ../classic
bun run build
```

手工验收：

1. 准备 A/B/C/D 四个测试渠道。
2. 设置 A、B 上游必定失败，C 成功。
3. 发起请求后确认顺序为 A 三次、B 三次、C 成功。
4. A、B 状态变为 auto disabled。
5. A、B `auto_disabled_until` 约等于当前时间加 7200 秒。
6. `channel_scheduler_logs` 有 A、B 两条禁用日志。
7. 2 小时后或手动调整时间后，自动恢复只恢复 A/B，不影响手动禁用渠道。
8. 在渠道管理页修改 A 的自动重试次数和禁用时长后，下一次调度按 A 的渠道级配置执行。
9. 在使用日志下进入调度日志独立页面，可以按 `channel_id`、`request_id`、priority、时间范围筛选。

## 19. 生产上线建议

你的服务器如果已经承载真实请求，不要直接在生产上试错。

推荐流程：

1. 新建开发分支，例如 `feature/scheduler-failover`。
2. 本地改代码并跑测试。
3. 本地用 Docker Compose 跑起来。
4. 本地构造 A/B/C/D 测试渠道，验证调度顺序。
5. 本地确认日志表写入正常。
6. 备份服务器数据库。
7. 在服务器上开测试 compose 项目，使用不同端口和测试数据库。
8. 部署后保持 `AdvancedChannelSchedulerEnabled=false`。
9. 打开 `SchedulerObservationOnly=true` 观察至少一天。
10. 确认错误原因脱敏、日志量可控、查询性能可接受。
11. 先在测试分组或少量模型上启用高级调度。
12. 再逐步扩大到生产分组。
13. 保留一键回滚方式：关闭 `AdvancedChannelSchedulerEnabled` 即回到旧调度。
14. 上线初期不要启用流式高级重试和任务 relay 高级重试。

生产前必须备份数据库，尤其是这次会新增表和字段。

## 20. 最需要注意的坑

1. 不要把 `RetryTimes=3` 当成“每个渠道重试 3 次”。这会误改现有优先级降级语义。
2. 不要只改 `controller/relay.go`，首次渠道选择在 `middleware.Distribute`。
3. 不要只改 `model/channel_cache.go`，内存缓存关闭时还有 `model/ability.go`。
4. 不要直接把独立日志放进 `LOG_DB`，ClickHouse 会显著增加第一版成本。
5. 不要对所有错误重试，必须尊重 `SkipRetry`、状态码规则和 channel affinity。
6. 不要自动恢复手动禁用渠道。
7. 不要在已开始向客户端输出的流式请求中换渠道重试。
8. 不要在第一版做 key 级别失败计数，先按 channel 级别落地。
9. 不要忘记同步 `ability.enabled`，否则渠道状态和可选能力会不一致。
10. 不要忘记前端 i18n 和两套构建验证。
11. 不要让单请求尝试次数无限增长，必须设置 `SchedulerMaxAttemptsPerRequest`。
12. 不要把上游完整错误原样暴露给普通用户，调度日志要脱敏，并且查看权限从 Admin 起步。
13. 不要把调度器入口放到系统设置深处。用户明确要求放在“渠道”入口里，方便日常操作。
14. 不要为调度器面板重新设计一套 UI。按钮、表格、弹窗、抽屉、颜色和间距优先复用现有渠道页和使用日志页。
15. 不要只做全局配置。每个渠道必须能独立设置自动重试次数、连续失败后的禁用时长、是否自动恢复和是否允许手动恢复。

## 21. 一句话路线

先做“独立调度日志和观察开关”，再做“候选渠道分桶和调度器”，把调度器设置放到渠道管理页，把调度日志放到使用日志下面；最后接入 `relay` 并默认关闭，稳定后再开启“同级重试、达到阈值临时禁用、同级耗尽后降级”。
