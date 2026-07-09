# NewAPI 二开管线布置

更新时间：2026-07-09
当前分支：`feature/scheduler-failover`
基线分支：`custom-v1.0.0-rc.15`
已完成 checkpoint 提交：`b347b9bb chore: checkpoint scheduler failover work`

## 1. 本次问题结论

本次 502 的根因不是上游 HTTP 状态码失败，而是协议形态不一致：

```text
API压力测试项目发起 OpenAI stream:true 请求
  -> NewAPI 进入 OpenAI 流式处理 OaiStreamHandler
  -> 模拟API响应 success 模式返回 200 application/json 普通 JSON
  -> NewAPI 原流式扫描器只消费 SSE data: 行，普通 JSON 被当成无有效流内容
  -> 调度器开启 DetectEmptyResponseForScheduler 后判定 empty_response
  -> controller/relay.go 把 empty_response 视为可重试错误
  -> ChannelScheduler 记录失败并在达到阈值后临时禁用渠道
```

所以图片中的“上游都是 200，但 NewAPI 返回 502、自动重试、禁用三个渠道”能够被同一个根因解释。

已修复：`relay/channel/openai/relay-openai.go` 在流式请求收到明确 JSON `Content-Type` 的 2xx 响应时，把普通 OpenAI chat completion JSON 包装成 SSE chunk 返回给下游。这样压测项目仍按流式读取，NewAPI 不再把这类成功 JSON 误判为空流。

同时加了保护：只有 `application/json`、`text/json`、`+json` 这类明确 JSON 响应才触发包装。缺失 `Content-Type` 的真实 SSE 仍走原流式扫描，避免 `ReadAll` 吞掉真实流。

## 2. 三条链路复现矩阵

| 链路 | 结果 | 关键差异 |
| --- | --- | --- |
| 模拟API响应 -> NewAPI -> API压力测试 | 修复前 502，调度器重试并禁用渠道；修复后应返回流式成功 | 下游请求 `stream:true`，上游 success 返回普通 JSON |
| 模拟API响应 -> 模拟API响应 | 成功，200 | 压测项目只要响应 2xx 就读 body，不强校验 SSE 语义 |
| 模拟API响应 -> NewAPI -> 酒馆 | 成功 | 酒馆侧对响应协议/读取方式与压测项目不同，没有触发 NewAPI 空流重试路径 |

本次 bug 只在第一条链路出现，是因为它同时满足三个条件：

1. 下游请求是 OpenAI 协议并带 `stream:true`。
2. NewAPI 根据下游请求进入 OpenAI 流式 handler。
3. 上游 mock success 模式返回 `application/json`，不是 `text/event-stream`。

## 3. 外部项目当前触发条件

### 3.1 API压力测试项目：下游

配置文件：`C:/Users/liuyun/Desktop/API压力测试项目/data/config.json`

关键配置：

- NewAPI base URL：`http://localhost:3000`
- 协议：`openai`
- 模型：`gemini-3.1-pro-preview`
- `stream: true`
- API Key 已在配置中存在，文档中不记录明文。

请求构造：

- `C:/Users/liuyun/Desktop/API压力测试项目/server/protocols.js`
- OpenAI 默认路径：`/v1/chat/completions`
- OpenAI body 中写入 `stream: requestStream`
- `C:/Users/liuyun/Desktop/API压力测试项目/server/requester.js` 在 `request.stream` 为 true 时直接读取响应 body stream。

压测项目的流式读取并不解析 SSE 内容，只统计 chunk、首包和耗时；因此直连 mock 普通 JSON 时，只要 HTTP 200 就会被认为成功。

### 3.2 模拟API响应：上游

配置文件：`C:/Users/liuyun/Desktop/模拟API响应/data/config.json`

当前 5001、5002、5003 都是：

```json
{ "mode": "success" }
```

服务端关键行为：

- `C:/Users/liuyun/Desktop/模拟API响应/src/server/index.ts`
- `empty` 模式会检查请求体 `stream:true`，并返回 `text/event-stream`。
- `success` 模式不检查 `stream:true`，直接 `reply.code(200).send(successPayload(...))`，Fastify 会返回普通 JSON。

这就是“上游 200 但 NewAPI 流式 handler 看不到 SSE data 行”的直接原因。

## 4. NewAPI 主请求管线

简化链路：

```text
middleware.Distribute
  -> 初始渠道选择与上下文写入
controller.Relay
  -> relaycommon.GenRelayInfo
  -> 旧重试循环或 ChannelScheduler 接管
  -> relay/channel/{openai,claude,gemini,...}
  -> provider handler 读取上游响应
  -> controller.processChannelError / processChannelErrorForScheduler
  -> shouldRetry
  -> 成功返回或进入下一次调度
```

调度器开启后的关键点：

- `controller/relay.go:193` 判断是否启用高级调度器。
- `controller/relay.go:294` 进入 `relayWithChannelScheduler`。
- `controller/relay.go:334` 对调度器请求设置 `DetectEmptyResponseForScheduler=true`。
- `controller/relay.go:439` 对 `ErrorCodeEmptyResponse` 强制允许 retry。
- `service/channel_scheduler.go:259` 的 `RecordFailure` 负责失败计数、日志、临时禁用。
- `service/empty_response.go:101` 生成 502 `empty_response`。

本次问题发生在 `relay/channel/openai/relay-openai.go` 的 OpenAI 流式读取阶段，随后沿 `empty_response -> retry -> RecordFailure -> auto disable` 扩散。

## 5. 本次 502 修复点

修改文件：

- `relay/channel/openai/relay-openai.go`
- `relay/channel/openai/relay_openai_stream_test.go`

核心实现：

1. `OaiStreamHandler` 开头读取上游 `Content-Type`。
2. 仅当上游是 2xx 且 `Content-Type` 明确为 JSON 时，进入 `OaiJSONAsStreamHandler`。
3. `OaiJSONAsStreamHandler` 使用 `common.Unmarshal` 解析 `dto.OpenAITextResponse`。
4. 若 JSON 内是 OpenAI error，按原错误返回。
5. 若调度器要求空响应检测且无 content、reasoning、tool_calls，继续返回 `empty_response`。
6. 若是有效普通 JSON，则合成 `chat.completion.chunk`，写给下游。
7. 最后发送 stop chunk、usage final chunk 和 `[DONE]`。

保护测试：

- `TestOaiStreamHandlerWrapsJSONChatResponseWhenStreamRequested`：验证上游普通 JSON 能被包装为 SSE。
- `TestOaiStreamHandlerKeepsSSEWhenContentTypeMissing`：验证缺失 `Content-Type` 的真实 SSE 不会误走 JSON fallback。

## 6. 后端二开管线布置

### 6.1 全局调度器配置

文件：

- `setting/operation_setting/channel_scheduler_setting.go`
- `controller/channel_scheduler.go`
- `router/api-router.go`

职责：

- 配置高级调度器总开关。
- 配置观察模式、失败阈值、临时禁用秒数、日志开关、是否尊重 `auto_ban`、单请求最大尝试次数、retry jitter。
- 对外提供 `/api/channel_scheduler/config`。

默认语义：

- `enabled=false`：默认不改变生产调度。
- `observation_only=true`：观察模式只记日志，不执行真实临时禁用。
- `channel_failure_threshold=3`：单渠道连续失败 3 次后触发临时禁用。
- `auto_disable_seconds=7200`：默认禁用 2 小时。
- `max_attempts_per_request=12`：防止一次请求无限尝试。

### 6.2 渠道表字段与状态

文件：

- `model/channel.go`
- `model/main.go`
- `model/channel_scheduler.go`

新增字段：

- `auto_disabled_until`
- `scheduler_enabled`
- `scheduler_retry_times`
- `scheduler_auto_disable_seconds`
- `scheduler_auto_recover_enabled`
- `scheduler_manual_restore_allowed`

约定：

- `auto_disabled_until=0` 表示不是调度器临时禁用。
- 渠道级配置使用指针或可空语义：`nil/null` 表示继承全局默认。
- 手动禁用渠道不能被调度器自动恢复。
- 旧式无到期时间的 auto disabled 渠道不会被自动恢复。

### 6.3 候选渠道分桶

文件：

- `model/channel_cache.go`
- `model/ability.go`
- `service/channel_scheduler.go`

职责：

- 内存缓存开启时，从缓存候选集中筛选。
- 内存缓存关闭时，从 ability 表直查。
- 保留 request path 过滤，避免 Advanced Custom 渠道被错误纳入。
- 按 priority 从高到低分桶。
- 同优先级内继续复用现有权重/随机选择习惯。

目标行为：

```text
priority=3: A 失败到阈值 -> 临时禁用 A -> 继续同级 B
priority=3: B 失败到阈值 -> 临时禁用 B -> 同级耗尽
priority=2: 尝试 C
priority=1: 尝试 D
```

### 6.4 单请求调度会话

文件：

- `service/channel_scheduler.go`
- `service/channel_scheduler_test.go`

核心对象：`ChannelSchedulerSession`

职责：

- 保存单次请求内的候选渠道、当前 priority 桶、尝试次数、失败次数、已使用渠道。
- `NextChannel()` 选择下一渠道。
- `RecordFailure()` 记录失败、判断阈值、写调度日志、触发临时禁用。
- `WaitBeforeRetry()` 应用 retry jitter。
- `RemainingAttempts()` 限制单请求总尝试次数。

重要边界：

- 第一版是“单次请求内计数”，不是 Redis 全局连续失败计数。
- 已向客户端输出内容的流式请求不能换渠道重试。
- `shouldRetry` 仍然是重试资格判断入口。
- `SkipRetry`、channel affinity、不可重试状态码不能被调度器绕过。

### 6.5 relay 接入点

文件：

- `controller/relay.go`
- `controller/relay_retry_test.go`
- `relay/common/relay_info.go`
- `service/empty_response.go`
- `service/empty_response_test.go`

职责：

- `RelayInfo` 增加 `DetectEmptyResponseForScheduler`。
- 调度器接管时，空业务内容可以在未写出响应前转换成 502 `empty_response`。
- `empty_response` 在调度器语义下是可重试错误，用于触发同级 failover。
- 非调度器路径保持原有“上游空响应也可能原样成功返回”的兼容行为。

当前各 provider 的空响应检测入口：

- OpenAI chat：`relay/channel/openai/relay-openai.go`
- OpenAI responses：`relay/channel/openai/relay_responses.go`
- OpenAI chat via responses：`relay/channel/openai/chat_via_responses.go`
- Claude：`relay/channel/claude/relay-claude.go`
- Gemini：`relay/channel/gemini/relay-gemini.go`
- Gemini native：`relay/channel/gemini/relay-gemini-native.go`

### 6.6 调度日志

文件：

- `model/channel_scheduler_log.go`
- `controller/channel_scheduler_log.go`
- `router/api-router.go`

表：`channel_scheduler_logs`

事件类型：

- `failure`
- `observe_disable`
- `auto_disable`
- `auto_recover`
- `manual_restore`

API：

- `GET /api/channel_scheduler/logs`
- `GET /api/channel_scheduler/logs/stat`
- `DELETE /api/channel_scheduler/logs`

兼容别名：

- `/api/channel-scheduler/*`

日志放主库 DB，不放 `LOG_DB`，避免 ClickHouse 建表和查询差异。

### 6.7 临时禁用与恢复

文件：

- `model/channel_scheduler.go`
- `service/channel_scheduler.go`
- `controller/system_task_handlers.go`
- `model/system_task.go`
- `service/system_task.go`

禁用流程：

```text
RecordFailure 达到阈值
  -> TempDisableChannelForScheduler
  -> model.SchedulerTempDisableChannel
  -> status=auto_disabled
  -> auto_disabled_until=now+seconds
  -> 同步 ability/cache
  -> 写 channel_scheduler_logs
```

恢复流程：

```text
system task scheduler
  -> schedulerRecoverHandler.Enabled
  -> model.HasSchedulerTempDisabledChannels
  -> service.RecoverExpiredSchedulerChannels
  -> model.SchedulerRecoverChannel(requireExpired=true)
  -> status=enabled
  -> auto_disabled_until=0
  -> 写 auto_recover 日志
```

手动恢复：

- `POST /api/channel_scheduler/restore/:id`
- 只恢复调度器临时禁用渠道。
- 未到期且不允许手动恢复时拒绝。
- 不恢复手动禁用渠道。

### 6.8 API 缓存保护

文件：

- `middleware/cache.go`
- `middleware/cache_test.go`
- `web/default/src/lib/api.ts`

背景：

早前调度器页面 404 问题中，旧 API 404 被静态缓存头缓存，浏览器继续命中旧 404。二开中已加保护：

- 后端对 `/api`、`/v1`、`/mj`、`/pg` 等 API 路径返回 `no-store`。
- 前端 GET `/api*` 自动追加稳定 `_newapi_cache` 参数，绕开旧浏览器缓存。

排查 404 时应先看响应头：

- 正常 API 响应不应有长期 `max-age=604800`。
- 若看到磁盘缓存命中旧 404，先清缓存或确认 `_newapi_cache` 是否存在。

### 6.9 开发运行管线

文件：

- `Dockerfile.dev`
- `docker-compose.dev.yml`
- `一键启动.bat`

职责：

- 用开发镜像运行 NewAPI。
- 通过源码戳判断是否需要 rebuild。
- 避免旧前端请求新接口、旧后端未注册路由这类错配。

已有经验：

- API 路由是否存在，用未登录 401 判断比看前端弹窗更准确。
- 401 表示进入鉴权链路，404 才表示路由缺失或请求路径错误。

## 7. 前端 default 二开管线布置

当前调度器 UI 只在 `web/default` 完整落地，`web/classic` 本阶段主要保证构建不坏。

### 7.1 调度器 API 封装

文件：

- `web/default/src/features/usage-logs/scheduler/api.ts`
- `web/default/src/features/usage-logs/scheduler/types.ts`

封装接口：

- 查询日志：`getSchedulerLogs`
- 查询统计：`getSchedulerLogStat`
- 查询临时禁用渠道：`getSchedulerDisabledChannels`
- 查询全局配置：`getSchedulerGlobalConfig`
- 更新全局配置：`updateSchedulerGlobalConfig`
- 查询渠道级配置：`getSchedulerChannelConfig`
- 更新渠道级配置：`updateSchedulerChannelConfig`
- 手动恢复：`restoreSchedulerChannel`

React Query key 统一在 `schedulerQueryKeys`，涉及恢复、保存配置后要 invalidate `channel-scheduler`。

### 7.2 渠道页入口

文件：

- `web/default/src/features/channels/index.tsx`
- `web/default/src/features/channels/components/channels-provider.tsx`
- `web/default/src/features/channels/components/channels-dialogs.tsx`
- `web/default/src/features/channels/components/data-table-row-actions.tsx`
- `web/default/src/features/channels/types.ts`

入口：

- 渠道页顶部打开 `SchedulerSettingsDialog`。
- 渠道行操作打开 `ChannelSchedulerConfigDialog`。
- 行操作中提供“查看调度日志”和“手动恢复”。

渠道级配置：

- 是否参与高级调度。
- 连续失败阈值。
- 临时禁用秒数。
- 是否自动恢复。
- 是否允许手动恢复。
- “使用全局默认”会把字段重置为 `null`。

### 7.3 调度器设置与临时禁用面板

文件：

- `web/default/src/features/channels/components/dialogs/scheduler-settings-dialog.tsx`

包含：

- 临时禁用渠道列表。
- 到期时间展示。
- 自动恢复状态展示。
- 手动恢复按钮。
- 全局调度器配置表单。
- 跳转调度日志按钮。

注意：

- 手动恢复按钮只在允许恢复时显示或可用。
- 保存全局配置需要 Root 权限。
- 查看临时禁用列表需要 Admin 权限。

### 7.4 单渠道调度配置弹窗

文件：

- `web/default/src/features/channels/components/dialogs/channel-scheduler-config-dialog.tsx`

包含：

- 显示当前渠道是否被调度器临时禁用。
- 显示 effective 配置，也显示渠道覆盖值。
- 保存渠道级覆盖配置。
- 重置为全局默认。
- Root 才能修改配置。

### 7.5 使用日志下的调度日志页

文件：

- `web/default/src/features/usage-logs/index.tsx`
- `web/default/src/features/usage-logs/section-registry.tsx`
- `web/default/src/features/usage-logs/scheduler/scheduler-logs-table.tsx`
- `web/default/src/features/usage-logs/scheduler/scheduler-logs-filter-bar.tsx`
- `web/default/src/features/usage-logs/scheduler/scheduler-logs-columns.tsx`
- `web/default/src/routes/_authenticated/usage-logs/$section.tsx`
- `web/default/src/hooks/use-sidebar-data.ts`

路由：

- `/usage-logs/scheduler`

导航：

- 左侧“使用日志”下面新增调度日志入口。

筛选维度：

- 时间范围。
- 渠道 ID。
- 模型名。
- 分组。
- event type。
- request id。

### 7.6 i18n

文件：

- `web/default/src/i18n/locales/en.json`
- `web/default/src/i18n/locales/zh.json`
- `web/default/src/i18n/locales/fr.json`
- `web/default/src/i18n/locales/ja.json`
- `web/default/src/i18n/locales/ru.json`
- `web/default/src/i18n/locales/vi.json`

约定：

- 所有新增可见文案都走 `t('English source key')`。
- 翻译文件保持 flat JSON。

## 8. 后续排查 502 的固定顺序

遇到 “NewAPI 502 + 调度器重试/禁用” 时，按这个顺序查：

1. 看 NewAPI 返回体中的 `error.code`。
   - 如果是 `empty_response`，优先查上游是否返回了空业务内容或协议不匹配。
   - 如果是 `bad_response_body`，查上游 body 是否不是当前 handler 期望的 JSON/SSE。
2. 看请求是否是流式。
   - OpenAI：body 是否有 `stream:true`。
   - Gemini：URL 是否是 `streamGenerateContent?alt=sse`。
   - Claude：body 是否有 `stream:true`。
3. 看上游实际响应头。
   - 流式应是 `text/event-stream`。
   - 普通 JSON 应是 `application/json`。
4. 看 NewAPI 选择了哪个 provider handler。
   - OpenAI 路径重点看 `relay/channel/openai/relay-openai.go`。
5. 看是否已经向下游写出内容。
   - 已写出后不能安全 retry。
6. 看调度器日志。
   - `/api/channel_scheduler/logs`
   - 是否记录 `failure`、`observe_disable`、`auto_disable`。
7. 看渠道状态。
   - `status`
   - `auto_disabled_until`
   - `scheduler_auto_recover_enabled`
8. 看前端/浏览器缓存。
   - 调度器 API 返回 404 时确认是否旧缓存。
   - 未登录返回 401 是正常路由存在。

## 9. 后续排查前端调度器页面的固定顺序

1. 直接请求后端 API，不先看前端弹窗。
   - `/api/channel_scheduler/disabled`
   - `/api/channel_scheduler/logs`
   - `/api/channel_scheduler/config`
2. 未登录返回 401：路由存在。
3. 返回 404：查 `router/api-router.go` 是否注册，或前端是否请求了 hyphen/underscore 不一致路径。
4. 返回缓存旧 404：查响应头是否 `no-store`，查 URL 是否有 `_newapi_cache`。
5. 前端页面空白：查 `section-registry.tsx` 是否注册 `scheduler`。
6. 侧边栏没有入口：查 `use-sidebar-data.ts`。
7. 行操作没有按钮：查 `data-table-row-actions.tsx` 中权限、状态和到期时间判断。

## 10. 建议验证命令

后端局部：

```powershell
$env:GOCACHE=(Resolve-Path '.tools\gocache').Path
$env:GOPATH=(Resolve-Path '.tools\gopath').Path
& '.\.tools\go\bin\go.exe' test ./relay/channel/openai -count=1
```

后端调度器相关包：

```powershell
$env:GOCACHE=(Resolve-Path '.tools\gocache').Path
$env:GOPATH=(Resolve-Path '.tools\gopath').Path
& '.\.tools\go\bin\go.exe' test ./router ./middleware ./service ./model ./controller ./setting/operation_setting -count=1
```

全仓后端构建：

```powershell
$env:GOCACHE=(Resolve-Path '.tools\gocache').Path
$env:GOPATH=(Resolve-Path '.tools\gopath').Path
& '.\.tools\go\bin\go.exe' build ./...
```

前端 default：

```powershell
cd web/default
bun run typecheck
bun run build
```

前端 classic：

```powershell
cd web/classic
bun run build
```

差异检查：

```powershell
git diff --check
git status --short --branch -uall
```

## 11. 本分支主要变更清单

后端新增或改造：

- `controller/channel_scheduler.go`
- `controller/channel_scheduler_log.go`
- `controller/relay.go`
- `controller/system_task_handlers.go`
- `model/channel.go`
- `model/channel_cache.go`
- `model/ability.go`
- `model/channel_scheduler.go`
- `model/channel_scheduler_log.go`
- `model/main.go`
- `model/system_task.go`
- `service/channel_scheduler.go`
- `service/empty_response.go`
- `setting/operation_setting/channel_scheduler_setting.go`
- `router/api-router.go`
- `middleware/cache.go`
- `relay/common/relay_info.go`
- `relay/channel/openai/relay-openai.go`
- `relay/channel/openai/relay_responses.go`
- `relay/channel/openai/chat_via_responses.go`
- `relay/channel/claude/relay-claude.go`
- `relay/channel/gemini/relay-gemini.go`
- `relay/channel/gemini/relay-gemini-native.go`

后端测试：

- `controller/relay_retry_test.go`
- `controller/channel_test_internal_test.go`
- `router/api_router_test.go`
- `middleware/cache_test.go`
- `service/channel_scheduler_test.go`
- `service/empty_response_test.go`
- `service/channel_affinity_usage_cache_test.go`
- `service/task_billing_test.go`
- `relay/channel/openai/relay_openai_stream_test.go`

前端 default：

- `web/default/src/features/channels/components/channels-dialogs.tsx`
- `web/default/src/features/channels/components/channels-provider.tsx`
- `web/default/src/features/channels/components/data-table-row-actions.tsx`
- `web/default/src/features/channels/components/dialogs/channel-scheduler-config-dialog.tsx`
- `web/default/src/features/channels/components/dialogs/scheduler-settings-dialog.tsx`
- `web/default/src/features/channels/components/numeric-spinner-input.tsx`
- `web/default/src/features/channels/index.tsx`
- `web/default/src/features/channels/types.ts`
- `web/default/src/features/usage-logs/index.tsx`
- `web/default/src/features/usage-logs/section-registry.tsx`
- `web/default/src/features/usage-logs/scheduler/api.ts`
- `web/default/src/features/usage-logs/scheduler/types.ts`
- `web/default/src/features/usage-logs/scheduler/scheduler-logs-table.tsx`
- `web/default/src/features/usage-logs/scheduler/scheduler-logs-filter-bar.tsx`
- `web/default/src/features/usage-logs/scheduler/scheduler-logs-columns.tsx`
- `web/default/src/hooks/use-sidebar-data.ts`
- `web/default/src/lib/api.ts`
- `web/default/src/routes/_authenticated/usage-logs/$section.tsx`
- `web/default/src/i18n/locales/{en,zh,fr,ja,ru,vi}.json`

开发运行：

- `Dockerfile.dev`
- `docker-compose.dev.yml`
- `一键启动.bat`

文档：

- `newapi-调度器二次开发计划.md`
- `调度器开发TODO.md`
- `调度器开发任务报告.md`
- `NewAPI二开管线布置.md`

## 12. 已知风险与注意事项

1. mock success 模式返回普通 JSON 对真实 OpenAI 流式协议来说不标准；NewAPI 现在兼容它，但长期最好让 mock success 也按 `stream:true` 返回 SSE。
2. 本次 fallback 只覆盖 OpenAI chat completion 普通 JSON；如果其他协议出现“请求流式、上游普通 JSON”的情况，需要按各自 DTO 单独补兼容，不能复用 OpenAI chunk。
3. 调度器空响应检测是为了 failover，不能对所有空响应路径无脑启用，否则会改变历史兼容行为。
4. 调度器第一版是单请求内失败计数，不代表多节点全局连续失败。
5. `go vet ./...` 历史上有基线既有问题，不能把它当成本次修复的唯一验收门槛。
6. 前端 default 是完整调度器 UI，classic 当前主要保证构建不坏；如果线上切 classic，需要另补 classic 页面。
7. API Key、上游错误、渠道 key 不应写入文档或普通用户可见日志；调度日志和文档只保留脱敏信息。

## 13. 本次 bug 的验收标准

修复完成后，至少满足：

1. `go test ./relay/channel/openai -count=1` 通过。
2. JSON fallback 测试能证明普通 JSON 被包装成 SSE。
3. 缺失 `Content-Type` 的 SSE 测试能证明不会被 fallback 误吞。
4. `git diff --check` 通过。
5. 手工链路中 `模拟API响应 -> NewAPI -> API压力测试` 不再返回 502 `empty_response`。
6. 若仍禁用渠道，先看 `channel_scheduler_logs` 中最后一条错误是否已经不是 `empty_response`，再继续排查上游业务内容。

## 14. 本轮已执行验证

已通过：

- `go test ./relay/channel/openai -count=1`
- `go test ./relay/channel/openai ./router ./middleware ./service ./model ./controller ./setting/operation_setting -count=1`
- `go build ./...`
- `git diff --check`

说明：

- 相关包测试和后端构建第一次执行时，因为沙箱拦截 Go 依赖下载失败；通过本机代理补齐依赖后复跑通过。
- 尚未在本轮启动三个外部服务做手工端到端压测；代码层面已经覆盖“流式请求收到普通 JSON 上游响应”的回归场景。
