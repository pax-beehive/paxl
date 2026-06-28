# paxl

**给 AI coding agents 用的本地优先上下文转移工具。**

`paxl` 让 Codex、Claude Code、Pi、Kiro 和 OpenClaw 可以互相交接工作，
不用手动复制长 transcript，也不用把本地 session history 上传到云端服务。

当一个 agent 额度用完、卡住，或者不适合下一步时，`paxl` 可以保留当前工作上下文，
再把它投递给另一个本地 agent session。

英文文档：[../README.md](../README.md)

架构文档：[ARCHITECTURE.md](ARCHITECTURE.md)

## 安装

```sh
curl -fsSL https://api.paxtech.net/api/v1/public/paxl/install.sh | bash
paxl version
```

从源码构建：

```sh
go build -trimpath -o ./paxl ./cmd/paxl
```

## 前五分钟

先看本机能连到哪些 agent：

```sh
paxl agent list
```

找到要迁移的 session：

```sh
paxl session list --agent claude --limit 5
paxl session list --agent codex --limit 5
```

把 Claude session 迁到已有 Codex session：

```sh
paxl session mirror \
  claude:<source-session-id> \
  --to-session codex:<target-session-id> \
  --verbose
```

也可以带着上下文开一个新的目标 session：

```sh
paxl session mirror claude:<source-session-id> --to codex
```

## 它转移什么

`paxl` 主要处理四类本地优先对象：

| 对象 | 什么时候用 | 示例 |
| --- | --- | --- |
| Session | 查看本地 agent 会话。 | `paxl session get claude:<id>` |
| Mirror | 让另一个 agent 立刻接手。 | `paxl session mirror claude:<id> --to codex` |
| Capsule | 把知识沉淀下来以后复用。 | `paxl capsule create codex:<id> --keyword "release plan"` |
| Envelope | 把 capsule 发给 accepted friend。 | `paxl capsule send <id> --to @alice` |

关键边界：本地查看就是本地查看。只有你显式 mirror、inject 或 send 时，选中的上下文才会被投递出去。

## 支持的 agents

| Agent | 本地 session | 投递方式 | Hook setup |
| --- | --- | --- | --- |
| Codex | 本地日志 | App server 或 `codex exec` | `UserPromptSubmit` |
| Claude Code | 本地日志 | `claude --print` | `UserPromptSubmit` |
| Pi | 本地日志 | Pi CLI | `before_agent_start` extension |
| Kiro | Kiro CLI 日志 | `kiro-cli chat` | Kiro `userPromptSubmit` agent hook |
| Hermes | 本地 descriptor host | Hook descriptor | Descriptor only |
| OpenClaw | ACP | ACP `session/prompt` | Descriptor only |

运行 `paxl agent list` 可以看到这些 agent 在本机是否可用。

Gemini CLI 支持已经下线。旧本地数据里的 `gemini` 值仍可被解析，但新的 CLI 入口会拒绝
继续使用 Gemini。

## Hook 注入

安装一次本地 hooks：

```sh
paxl setup
```

然后把 capsule 排队到下一次匹配的 prompt：

```sh
paxl capsule inject <capsule-id> --match keyword --keyword "release plan"
paxl capsule inject <capsule-id> --match project --project paxl --agent claude
```

隐藏 hook 入口会一次性领取匹配的 injection，把 capsule 渲染成 handoff，再通过对应 agent
的 native hook shape 交回去。

## 常用动作

切换 agent 前保存 timeline：

```sh
paxl session get claude:<session-id>
paxl session get codex:<session-id> --format jsonl
paxl session get codex:<session-id> --format html --output session.html
```

Transcript 适合直接阅读，JSONL 适合脚本处理，HTML 适合当作可携带的 review artifact。

沉淀可复用知识：

```sh
paxl capsule create claude:<session-id> --keyword "release plan"
paxl capsule get <capsule-id>
paxl capsule inject <capsule-id> codex:<target-session-id>
```

Capsule 适合保存架构决策、debug 历史、release checklist、项目约定这类不应该只留在一个
conversation 里的上下文。

发给 accepted friend：

```sh
paxl friend request alice@example.com --alias alice
# Alice 接受 friend request 后
paxl capsule send <capsule-id> --to @alice --message "please review"
```

转移人工整理好的上下文：

```sh
paxl capsule create --manual \
  --keyword "production incident" \
  --content "Prepared incident context..."
```

安装仓库里的 Codex skill 后，可以直接说“把这个上下文迁到 Claude”或“给这个 bug 建一个
capsule”，让 agent 去执行对应的 `paxl` 命令。

```sh
mkdir -p ~/.codex/skills
cp -R skills/knowledge-transfer ~/.codex/skills/
```

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

## 命令参考

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

如果要转发一条已经整理好的需求，可以直接通过 `--content` 创建 capsule：

```sh
paxl capsule create codex:<session-id> \
  --keyword "installer hosting" \
  --title "paxl installer hosting" \
  --summary "Installer upload and hosting requirement." \
  --content "The installer should be uploaded and hosted at GCS."
```

如果这条内容不需要绑定某个 source session，可以创建 manual capsule：

```sh
paxl capsule create --manual \
  --keyword "installer hosting" < capsule.md
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

`paxl setup` 默认会安装当前支持的 agent hook plumbing：Codex、Claude、Pi、
Kiro、Hermes 和 OpenClaw。Codex 和 Claude 会写入原生
`UserPromptSubmit` hook；Pi 会写入 `before_agent_start` extension；
其他 agent 会写入 paxl-owned descriptor，供对应 host/gateway 调用同一个隐藏入口。

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
- 条件 hook 注入：通过 Pi `before_agent_start` extension 在 agent loop 启动前
  返回一条 custom message。

Kiro 投递：

- 已有 session：`kiro-cli chat --resume-id <session-id> --no-interactive <message>`
- 新 session：`kiro-cli chat --no-interactive <message>`

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
目前内置支持 Codex、Claude、Pi、Kiro 和 OpenClaw。

## 平台支持边界

CLI 架构和 SQLite 存储本身是跨平台 Go 代码，但当前内置 adapters 依赖本地 agent
日志路径和 native CLI。

当前支持边界：

- macOS：已经用本地 Codex、Claude Code、Pi 和 Kiro CLI 日志形态验证过。
  OpenClaw 通过 ACP contract tests 覆盖，需要本机存在 OpenClaw ACP 命令。
- Linux：如果存在 `~/.codex/sessions`、`~/.claude/projects`、
  `~/.pi/agent/sessions`、`~/.kiro/sessions`，并且对应 CLI 在 `PATH` 中，
  理论上和 macOS 很接近，但还需要真实环境验证。
- Windows：还没有充分验证。路径处理、Claude project 目录名解码、fake command
  测试方式、native CLI resume 行为都需要单独覆盖。

简而言之：macOS 已验证，Linux 预计可用但需要验证，Windows 目前应视为 experimental。
