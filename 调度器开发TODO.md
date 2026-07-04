# 调度器二次开发 TODO

依据：`newapi-调度器二次开发计划.md`（合并版计划书）
开发分支：`feature/scheduler-failover`（自 `custom-v1.0.0-rc.15` 切出）
开始时间：2026-07-03

状态图例：[ ] 待办 [~] 进行中 [x] 完成 [!] 受阻/有风险

## 任务清单

- [x] T1 环境准备：工作区内便携 Go 工具链（.tools/，缓存全部指向工作区内，不动系统）
- [x] T2 探索汇总与计划校准（4 个并行探索代理 + 亲读 relay.go）
- [x] T3 创建 feature/scheduler-failover 分支
- [x] T4 后端：全局调度器配置项（channel_scheduler_setting 分层配置，11 项，默认关闭总开关，观察模式默认开）
- [x] T5 后端：Channel 新字段（auto_disabled_until + 5 个渠道级调度配置）+ channel_scheduler_logs 表 + AutoMigrate（主库，三库兼容）
- [x] T6 后端：候选渠道分桶能力（内存缓存 + ability 直查双路径，顺序一致）
- [x] T7 后端：ChannelScheduler 核心（同渠道阈值重试→临时禁用→同级换渠道→降级）+ 禁用/恢复逻辑（幂等、同步 ability 与缓存）
- [x] T8 后端：relay 接入（总开关默认关、观察模式只记日志、流式/任务 relay 默认不启用、MaxAttempts 上限）+ 自动恢复后台任务
- [x] T9 后端：/api/channel_scheduler 路由组（logs/stat/disabled/config/channel config/restore/清理，Admin/Root 分权，日志脱敏）
- [x] T10 后端：调度算法单测（计划书第18节全部用例）；当前阶段后端相关包测试与 go build ./... 通过。go vet ./... 仍有基线既有问题（common/custom-event.go 复制锁、common/email_test.go IPv6 格式、多个 provider adaptor unreachable code），未在本阶段处理。
- [x] T11 前端 default：调度日志独立页（/usage-logs/scheduler，侧边栏"使用日志"下新增入口，复用现有表格/筛选）
- [x] T12 前端 default：渠道页调度器面板（全局设置入口 + 临时禁用面板 + 行操作：调度设置/手动恢复/查看调度日志）+ i18n 六语言
- [x] T13 前端验证：default typecheck+build 通过；classic build 通过（classic 本期仅保证构建不坏）
- [x] T14 验收：本地端到端验收 27/27 全部通过（mock 上游 + SQLite + 内存缓存：A×3→禁用7200s→B×3→禁用→C 成功；渠道级阈值/时长覆盖精确生效；手动恢复；恢复 API 拒绝手动禁用渠道；调度日志/统计/临时禁用列表正确）。dev compose 已完成 rebuild/API 冒烟：`new-api-dev:local` 镜像源码戳为 `42ce882ec7b53321`，`/api/channel_scheduler/logs`、`/logs/stat`、`/disabled`、`/config`、`/channel/1/config`、`/restore/1` 均返回鉴权响应而非 404，覆盖截图 1/2 的调度器接口 404 问题。
- [x] T15 多视角对抗审查完成（15 代理）：确认 8 项问题已全部修复（i18n 命名空间、auto_disabled_until 残留、多 Key 渠道 key 级禁用保留、会话失败降级、索引定义、默认时间范围、数字输入、禁用通知）；复审新增 2 项恢复语义问题（未到期手动恢复、不可自动恢复渠道唤醒恢复任务）已修复并补测试。
- [x] T16 截图 404 二次修复：为调度器后端 API 增加 `/api/channel-scheduler/*` 兼容别名，与标准 `/api/channel_scheduler/*` 共用同一套 controller 和鉴权链路；新增路由表回归测试，防止旧前端包或浏览器缓存继续请求 hyphen 路径时返回 404。

## 关键设计决定（依据计划书）

1. 新增 `ChannelScheduler` 层，不复用 `RetryTimes` 语义，旧调度逻辑零改动保留。
2. 渠道级配置字段全部用指针类型：`nil` = 使用全局默认（不把 0 当"未设置"）。
3. `auto_disabled_until` 为 channels 表明确字段（bigint, default 0, index），不塞 other_info。
4. 调度日志表 `channel_scheduler_logs` 放主库 DB，不放 LOG_DB（规避 ClickHouse）。
5. 单次请求内计数（不做跨节点全局计数，第一版）。
6. 自动恢复只恢复 `status=auto_disabled 且 auto_disabled_until>0 且已到期 且允许自动恢复` 的渠道。
7. 流式已输出内容后不换渠道重试；尊重 shouldRetry / SkipRetry / channel affinity / auto_ban。
8. 前端复用现有组件与视觉体系，全部新文案走 i18n（en/zh/fr/ru/ja/vi）。

## 验证命令

```bash
# 后端（便携工具链，.tools/goenv.ps1 注入环境）
go vet ./...
go build ./...
go test ./...

# 前端
cd web/default && bun run typecheck && bun run build
cd web/classic && bun run build
```

## 进度日志

- 2026-07-03: 任务启动。发现本机无 Go（用户日常用 Docker 构建后端），采用工作区内便携 Go 方案。探索 workflow 已在后台运行。
- 2026-07-03: Codex 接手审核。截图 1/2 的 404 初步定位为前端已包含调度器页面、但开发后端容器可能仍是旧镜像；已改造 dev Docker 镜像源码戳与一键启动脚本自动 rebuild 检测，避免新前端请求旧后端接口。
- 2026-07-03: 处理复审反馈。修复手动恢复必须等 auto_disabled_until 到期后才允许执行；恢复任务 Enabled 查询收窄为“已到期且允许自动恢复”的临时禁用渠道；前端三个恢复入口复用现有按钮/菜单，仅在到期后显示。
- 2026-07-03: 验证结果：新增恢复语义测试先红后绿；Docker Go 环境下 go test ./service ./model ./controller ./setting/operation_setting 通过，go build ./... 通过；web/default npm run typecheck --workspace default 与 npm run build --workspace default 通过；git diff --check 通过；go vet ./... 失败于基线既有问题，未发现本次调度器相关 vet 输出。
- 2026-07-03: 前端格式检查补充：npm run format:check --workspace default 失败于大量既有格式差异；本阶段只对改动的 3 个 TSX 文件执行 oxfmt，并恢复版权头到文件顶部，随后 typecheck/build 复验通过。
- 2026-07-03: 处理阶段复审 3 项 Important：一键启动后端源码戳从 HEAD-dirty 改为后端相关文件内容哈希，避免 dirty 状态下继续修改仍复用旧镜像；恢复任务 handler 取消全局开关空跑短路，只在存在已到期且允许自动恢复渠道时启用；一键启动 stop 默认只停本项目服务，不再关闭整个 Docker Desktop，新增 stop-all 才执行全局关闭。
- 2026-07-03: 复审修复验证：新增 TestSchedulerRecoverHandlerEnabledRequiresRecoverableChannel 先红后绿；一键启动.bat probe 输出内容哈希 Source stamp 并判定旧镜像需 rebuild；Docker Go 环境 go test ./service ./model ./controller ./setting/operation_setting 通过，go build ./... 通过；web/default typecheck/build 通过，web/classic build 通过；git diff --check 通过。
- 2026-07-04: 补做 dev compose 冒烟：`一键启动.bat probe` 输出 Source stamp `42ce882ec7b53321` 且判定需 rebuild；执行 `docker compose -f docker-compose.dev.yml up -d --build new-api` 后后端容器启动完成；六个前端调度器 API 路径均返回 401 Unauthorized 而不是 404，说明开发后端已加载新路由。观察到 Docker build context 约 3.36GB，后续可单独优化 `.dockerignore`，本阶段不扩大改动面。
- 2026-07-04: 最终验收复跑通过：便携 Go 环境 `go test ./service ./model ./controller ./setting/operation_setting` 通过，`go build ./...` 通过；Bun 环境 `web/default` 的 `bun run typecheck` 与 `bun run build` 通过，`web/classic` 的 `bun run build` 通过；`git diff --check` 通过，提交后工作区保持干净。
- 2026-07-04: 针对用户最新截图继续排查渠道页标题旁“调度器”入口与“调度日志”页 404。当前标准路径 `/api/channel_scheduler/*` 在 3000/3001 均已返回 401 非 404；复现到旧命名 `/api/channel-scheduler/*` 仍为 404，按 TDD 新增 `TestChannelSchedulerRoutesIncludeHyphenCompatibleAliases` 先红后绿，并在 `router/api-router.go` 中给同一组调度器 API 注册 hyphen 兼容别名。验证：`go test ./router ./service ./model ./controller ./setting/operation_setting` 通过，`go build ./...` 通过；本机无 Bun，改用 npm workspace 等价执行 `default` typecheck/build 与 `classic` build 均通过；重建 `new-api-dev` 后，3000 与 3001 上 underscore/hyphen 两组 14 个调度器 API 探针均返回 401 而非 404。
