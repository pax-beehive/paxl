# paxl

`paxl` 是一个本地优先的 CLI，用来查看、迁移和复用 AI agent 的 session context。

它适合在多个本地 coding agent 之间切换工作时使用。比如 Claude Code 额度用完了，
你可以把当前 Claude session mirror 到 Codex，让 Codex 带着同一份本地上下文继续工作。
同样的模式也可以用于 Pi、Kiro 和 Gemini。

英文文档：[../README.md](../README.md)

架构文档：[ARCHITECTURE.md](ARCHITECTURE.md)

## 快速安装

安装最新 stable 托管版本：

```sh
curl -fsSL https://api.paxtech.net/api/v1/public/paxl/install.sh | bash
```

安装指定上传版本：

```sh
curl -fsSL https://api.paxtech.net/api/v1/public/paxl/install.sh | PAXL_VERSION=0.1.0 bash
```

检查 binary：

```sh
paxl version
```

也可以从源码构建：

```sh
go build -trimpath -o ./paxl ./cmd/paxl
mkdir -p ~/bin
cp ./paxl ~/bin/paxl
```

## Agent Skill

仓库内包含一个 Codex skill，用来稳定复用本地 knowledge transfer 流程：

```sh
mkdir -p ~/.codex/skills
cp -R skills/knowledge-transfer ~/.codex/skills/
```

安装后，在需要跨 Codex、Claude、Pi、Kiro、Gemini session 转移上下文时，让
Codex 使用 `knowledge-transfer` skill。

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
- `pi`：读取本地 Pi 日志，通过 Pi CLI 投递上下文。
- `kiro`：读取本地 Kiro CLI 日志，通过 Kiro CLI 投递上下文。
- `gemini`：读取本地 Gemini CLI 日志，通过 Gemini CLI 投递上下文。

## 数据模型

`paxl` 是本地优先的。它读取本地 agent 日志，并使用本地 SQLite 缓存 session metadata、
knowledge capsule 和 injection 记录。

可以用 `--db` 指定 SQLite 文件：

```sh
paxl --db .local/paxl.sqlite session list
```

不指定 `--db` 时，`paxl` 会使用默认本地数据库路径。

## 执行日志

每次执行 `paxl` 命令时，都会在下面的目录写入一份 JSONL 执行日志：

```text
~/.pax/paxl/logs/
```

日志包含命令开始、结束、耗时、错误信息，以及被缓冲的 adapter diagnostic 输出。
正常命令输出不变；`--verbose` 仍然只控制是否把投递细节同时打印到 stderr。

## 质量指标

语句覆盖率仍然是 merge gate：

```sh
make test-cover
```

分支覆盖率通过 [`gobco`](https://github.com/rillig/gobco) 作为非阻塞质量指标统计：

```sh
make branch-cover-install
make branch-cover
```

分支覆盖率报告会输出每个 package 未覆盖的分支，并在最后给出总计，比如
`Branch coverage total: 792/1186 (66.8%)`。它用于指导测试 review，不作为 CI 硬门禁。

Mutation testing 通过 [`go-mutesting`](https://github.com/avito-tech/go-mutesting)
作为另一个非阻塞质量信号使用。该工具已经通过 Go tool dependency 固定在
`go.mod` 中，不需要额外安装：

```sh
make mutation-test
make mutation-test MUTATION_TARGETS=./internal/model/...
make mutation-test MUTATION_TARGETS=./internal/facade MUTATION_TIMEOUT=60
```

默认目标是 `./internal/model/store`，能覆盖非 trivial 的持久化行为，同时避免默认
对整个仓库做 mutation testing。报告会输出 surviving mutations 和 mutation score。
它适合用来判断高覆盖率区域是否真的断言了关键行为。

Cognitive complexity 通过 [`gocognit`](https://github.com/uudashr/gocognit)
统计，也已经固定为 Go tool dependency：

```sh
make cognitive-complexity
make cognitive-complexity COGNITIVE_TARGETS=./pkg/adaptor COGNITIVE_TOP=10
```

默认报告会输出生产代码里 cognitive complexity 最高的 20 个函数，以及仓库平均值。
它适合和 cyclomatic complexity 一起判断某个函数是否难以理解。

## Release 上传

`paxl` 以原生 Go binary 的形式发布到 GCS。release 脚本默认从最新本地
`paxl/vX.Y.Z` git tag 递增 patch 版本；如果没有 release tag，则从
`cmd/paxl/main.go` 里的版本开始递增。

不上传、不打 tag 的 dry run：

```sh
make release-paxl-dry-run
make release-paxl-dry-run RELEASE_VERSION=minor RELEASE_TAGS=beta
```

上传 stable release：

```sh
make release-paxl
```

脚本会构建 `darwin/amd64`、`darwin/arm64`、`linux/amd64` 和 `linux/arm64`，
通过 ldflags 写入 version 和 commit metadata，用 `paxl version` 对当前主机平台
binary 做 smoke test，生成 sha256 文件和 `manifest.json`，并上传到：

```text
gs://pax-tech-bucket/paxl/releases/<version>/
```

对于每个 release tag，脚本也会更新：

```text
gs://pax-tech-bucket/paxl/releases/latest/<tag>/manifest.json
```

同时也会上传 installer：

```text
gs://pax-tech-bucket/paxl/install.sh
```

上传成功后，脚本会创建本地 git tag：

```text
paxl/v<version>
```

设置 `PAX_RELEASE_PUSH_TAG=1` 会推送 tag。`RELEASE_VERSION=0.2.0` 可以指定明确
semantic version；`RELEASE_VERSION=major|minor|patch` 则按语义版本自动递增。

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

Mirror handoff 会带 `From` 和 `To` metadata，包括 node、agent 和 session ID。
Node ID 优先使用 `PAXL_NODE_ID`，没有设置时使用本机 hostname。

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

Capsule 会记录 source node、source agent 和 source session。Injection 记录会补上
target node、target agent 和 target session；这些 metadata 会出现在 JSONL 输出和
投递给目标 agent 的 handoff 文本里。

让源 agent 生成 capsule：

```sh
paxl capsule create claude:<session-id> --keyword "release plan"
```

本地 transcript 提取是离线 fallback。它保存匹配到的原文行，不会让源 agent 总结：

```sh
paxl capsule create codex:<session-id> --keyword "sqlite schema" --local
```

如果要转发一条已经整理好的需求，可以直接从文件创建 capsule：

```sh
paxl capsule create codex:<session-id> \
  --keyword "installer hosting" \
  --title "paxl installer hosting" \
  --summary "Installer upload and hosting requirement." \
  --content-file capsule.md
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

用 capsule 直接启动一个新的目标 agent session：

```sh
paxl capsule inject <capsule-id> --new --agent codex
```

归档 capsule：

```sh
paxl capsule archive <capsule-id>
```

## Agent 投递语义

Codex 投递：

- Codex App/Desktop 已有 session：`codex app-server` 的 `thread/resume` 后优先
  `turn/steer`；没有可 steer 的 active turn 时回退到 `turn/start`
- 其他已有 session 或 app-server 失败回退：`codex exec resume --all <session-id> -`
- 新 session：`codex exec -`

Claude 投递：

- 已有 session：`claude --print --resume <session-id>`
- 新 session：`claude --print`

Pi 投递：

- 已有 session：`pi --session <session-id> -p`
- 新 session：`pi -p`

Kiro 投递：

- 已有 session：`kiro-cli chat --resume-id <session-id> --no-interactive <message>`
- 新 session：`kiro-cli chat --no-interactive <message>`

Gemini 投递：

- 已有 session：`gemini --resume <session-id> -p <message>`
- 新 session：`gemini -p <message>`

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
目前内置支持 Codex、Claude、Pi、Kiro 和 Gemini。

## 平台支持边界

CLI 架构和 SQLite 存储本身是跨平台 Go 代码，但当前内置 adapters 依赖本地 agent
日志路径和 native CLI。

当前支持边界：

- macOS：已经用本地 Codex、Claude Code、Pi、Kiro CLI 和 Gemini CLI 日志形态验证过。
- Linux：如果存在 `~/.codex/sessions`、`~/.claude/projects`、
  `~/.pi/agent/sessions`、`~/.kiro/sessions`、`~/.gemini/tmp`，并且对应 CLI
  在 `PATH` 中，理论上和 macOS 很接近，但还需要真实环境验证。
- Windows：还没有充分验证。路径处理、Claude project 目录名解码、fake command
  测试方式、native CLI resume 行为都需要单独覆盖。

简而言之：macOS 已验证，Linux 预计可用但需要验证，Windows 目前应视为 experimental。
