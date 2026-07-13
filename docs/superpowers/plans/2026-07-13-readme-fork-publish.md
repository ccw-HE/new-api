# Fork README and GitHub Publish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为个人二开分支补充详细、可追溯的中文说明，重新验证依赖与安全状态，并推送到个人 GitHub 仓库后创建指向 `main` 的 Draft PR。

**Architecture:** 保留上游 README、许可证和署名，只在主 README 增加精简入口，把二开差异集中到独立 `README-FORK.md`。发布过程将文档、依赖锁文件和安全忽略规则作为一个明确范围进行验证和提交，不处理用户已排除的业务审查项，也不向上游仓库创建 PR。

**Tech Stack:** Markdown、Git、GitHub CLI、Go 1.25.1+、Bun、npm、Gitleaks、govulncheck

---

## File Map

- Create: `README-FORK.md`，详细记录二开身份、功能机制、配置、API、构建、安全、限制与上游同步方法。
- Modify: `README.md`，在标题区域后加入英文二开提示和 `README-FORK.md` 链接。
- Modify: `README.zh_CN.md`，在标题区域后加入中文二开提示和 `README-FORK.md` 链接。
- Create: `docs/superpowers/plans/2026-07-13-readme-fork-publish.md`，记录本实施计划。
- Modify: `docs/superpowers/specs/2026-07-13-readme-fork-publish-design.md`，按实际 `go.mod` 和渠道开关行为纠正规格事实。
- Preserve: `LICENSE`、`NOTICE`、其他语言 README、Go 模块路径、Docker 镜像名和所有 New API、QuantumNous 署名。
- Include in final implementation commit: `.dockerignore`、`.gitignore`、`electron/package-lock.json`、`go.mod`、`go.sum`、`web/bun.lock`、`web/default/package.json`、`web/package.json`。

### Task 1: Create the detailed fork README

**Files:**
- Create: `README-FORK.md`
- Reference: `docs/superpowers/specs/2026-07-13-readme-fork-publish-design.md`
- Reference: `service/channel_scheduler.go`
- Reference: `model/channel_scheduler.go`
- Reference: `model/channel_scheduler_log.go`
- Reference: `setting/operation_setting/channel_scheduler_setting.go`
- Reference: `setting/operation_setting/status_code_ranges.go`
- Reference: `controller/channel_scheduler.go`
- Reference: `router/api-router.go`

- [x] **Step 1: Write the fork identity and scope sections**

Create `README-FORK.md` with these opening sections and exact facts:

```markdown
# New API 个人二开说明

> 本仓库是基于 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 的个人二开版本，不代表上游官方发行版。New API、QuantumNous、原项目许可证、NOTICE、模块路径和原始署名均保持不变。

## 上游关系

| 项目 | 内容 |
| --- | --- |
| 上游仓库 | `https://github.com/QuantumNous/new-api` |
| 个人仓库 | `https://github.com/ccw-HE/new-api` |
| 功能分支 | `feature/scheduler-failover` |
| 分叉基准 | `69b0f0b56f528efa292a2893feb0c55c37399f4b` |
| 主要方向 | 高级渠道调度、同级故障转移、转发韧性、调度日志和依赖安全维护 |
```

- [x] **Step 2: Add the complete scheduler mechanism**

Add sections covering all items below with implementation-grounded prose and tables:

```text
启用与旧逻辑回退
候选渠道获取、priority 降序分桶和同级权重选择
同渠道连续重试与全局/渠道级失败阈值
错误分类：停止、重试当前渠道、切换但不禁用、达到阈值后禁用
可配置自动重试和自动禁用 HTTP 状态码范围
504、524 和坏响应体错误的强制保护
重试随机抖动及 0/0、100-10000ms 约束
事务、CAS、Ability 状态与内存缓存同步组成的临时禁用机制
respect_auto_ban 行为
自动恢复、关闭调度器后的存量恢复、渠道级自动恢复开关
旧恢复任务的 stale-cycle 防护
手动恢复权限和管理审计
内部尝试预算与异常候选保险丝
```

- [x] **Step 3: Add exact configuration tables**

The global configuration table must contain:

```markdown
| 字段 | 默认值 | 约束 | 作用 |
| --- | ---: | --- | --- |
| `enabled` | `false` | 布尔值 | 启用高级调度器；关闭时使用旧调度逻辑 |
| `channel_failure_threshold` | `3` | `1-100` | 单渠道连续失败阈值 |
| `auto_disable_seconds` | `7200` | 正整数 | 达到阈值后的临时禁用时长 |
| `retry_jitter_min_ms` | `0` | `0` 或 `100-10000` | 重试随机延迟下限 |
| `retry_jitter_max_ms` | `0` | `0` 或 `100-10000`，且不小于下限 | 重试随机延迟上限 |
| `allow_priority_fallback` | `true` | 布尔值 | 同优先级耗尽后是否进入低优先级 |
| `log_enabled` | `true` | 布尔值 | 是否写入独立调度日志 |
| `respect_auto_ban` | `true` | 布尔值 | 是否尊重渠道自动封禁开关 |
| `retry_same_channel` | `true` | 布尔值 | 是否优先在同一渠道重试至阈值 |
```

The channel configuration table must contain:

```text
scheduler_enabled
scheduler_retry_times
scheduler_auto_disable_seconds
scheduler_auto_recover_enabled
scheduler_manual_restore_allowed
auto_disabled_until
```

Explain that nullable override fields inherit the global value and that resetting them to `null` restores inheritance.

- [x] **Step 4: Add logs, UI and API sections**

Document the five event types exactly:

```text
failure
observe_disable
auto_disable
auto_recover
manual_restore
```

Document the log fields, filters, summary statistics, main-database storage, non-blocking write failure behavior, batch cleanup, system-task lease behavior, Default frontend entries, and these routes:

```text
GET    /api/channel_scheduler/logs
GET    /api/channel_scheduler/logs/stat
DELETE /api/channel_scheduler/logs
GET    /api/channel_scheduler/disabled
GET    /api/channel_scheduler/config
PUT    /api/channel_scheduler/config
GET    /api/channel_scheduler/channel/:id/config
PUT    /api/channel_scheduler/channel/:id/config
POST   /api/channel_scheduler/restore/:id
```

Also state that `/api/channel-scheduler/...` is a compatibility prefix.

- [x] **Step 5: Add relay resilience, compatibility and security sections**

Cover:

```text
OpenAI Chat、OpenAI Responses、Gemini、Claude 空响应识别
请求体复用与 Content-Length 边界
流式响应 Header 和生命周期保持
Header 显式覆盖、API Key 占位符、客户端 Header 占位符
全部 Header 与正则透传规则
Hop-by-hop、Host、Cookie、认证、长度和 WebSocket Header 跳过规则
SQLite、MySQL、PostgreSQL 和内存缓存兼容性
依赖升级、.env/.snow 忽略和 Gitleaks 扫描范围
```

- [x] **Step 6: Add setup, verification, limitations, sync and license sections**

Include runnable command blocks for:

```powershell
go test ./...
go build -o "$env:TEMP\new-api-fork-check.exe" .
govulncheck ./...

cd web
bun install --frozen-lockfile
bun audit

cd default
bun run typecheck
bun test
bun run build

cd ..\classic
bun run build

cd ..\..\electron
npm audit
npm ls --all
node --check main.js
node --check preload.js
```

State that users must build their own binary or Docker image from this branch because upstream public images do not contain fork-only changes. Add the known limitations and AGPLv3 obligations from the design spec.

- [x] **Step 7: Scan the fork README for unsupported claims**

Run:

```powershell
rg -n "官方支持|生产级保证|零漏洞|绝对安全|自动秒级恢复|TBD|TODO|FIXME" README-FORK.md
```

Expected: no unsupported claim or placeholder matches.

### Task 2: Add minimal entry notices to upstream README files

**Files:**
- Modify: `README.md`
- Modify: `README.zh_CN.md`

- [x] **Step 1: Insert the English notice**

Immediately after the closing `</div>` of the top title/navigation block in `README.md`, add:

```markdown
> [!NOTE]
> This repository contains personal fork enhancements. See [Fork Changes and Maintenance Notes](./README-FORK.md) for the implementation details, build instructions, and differences from upstream. The original New API and QuantumNous attribution remains unchanged.
```

- [x] **Step 2: Insert the Chinese notice**

Immediately after the closing `</div>` of the top title/navigation block in `README.zh_CN.md`, add:

```markdown
> [!NOTE]
> 当前仓库包含个人二开功能。详细实现机制、构建方式和上游差异请查看[二开说明](./README-FORK.md)。New API 与 QuantumNous 的原始署名保持不变。
```

- [x] **Step 3: Verify protected attribution remains present**

Run:

```powershell
rg -n "New API|QuantumNous|README-FORK.md" README.md README.zh_CN.md README-FORK.md NOTICE
```

Expected: all three README files retain New API and QuantumNous references, and both main README files link to `README-FORK.md`.

### Task 3: Review documentation and the intended commit scope

**Files:**
- Review: `README-FORK.md`
- Review: `README.md`
- Review: `README.zh_CN.md`
- Review: `.gitignore`
- Review: `.dockerignore`
- Review: dependency manifests and lock files

- [x] **Step 1: Run Markdown and whitespace checks**

Run:

```powershell
git diff --check
rg -n -i "TBD|TODO|FIXME|PLACEHOLDER|待定|稍后补充" README-FORK.md README.md README.zh_CN.md
```

Expected: `git diff --check` exits 0 and the placeholder search has no matches.

- [x] **Step 2: Review all unpublished changes**

Run:

```powershell
git status --short --branch -uall
git diff --stat HEAD
git diff HEAD -- .dockerignore .gitignore README.md README.zh_CN.md README-FORK.md docs/superpowers/plans/2026-07-13-readme-fork-publish.md docs/superpowers/specs/2026-07-13-readme-fork-publish-design.md electron/package-lock.json go.mod go.sum web/bun.lock web/default/package.json web/package.json
```

Expected: no unrelated files appear.

### Task 4: Run fresh dependency, test and build verification

**Files:**
- Verify only; do not intentionally modify tracked files.

- [x] **Step 1: Verify Go**

Run from repository root:

```powershell
go test ./...
go build -o "$env:TEMP\new-api-readme-final.exe" .
govulncheck ./...
```

Expected: tests and build exit 0; govulncheck reports no reachable vulnerability affecting the current code.

- [x] **Step 2: Verify the web workspace**

Run from `web/`:

```powershell
bun install --frozen-lockfile
bun audit
```

Expected: frozen install exits 0 and audit reports no known vulnerabilities.

- [x] **Step 3: Verify Default frontend**

Run from `web/default/`:

```powershell
bun run typecheck
bun test
bun run build
```

Expected: typecheck exits 0, all configured tests pass, and the production build exits 0.

- [x] **Step 4: Verify Classic frontend**

Run from `web/classic/`:

```powershell
bun run build
```

Expected: production build exits 0.

- [x] **Step 5: Verify Electron**

Run from `electron/`:

```powershell
npm audit
npm ls --all
node --check main.js
node --check preload.js
npx electron-builder --version
```

Expected: audit reports zero known vulnerabilities, dependency tree is valid, both scripts pass syntax checks, and electron-builder prints a version.

### Task 5: Run fresh secret and sensitive-file scans

**Files:**
- Verify only.

- [x] **Step 1: Check tracked environment and development files**

Run:

```powershell
git ls-files | rg "(^|/)(\.env($|\.)|\.snow(/|$))"
```

Expected: only approved example environment files may match; `.snow` must not be tracked.

- [x] **Step 2: Scan unpublished changes**

Run from repository root. Scan each intended file independently so concatenating unrelated files cannot create cross-file false positives:

```powershell
$files = @(
  '.dockerignore', '.gitignore', 'README.md', 'README.zh_CN.md',
  'README-FORK.md',
  'docs/superpowers/plans/2026-07-13-readme-fork-publish.md',
  'docs/superpowers/specs/2026-07-13-readme-fork-publish-design.md',
  'electron/package-lock.json', 'go.mod', 'go.sum', 'web/bun.lock',
  'web/default/package.json', 'web/package.json'
)
foreach ($file in $files) {
  gitleaks dir $file --no-banner --redact --exit-code 1
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
```

Expected: no verified secret in current unpublished changes.

- [x] **Step 3: Scan complete Git history**

Run:

```powershell
gitleaks git . --no-banner --redact --exit-code 1
```

Expected: any findings are reviewed against known upstream false positives; no usable credential is present.

### Task 6: Commit the reviewed implementation scope

**Files:**
- Stage exactly: `.dockerignore`
- Stage exactly: `.gitignore`
- Stage exactly: `README.md`
- Stage exactly: `README.zh_CN.md`
- Stage exactly: `README-FORK.md`
- Stage exactly: `docs/superpowers/plans/2026-07-13-readme-fork-publish.md`
- Stage exactly: `docs/superpowers/specs/2026-07-13-readme-fork-publish-design.md`
- Stage exactly: `electron/package-lock.json`
- Stage exactly: `go.mod`
- Stage exactly: `go.sum`
- Stage exactly: `web/bun.lock`
- Stage exactly: `web/default/package.json`
- Stage exactly: `web/package.json`

- [ ] **Step 1: Stage explicit paths only**

Run:

```powershell
git add -- .dockerignore .gitignore README.md README.zh_CN.md README-FORK.md docs/superpowers/specs/2026-07-13-readme-fork-publish-design.md electron/package-lock.json go.mod go.sum web/bun.lock web/default/package.json web/package.json
git add -f -- docs/superpowers/plans/2026-07-13-readme-fork-publish.md
```

- [ ] **Step 2: Verify the staged diff**

Run:

```powershell
git diff --cached --check
git diff --cached --stat
git status --short --branch -uall
```

Expected: only the thirteen explicit paths above are staged and there are no whitespace errors.

- [ ] **Step 3: Commit**

Run:

```powershell
git commit -m "docs: document fork and secure dependencies"
```

Expected: commit succeeds and the worktree becomes clean.

### Task 7: Authenticate, push and create a personal Draft PR

**Files:**
- Use: `.github/PULL_REQUEST_TEMPLATE.md`
- Create outside repository: temporary PR body file

- [ ] **Step 1: Re-authenticate GitHub CLI**

Run:

```powershell
gh auth login -h github.com -p https -w
gh auth status
```

Expected: GitHub reports an active authenticated account with access to `ccw-HE/new-api`.

- [ ] **Step 2: Confirm repository and base branch**

Run:

```powershell
gh repo view ccw-HE/new-api --json nameWithOwner,defaultBranchRef,url
git branch --show-current
git log -2 --oneline
```

Expected: repository is `ccw-HE/new-api`, default branch is `main`, current branch is `feature/scheduler-failover`, and the two latest commits are the design and implementation commits.

- [ ] **Step 3: Push the feature branch**

Run:

```powershell
git push -u origin feature/scheduler-failover
```

Expected: the remote branch is created or updated and local tracking is configured.

- [ ] **Step 4: Create the Draft PR using the repository template**

Create a temporary PR body that preserves `.github/PULL_REQUEST_TEMPLATE.md` headings and includes:

```text
变更描述：高级渠道调度、同级故障转移、临时禁用与恢复、调度日志、转发韧性、依赖安全修复和详细二开文档。
变更类型：Bug 修复、New feature、Documentation。
关联任务：无上游 Issue，不提交上游 PR。
人工确认：代码和文档经过人工范围确认，并使用 AI 辅助审查、修改和验证。
验证：列出本次实际通过的 Go、Bun、npm、Gitleaks 和构建命令。
```

Run:

```powershell
gh pr create --repo ccw-HE/new-api --base main --head feature/scheduler-failover --draft --title "[codex] document scheduler fork and secure dependencies" --body-file "$env:TEMP\new-api-pr-body.md"
```

Expected: GitHub returns a Draft PR URL in `ccw-HE/new-api`; no PR is created in `QuantumNous/new-api`.

- [ ] **Step 5: Verify remote state**

Run:

```powershell
gh pr view --repo ccw-HE/new-api --json url,isDraft,baseRefName,headRefName,title
git status --short --branch -uall
```

Expected: `isDraft=true`, base is `main`, head is `feature/scheduler-failover`, and the local branch tracks the remote with a clean worktree.
