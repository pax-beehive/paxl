# paxl

`paxl` 是一个本地优先的 CLI，用来查看、迁移和复用 AI agent 的 session context。

它适合在多个本地 coding agent 之间切换工作时使用。比如 Claude Code 额度用完了，
你可以把当前 Claude session mirror 到 Codex，让 Codex 带着同一份本地上下文继续工作。
同样的模式也可以扩展给未来的 Pi agent 或其他 agent，只要实现对应 adapter。

英文文档：[../README.md](../README.md)

架构文档：[ARCHITECTURE.md](ARCHITECTURE.md)

## 能做什么

- 查看当前支持的本地 agents。
- 从本地日志列出 agent sessions。
- 把 session timeline 渲染成 transcript、JSONL 或 HTML。
- 把一个 session mirror 到另一个 agent session。
- 从源 session 创建可复用的 knowledge capsule。
- 把 knowledge capsule 注入目标 session。
- 使用本地 SQLite 存储 metadata、capsule 和 injection 记录。

当前内置 agents：

- `codex`：读取本地 Codex 日志，通过 Codex CLI 投递上下文。
- `claude`：读取本地 Claude Code 日志，通过 Claude Code CLI 投递上下文。

## 安装

从源码构建：

```sh
go build -trimpath -o ./paxl ./cmd/paxl
```

可选：安装到本地 bin：

```sh
mkdir -p ~/bin
cp ./paxl ~/bin/paxl
```

检查 binary：

```sh
paxl version
```

## 数据模型

`paxl` 是本地优先的。它读取本地 agent 日志，并使用本地 SQLite 缓存 session metadata、
knowledge capsule 和 injection 记录。

可以用 `--db` 指定 SQLite 文件：

```sh
paxl --db .local/paxl.sqlite session list
```

不指定 `--db` 时，`paxl` 会使用默认本地数据库路径。

## 常用工作流

### 查看可用 agents

```sh
paxl agent list
```

### 列出本地 sessions

```sh
paxl session list
paxl session list --agent claude --limit 10
paxl session list --agent codex --format jsonl
```

只使用 SQLite 缓存，不重新扫描本地日志：

```sh
paxl session list --no-sync
```

### 查看 session 内容

```sh
paxl session get claude:<session-id>
paxl session get codex:<session-id> --format html --output session.html
```

### 把一个 session mirror 到另一个 agent

`session mirror` 会把源 session 的上下文投递到目标 agent session。它不会要求源 agent
根据 keyword 做总结。目标 agent 会收到一条 `system_handoff`，之后可以自己决定是否压缩、
总结或完整保留这份上下文。

把 Claude session mirror 到已有 Codex session：

```sh
paxl session mirror \
  claude:<source-session-id> \
  --to-session codex:<target-session-id>
```

从 Claude session 启动一个新的 Codex session：

```sh
paxl session mirror \
  claude:<source-session-id> \
  --to codex
```

从 Codex session 启动一个新的 Claude session：

```sh
paxl session mirror \
  codex:<source-session-id> \
  --to claude
```

### Claude Code 额度用完时怎么办

1. 找到最新的 Claude session：

   ```sh
   paxl session list --agent claude --limit 5
   ```

2. 找到你想接手工作的 Codex session：

   ```sh
   paxl session list --agent codex --limit 5
   ```

3. 把 Claude context mirror 到 Codex：

   ```sh
   paxl session mirror \
     claude:<source-session-id> \
     --to-session codex:<target-session-id> \
     --verbose
   ```

Codex 会通过自己的 native resume 路径收到这份上下文。之后你可以让 Codex 继续执行、
先 review handoff，或者把上下文压缩成后续可用的摘要。

### 创建 knowledge capsule

Knowledge capsule 是可复用的 handoff artifact。和 `session mirror` 不同，capsule
创建是 keyword-driven 的，默认会让源 agent 生成一个可移植的总结。

让源 agent 生成 capsule：

```sh
paxl capsule create claude:<session-id> --keyword "release plan"
```

改用本地 transcript 提取：

```sh
paxl capsule create codex:<session-id> --keyword "sqlite schema" --local
```

列出和查看 capsules：

```sh
paxl capsule list
paxl capsule get <capsule-id>
```

把 capsule 注入目标 session：

```sh
paxl capsule inject <capsule-id> codex:<target-session-id>
```

归档 capsule：

```sh
paxl capsule archive <capsule-id>
```

## Agent 投递语义

Codex 投递：

- 已有 session：`codex exec resume --all <session-id> -`
- 新 session：`codex exec -`

Claude 投递：

- 已有 session：`claude --print --resume <session-id>`
- 新 session：`claude --print`

`paxl` 默认会缓冲子进程 stdout/stderr，避免污染命令输出。需要查看投递细节时使用
`--verbose`。

## 开发

常用命令：

```sh
make format
make lint
make test
make test-cover
make mock
make gen
```

CI 的 coverage 门槛是 80%。

## 当前状态

`paxl` 还是早期 open-source CLI。架构上支持继续扩展更多 agent adapters，
但目前内置支持的是 Codex 和 Claude。

## 平台支持边界

CLI 架构和 SQLite 存储本身是跨平台 Go 代码，但当前内置 adapters 依赖本地 agent
日志路径和 native CLI。

当前支持边界：

- macOS：已经用本地 Codex 和 Claude Code 日志验证过。
- Linux：如果存在 `~/.codex/sessions`、`~/.claude/projects`，并且 `codex`、
  `claude` 在 `PATH` 中，理论上和 macOS 很接近，但还需要真实环境验证。
- Windows：还没有充分验证。路径处理、Claude project 目录名解码、fake command
  测试方式、native CLI resume 行为都需要单独覆盖。

简而言之：macOS 已验证，Linux 预计可用但需要验证，Windows 目前应视为 experimental。
