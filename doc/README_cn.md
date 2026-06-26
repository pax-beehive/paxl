# paxl

`paxl` 是一个本地优先的 AI coding agent context bridge。

它用来在 Codex、Claude Code、Pi、Kiro、Gemini 和 OpenClaw 之间迁移工作上下文，
不需要手动复制长 transcript，也不需要把本地 session history 上传到云端服务。

最直接的使用场景是：某个本地 agent 额度用完、卡住了，或者另一个 agent 更适合下一步时，
用 `paxl` 把当前 session context 交给目标 agent，让工作继续往前走。

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

检查是否有新的 stable 托管版本：

```sh
paxl version --check
```

原地升级当前 binary：

```sh
paxl update
```

也可以从源码构建：

```sh
go build -trimpath -o ./paxl ./cmd/paxl
mkdir -p ~/bin
cp ./paxl ~/bin/paxl
```

## 第一次成功：换个 agent 继续

建议先跑这条最窄 workflow。它能验证核心概念，而且不用一次学完所有命令。

1. 检查哪些本地 agent 有 CLI、哪些已经能看到本地 session 日志：

   ```sh
   paxl agent list
   ```

2. 列出最近的 Claude Code sessions：

   ```sh
   paxl session list --agent claude --limit 5
   ```

3. 列出最近的 Codex sessions：

   ```sh
   paxl session list --agent codex --limit 5
   ```

4. 把 Claude session mirror 到已有 Codex session：

   ```sh
   paxl session mirror \
     claude:<source-session-id> \
     --to-session codex:<target-session-id> \
     --verbose
   ```

如果某个 agent 还没有本地日志，session list 会对该 agent 返回空列表，而不是让整条命令失败。

## 为什么用它

- 当 quota、模型表现或工具权限在任务中途变化时，把工作交给另一个 agent 继续。
- 在交接前把 session timeline 保存成 transcript、JSONL 或 HTML。
- 把决策、bug 线索、release plan、项目约定整理成可复用的 knowledge capsule。
- 把准备好的 handoff 注入已有 session，或者用它启动一个新的目标 agent session。
- 安装 Codex skill 后，让 agent 帮你选择和执行合适的 `paxl` 命令。

## Agent Skill

仓库内包含一个 Codex skill，用来稳定复用本地 knowledge transfer 流程：

```sh
mkdir -p ~/.codex/skills
cp -R skills/knowledge-transfer ~/.codex/skills/
```

如果想让 agent 帮你安装，可以让 agent 先阅读这个仓库，再从
`skills/knowledge-transfer` 安装 skill。可以直接这样说：

```text
Read this repository, inspect skills/knowledge-transfer/SKILL.md, then install
the knowledge-transfer skill into the Codex skills directory for all future
sessions on this machine.
```

安装后，在需要跨 Codex、Claude、Pi、Kiro、Gemini、OpenClaw session 转移上下文时，
让 Codex 使用 `knowledge-transfer` skill。它适合在你不想记具体参数时，让 agent 选择
应该跑 `session mirror` 还是 `capsule create` / `capsule inject`。

## 心智模型

`paxl` 里有三个核心概念：

- **session**：从本地 agent 日志中发现的一段本地会话。
- **mirror**：把一个 session 的上下文即时交接到另一个 agent session。
- **capsule**：可复用的本地 context artifact，可以查看、归档，也可以之后再注入。

需要马上接着干活时，用 `session mirror`。需要沉淀以后还能复用的知识时，用
`capsule create` 和 `capsule inject`。

当前内置 agents：

- `codex`：读取本地 Codex 日志，通过 Codex CLI 投递上下文。
- `claude`：读取本地 Claude Code 日志，通过 Claude Code CLI 投递上下文。
- `pi`：读取本地 Pi 日志，通过 Pi CLI 投递上下文。
- `kiro`：读取本地 Kiro CLI 日志，通过 Kiro CLI 投递上下文。
- `gemini`：读取本地 Gemini CLI 日志，通过 Gemini CLI 投递上下文。
- `openclaw`：通过 OpenClaw ACP 做 session list 和已有 session prompt 投递。默认命令
  是 `openclaw acp`；如果本机入口不同，设置 `PAXL_OPENCLAW_ACP_COMMAND`。

## 高级玩法

### 保存 session timeline

切换 agent 前，可以先把 session 渲染出来：

```sh
paxl session get claude:<session-id>
paxl session get codex:<session-id> --format jsonl
paxl session get codex:<session-id> --format html --output session.html
```

Transcript 适合直接阅读，JSONL 适合脚本处理，HTML 适合当作可携带的 review artifact。

### 带着上下文开新 session

目标 agent 不一定要有已有 session。可以直接用源上下文启动一个新的目标 session：

```sh
paxl session mirror claude:<source-session-id> --to codex
```

当你希望目标 agent 收到 handoff 后，在一个干净 session 中决定如何继续时，这个模式更合适。

### 沉淀可复用知识

让源 agent 围绕某个 keyword 生成 capsule：

```sh
paxl capsule create claude:<session-id> --keyword "release plan"
paxl capsule get <capsule-id>
paxl capsule inject <capsule-id> codex:<target-session-id>
```

Capsule 适合保存架构决策、debug 历史、release checklist、项目约定这类不应该只留在一个
conversation 里的上下文。

### 发给 friend

Cloud inbox 投递必须经过 accepted friend。不能直接发到裸邮箱；`--to` 必须是 `@alice`
这样的 friend alias。

```sh
paxl friend request alice@example.com --alias alice
# Alice 接受 friend request 后
paxl capsule send <capsule-id> --to @alice --message "please review"
paxl outbox list
paxl inbox list
paxl inbox accept <envelope-id>
paxl inbox accept --all
paxl inbox watch
```

发送方可以通过 outbox 跟踪已发送 envelope；接收方继续从 inbox 处理。接受 inbox envelope
后，远端 payload 会保存成本地 capsule。可以用 `paxl inbox accept --all` 一次性接受所有
pending inbox envelope，也可以运行 `paxl inbox watch` 在前台持续接受 pending envelope，
直到进程被停止。需要让某个本地 agent 接手时，再把这个 capsule inject 到目标 session。

### 转移人工整理好的上下文

如果你已经知道应该交接什么，可以直接从文件创建 capsule，不需要让源 agent 总结：

```sh
paxl capsule create codex:<session-id> \
  --keyword "production incident" \
  --title "api timeout incident" \
  --summary "Known facts, mitigations, and next checks." \
  --content-file handoff.md
```

### 让 agent 帮你操作

安装仓库里的 Codex skill 后，可以直接说“把这个上下文迁到 Claude”或“给这个 bug 建一个
capsule”，让 agent 去执行对应的 `paxl` 命令。

## 数据模型

`paxl` 是本地优先的。它读取本地 agent 日志，并使用本地 SQLite 缓存 session metadata、
knowledge capsule 和 injection 记录。

可以用 `--db` 指定 SQLite 文件：

```sh
paxl --db .local/paxl.sqlite session list
```

不指定 `--db` 时，`paxl` 会使用默认本地数据库路径。

## 隐私边界

`paxl` 不需要 cloud registration 就能查看和迁移本地 sessions。它会读取本地 agent
transcript 文件，把缓存 metadata、生成的 capsule 和 injection 记录写入本地 SQLite，
并把命令执行日志写到 `~/.pax/paxl/logs/`。

Mirror 和 capsule injection 会有意把选中的 session context 投递给目标本地 agent CLI。
如果需要确认即将投递的内容，可以先用 `paxl capsule get <id>` 查看 capsule。

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

`CLI` 列表示原生 agent 命令是否在 `PATH` 中；`SESSIONS` 列表示 `paxl` 是否能看到
该 agent 的本地 session 日志。

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

如果这条内容不需要绑定某个 source session，可以创建 manual capsule：

```sh
paxl capsule create --manual \
  --keyword "installer hosting" \
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
paxl capsule inject <capsule-id> codex:<target-session-id> \
  --action-items "run go test ./..." \
  --action-items "open a PR"
```

Capsule handoff 默认只做知识转移，不让目标 agent 直接行动。重复传 `--action-items`
可以把明确的可执行待办交给目标 agent。这里的 action item 指具体下一步，例如规划、
编辑文件、运行工具，或基于 capsule 继续推进任务。直接 inject 和 `--match` 排队 hook
inject 都可以使用 action items。

用 capsule 直接启动一个新的目标 agent session：

```sh
paxl capsule inject <capsule-id> --new --agent codex
```

归档 capsule：

```sh
paxl capsule archive <capsule-id>
```

把 capsule 发给 accepted friend：

```sh
paxl friend request alice@example.com --alias alice
# Alice 接受 friend request 后
paxl capsule send <capsule-id> --to @alice
paxl capsule send <capsule-id> --to @alice --match project --project pax-manager --agent codex
paxl capsule send <capsule-id> --to @alice --match keyword --keyword "capsule routing"
```

`capsule send` 必须使用 accepted friend alias。manager 也会强制检查这个边界，所以即使
绕开 CLI 直接调用 API，裸邮箱投递也会被拒绝。
条件发送会把 route 存进 envelope。收件人先自行 accept，目标 session 之后由本地
agent hook 在真实 prompt 触发时选择。

读取收到的 envelopes：

```sh
paxl inbox list
paxl inbox get <envelope-id>
paxl inbox accept <envelope-id>
paxl inbox archive <envelope-id>
```

跟踪已发送 envelopes：

```sh
paxl outbox list
paxl outbox list --status accepted
paxl outbox get <envelope-id>
```

管理 friends：

```sh
paxl friend list
paxl friend accept <friend-id> --alias alice
paxl friend remove <friend-id>
paxl friend block <friend-id>
```

## Agent 投递语义

Codex 投递：

- Codex App/Desktop 已有 session：`codex app-server` 的 `thread/resume` 后优先
  `turn/steer`；没有可 steer 的 active turn 时回退到 `turn/start`
- 其他已有 session 或 app-server 失败回退：`codex exec resume --all <session-id> -`
- 新 session：`codex exec -`
- 条件 hook 注入：通过 Codex `UserPromptSubmit` hook JSON 的
  `hookSpecificOutput.additionalContext` 注入到当前用户 prompt 之前。Codex
  可能需要用户先 trust 新增或变更的 hook。

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

OpenClaw 投递：

- 已有 session：通过 `openclaw acp` 发送 ACP `session/prompt`
- Session list：通过 ACP `session/list`
- 覆盖命令：`PAXL_OPENCLAW_ACP_COMMAND='openclaw --acp'`

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
目前内置支持 Codex、Claude、Pi、Kiro、Gemini 和 OpenClaw。

## 平台支持边界

CLI 架构和 SQLite 存储本身是跨平台 Go 代码，但当前内置 adapters 依赖本地 agent
日志路径和 native CLI。

当前支持边界：

- macOS：已经用本地 Codex、Claude Code、Pi、Kiro CLI 和 Gemini CLI 日志形态验证过。
  OpenClaw 通过 ACP contract tests 覆盖，需要本机存在 OpenClaw ACP 命令。
- Linux：如果存在 `~/.codex/sessions`、`~/.claude/projects`、
  `~/.pi/agent/sessions`、`~/.kiro/sessions`、`~/.gemini/tmp`，并且对应 CLI
  在 `PATH` 中，理论上和 macOS 很接近，但还需要真实环境验证。
- Windows：还没有充分验证。路径处理、Claude project 目录名解码、fake command
  测试方式、native CLI resume 行为都需要单独覆盖。

简而言之：macOS 已验证，Linux 预计可用但需要验证，Windows 目前应视为 experimental。
