# claude-toolkit 路线图（合并版 v1）

> 状态：草案，等待评审。
> 文档依据：Claude Code 官方 hook 文档（https://code.claude.com/docs/en/hooks），所有 API 调用均经过逐项核对。
> 架构原则：把「确定性规则 / 自动修复 / Token 瘦身」从大模型注意力机制中剥离，下沉到本地 Go 确定性代码层。

---

## 一、设计原则（合并自两版计划）

1. **单入口 Dispatcher**。`settings.json` 中每个事件只配置一个 hook 命令（`claude-toolkit run --event=...`），所有路由收敛在二进制内部的 dispatcher。
2. **按 capability 组织，不是按事件**。一个 capability（`guard` / `heal` / `enrich` / `truncate` / `format`）可监听多个事件，但只占用 settings.json 中的一条配置。
3. **fail-open**。Hook 解析失败或超时时永远返回退出码 0 且不阻断；只在规则明确命中时输出 JSON 拒绝。
4. **零依赖**。Go 1.25 stdlib 单二进制发布。Python 仅作为可选辅助脚本（按需捆绑，不进默认 release）。
5. **本地私有空间与 Claude Code 命名空间严格分离**。Toolkit 的私有资产在 `~/.claude-toolkit/`，只对 `~/.claude/settings.json` 写入合并后的 hook 配置。

---

## 二、当前架构（v0.1，已落地）

```
~/.claude-toolkit/                      ← toolkit 私有
└── bin/claude-toolkit                  ← 单二进制

~/.claude/settings.json                 ← Claude Code 命名空间（合并）
└── hooks:
    ├── PreToolUse:    [matcher=^(Bash|Write|Edit|NotebookEdit)$, cmd=claude-toolkit --event=pre]
    ├── PostToolUse:   [matcher=^(Write|Edit|NotebookEdit)$,       cmd=claude-toolkit --event=post]
    └── SessionStart:  [matcher=*,                                  cmd=claude-toolkit --event=session]

internal/
├── dispatcher/        路由引擎（按 Event 索引，按 Tool 过滤，merge 优先级 deny > ask > allow）
├── hooks/             三个 capability
│   ├── guard.go       PreToolUse 安全守门（rm-rf、pipe-to-shell、secret-exfil、dd、fork-bomb、force-push）
│   ├── format.go      PostToolUse 自动格式化（gofmt/rustfmt/shfmt/ruff/black/prettier）
│   └── context.go     SessionStart 注入 git 状态（branch + porcelain + last 5 commits + ahead/behind）
├── payload/           hook stdin/stdout 的强类型解码与构造
pkg/
├── dir/               ~/.claude-toolkit/ 路径解析
└── installer/         settings.json 的非破坏性合并器
cmd/
├── run.go             hook 入口（stdin → dispatcher → stdout）
├── init.go            安装 / 卸载 hook 配置
└── doctor.go          自检（binary / settings / registration / 自测 / 依赖）
```

**v0.1 已知限制**（文档核对中暴露）：

| 项 | 当前 | 应改为 | 影响 |
|---|---|---|---|
| `permissionDecision` | 只识别 `allow/deny/ask` | 加 `defer` | 阻塞某些工作流 |
| `SessionStart` 输出 | 无截断 | 10000 字符硬限 | 静默丢失 |
| `cwd` 来源 | stdin JSON 的 `cwd` | 优先 `$CLAUDE_PROJECT_DIR` env | SessionEnd 等事件 `cwd` 可能为空 |
| `Merge` 在并行 hook 下 | 单二进制内有效 | 用户多 hook 场景无解 | 用户装多个工具时可能行为异常 |

---

## 三、能力矩阵（合并后）

按「确定性下沉」原则，分四象限：

|             | PreToolUse（守门）                  | PostToolUse（自愈）                | SessionStart / UserPromptSubmit（环境感知） |
|-------------|--------------------------------------|--------------------------------------|---------------------------------------------|
| **确定性**  | `guard` 阻断危险命令                 | `heal` 自动修复 + 增量测试           | `enrich` 注入环境摘要                       |
| **Token 瘦身** | `truncate` Bash 输出截断         | `format` 格式化避免 Edit 失配        | （同 `enrich`）                              |
| **确定性安全** | `guard` + 分支防护 + 高熵凭证    | `heal` 增量测试回灌                  | —                                           |
| **可观测**  | `log` 记录命中的规则                 | `log` 记录修复动作                   | `log` 记录注入上下文大小                     |

---

## 四、路线图（按时间盒）

### 阶段 0：架构加固（1 周）

> 目标：把 v0.1 已知限制清掉，建立稳定的扩展面。

- [x] **增加 `permissionDecision: defer`** 支持（payload.Response 加构造器）
- [x] **`SessionStart` 输出 10000 字符硬截断**（保留头部 + 可见截断标记）
- [x] **cwd 来源切换**：dispatcher 优先 `$CLAUDE_PROJECT_DIR`，fallback 到 stdin JSON
- [x] **Dispatcher 暴露 hook 超时剩余时间**（`ctx.Done()` 给 handler 看是否快超时，便于降级）
- [x] **新增 `StopFailure` / `WorktreeCreate` 事件常量**（不在 dispatcher 注册，留接口）

> 已于 2026-08-03 落地。附带完成：`manage` 子命令（capability 开关 + 终端 TUI）、Claude Code plugin（`/toolkit`，见仓库根 `.claude-plugin/` 与 `commands/`）。

**验收**：`go test` 通过；`doctor` 增加 2 个新用例（cwd 来源、输出截断）。

---

### 阶段 1：`heal` capability（2 周）—— *核心价值*

> 把 PostToolUse 从「格式化」升级为「自动修复 + 验证」闭环。

- [ ] **`pkg/healer/` 子包**
  - 语言检测：`*.go` / `*.py` / `*.ts` 派发到对应 fixer
  - Go：检测到 import 缺失 / 未使用时跑 `goimports -w`（不动磁盘外的内容）
  - Python：检测到 `unused-import / undefined-name` 时跑 `ruff check --fix --select F401,F821`
  - TypeScript / JS：暂不内置修复器（生态太杂），仅跑 prettier
- [ ] **增量测试 runner**
  - 解析 Edit/Write 的目标文件路径 → 推断对应 `_test.go` / `test_*.py`
  - 跑 `go test -run <TestName> -count=1` 或 `pytest -x -q <file>::<test>`
  - 失败时构造 `additionalContext`：只保留 Top-5 错误栈，去掉 framework banner
  - 通过 `decision: block`（PostToolUse 顶级字段）让 Claude 必须看到失败
- [ ] **日志截断器（`truncate` capability 前置）**
  - 拦截 `go test` / `pytest` / `npm test` 的输出
  - 保留 fail 行 + 前后 3 行 context，丢弃 info 噪音
  - 写回 stderr，让 Claude 看到精简版

**架构约束**：healer 不通过 hook 自身跑测试 —— 改用 PostToolUse **触发一个独立的 `claude-toolkit test <file>` 子命令**，避免 hook 超时（默认 30s 跑不完单测）。

**验收**：在 fixture 仓库中故意引入 import 缺失 → Edit 后 hook 自动修复 → 测试运行 → 失败回灌；token 用量比手工让 Claude 修下降 70%。

---

### 阶段 2：守门加固（1.5 周）—— *确定性安全*

- [ ] **`guard` 增加 Git 分支防护**
  - `git commit` / `git push` 时解析当前分支
  - 黑名单：`main` / `master` / `release/*` / `production`
  - 白名单机制（项目根 `.claude-toolkit-allow` 文件）
- [ ] **`guard` 增加高熵凭证正则**
  - 在 `Write` / `Edit` 的 `content` / `new_string` 字段上跑 entropy 检测
  - Shannon 熵 > 4.5 + base64/hex 模式 → deny，附「疑似凭证，请人工确认」
  - 与现有的路径白名单并存（不互斥）
- [ ] **`guard` 日志/dump 截断**
  - Bash 输入里出现 `cat /var/log/...` / `journalctl` / `kubectl logs` 时
  - 建议改为 `tail -100` / `--since=10m`，或直接 deny

**验收**：尝试在 main 分支 commit → 拒绝；尝试写入含 AWS key 字符串的文件 → 拒绝；尝试 cat 巨型日志 → 建议替代命令。

---

### 阶段 3：环境探针升级（1 周）—— *Token 瘦身*

- [ ] **`enrich` 扩展 SessionStart 注入内容**
  - Go 版本 / Python 版本 / Node 版本（`go version` / `python --version` / `node --version`）
  - 当前 venv / conda env 名（`$VIRTUAL_ENV` / `conda info --json`）
  - 数据库连通状态（配置驱动：`~/.claude-toolkit/config.yaml` 列数据库连接）
  - 当前 dirty 文件列表（已有）
  - 包管理器版本（`pnpm` / `yarn` / `bun`）
- [ ] **DB Schema 挂载（lazy 模式）**
  - 默认只挂「schema 摘要 + 表名列表」，最多 50 张
  - 具体表结构等 Claude 第一次 query 该表时按需注入
- [ ] **`enrich` 注册 UserPromptSubmit**
  - 每次用户提问前再注入一次 `cwd` + 简短 dirty 状态
  - Claude 长时间运行后能知道 cwd 漂移

**验收**：SessionStart 注入长度 < 2000 字符，包含全部上述字段。

---

### 阶段 4：CLI 工具集（1.5 周）—— *Token 瘦身*

- [ ] **`claude-toolkit summary <path>`**
  - Go 路径：用 `go/parser` + `go/ast` 抽取 Package / Struct / Interface / Function 签名
  - Python 路径：调外部 `python3 -m toolkit_ast <path>`（可选脚本，stdlib `ast` 实现）
  - 输出 JSON / YAML，密度比 `cat *.go` 高一个数量级
- [ ] **`claude-toolkit rules`** 列出 / 解释所有内置规则
- [ ] **`claude-toolkit replay <event.json>`** 重放历史事件（调试 hook 用）

**验收**：`summary .` 对一个 1 万行 Go 仓库输出 < 50 KB，包含所有导出符号。

---

### 阶段 5：生命周期补完（1 周）—— *我之前计划中的保留项*

- [ ] **`claude-toolkit upgrade [--check-only] [--force]`**
  - 调 GitHub Releases API 比 `tag_name`
  - 走 `install.sh` 相同的下载 + SHA-256 + 原子替换
- [ ] **`claude-toolkit uninstall [--scope=...] [--purge-config]`**
  - 对称于 `init`，但 `init --uninstall` 仍保留兼容
- [ ] **`claude-toolkit log --follow --since=10m --event=pre`**
  - 当前已有 `~/.claude/claude-toolkit.log` 文件但无查看器
- [ ] **所有写操作支持 `--dry-run`**（`init` 已有，`upgrade` / `uninstall` 跟进）

---

### 阶段 6：分发与跨平台（1 周）—— *上线硬要求*

- [ ] **Windows 端到端 CI**
  - 当前 CI matrix 有 windows 但无 e2e
  - 加 job：`install.ps1` → `init` → `doctor` → `uninstall`
- [ ] **`install.ps1`**（或在 README 中给 `irm | iex` 一行命令）
- [ ] **Homebrew tap**（`brew install zealot00/tap/claude-toolkit`）
- [ ] **Scoop manifest**

---

### 阶段 7：Git 本地钩子（0.5 周）—— *你提案中明确、但属于开发者本地*

- [ ] **`claude-toolkit hook install-git`**
  - 把 `guard` 的核心规则写入 `.git/hooks/pre-commit`
  - 复用 `internal/hooks/guard.go`，不重新实现
- [ ] **`claude-toolkit hook uninstall-git`**

---

## 五、阶段间依赖关系

```
阶段 0 ──► 阶段 1 ──┬──► 阶段 3
              │      └──► 阶段 4
              └──► 阶段 2
                              │
                              ▼
                          阶段 5
                              │
                              ▼
                          阶段 6
                              │
                              ▼
                          阶段 7
```

阶段 0 是地基。阶段 1（heal）和 阶段 2（守门加固）可并行。阶段 3/4 在阶段 1 落地后做集成。5/6/7 是上线收尾。

---

## 六、不做的（明确排除）

| 项 | 不做的原因 |
|---|---|
| 持久化 daemon | Claude Code 自己拉 hook 进程足够频繁；常驻增加安装摩擦 |
| 远程配置同步 | 隐私敏感，且违反「本地确定性」原则 |
| 多 hook 编排 GUI | settings.json 已经够简单；CLI 工具不需要 GUI |
| 远程 hook（`http` 类型） | 增加攻击面；本地命令足够 |
| 把 Claude Code 主进程替换 | 不可行也不必要 |
| Python 作为主语言 | 依赖管理混乱；保持 Go 单二进制 |
| 内置任意 LLM 调用 | 违反「确定性下沉」原则 |

---

## 七、风险与待验证项

| 风险 | 触发条件 | 缓解 |
|---|---|---|
| Claude Code hook API 变更 | 官方升级 | dispatcher 抽象层隔离；`go test ./internal/dispatcher` 锁住行为 |
| Go 1.25 stdlib 变更 | 语言升级 | 锁版本；CI 跑 go1.25 / go1.26 |
| 用户装了多个 hook 工具 | 并行 hook 行为不可控 | 文档明示 dispatcher 行为；建议只装一个分发器 |
| Mac GUI 启动 Claude Code 不带 PATH | `claude-toolkit` 找不到 | `init` 默认要求 PATH，`--abs-path` 兜底；`doctor` 检查 |
| `goimports` / `ruff` 不在用户机器 | heal 降级 | `doctor` 检查并 warn；formatter 早已支持降级 |

---

## 八、API 已验证项（来自官方文档核对）

> 来源：https://code.claude.com/docs/en/hooks，2026-08-03 核对

### 事件清单（已验证存在）

```
PreToolUse, PostToolUse, PostToolUseFailure,
UserPromptSubmit, UserPromptExpansion,
Notification, Stop, StopFailure, SubagentStop,
PreCompact, ConfigChange,
SessionStart, SessionEnd,
WorktreeCreate, WorktreeRemove
```

### 输出字段（已验证）

| 字段 | 位置 | 适用事件 | 行为 |
|---|---|---|---|
| `continue` | 顶级 | 全部 | `false` = Claude 停止处理 |
| `stopReason` | 顶级 | 全部 | `continue: false` 时显示 |
| `suppressOutput` | 顶级 | 全部 | 隐藏 stdout |
| `systemMessage` | 顶级 | 全部 | 给用户看的警告 |
| `decision: block` | 顶级 | PostToolUse / PostToolUseFailure / UserPromptSubmit / Stop / SubagentStop / PreCompact | 阻断 |
| `hookSpecificOutput.additionalContext` | 嵌套 | SessionStart / Setup / SubagentStart 等 | 给 Claude 看 |
| `hookSpecificOutput.permissionDecision` | 嵌套 | PreToolUse | `allow`/`deny`/`ask`/`defer` |
| `hookSpecificOutput.permissionDecisionReason` | 嵌套 | PreToolUse | 解释 |
| `hookSpecificOutput.updatedInput` | 嵌套 | PreToolUse | 修改工具输入 |
| `hookSpecificOutput.updatedToolOutput` | 嵌套 | PostToolUse | 修改工具返回 |
| `hookSpecificOutput.initialUserMessage` | 嵌套 | SessionStart | 注入初始消息 |
| `hookSpecificOutput.watchPaths` | 嵌套 | SessionStart | 触发自动 reload |
| `hookSpecificOutput.sessionTitle` | 嵌套 | SessionStart | 重命名会话 |

### Matcher 语义（已验证）

- 实现：JavaScript `RegExp.prototype.test`
- **非锚定** —— `"Bash"` 会匹配 `"BashOutput"`
- 空字符串 / `"*"` / 缺失 = 匹配所有
- 多 hook 对同一 matcher：**并行运行**
- 重复 hook：**自动去重**

### 退出码（已验证）

- 退出 0 + JSON stdout = 标准路径
- 退出 2 = 阻塞（仅 PreToolUse / UserPromptSubmit 等明确支持的事件）
  - stderr 进 Claude 视野作为错误消息
- 退出 1 = 非阻塞错误，继续执行
- 其他非零 = 大多数事件非阻塞；**仅 WorktreeCreate 例外**

### 环境变量（已验证存在）

```
$CLAUDE_PROJECT_DIR          项目根
$CLAUDE_EFFORT               effort 级别
$CLAUDE_CODE_REMOTE          远程 web 环境时 "true"
$CLAUDE_CODE_BRIDGE_SESSION_ID  Remote Control 会话 ID
$CLAUDE_PLUGIN_ROOT          插件 hooks
$CLAUDE_PLUGIN_DATA
$CLAUDE_PLUGIN_OPTION_<KEY>
```

`OTEL_*` 被剥离。`CLAUDE_SESSION_ID` 在 hook 的 stdin JSON 中，**不是**环境变量。

### 容量限制

- 输出字符串硬限：10,000 字符（超出落盘 + preview）

---

## 九、评审请求（致评审者）

请重点评估：

1. **优先级**：阶段 0-7 的顺序是否符合实际使用场景的 ROI？
2. **`heal` 的边界**：阶段 1 把增量测试放在 hook 里是否过重？是否应该走独立子命令而非 hook 内 subprocess？
3. **DB Schema 挂载**：阶段 3 的 lazy 模式是否足够？是否需要彻底砍掉以避免 Token 风险？
4. **Git pre-commit 钩子**（阶段 7）：是否值得做？它和 `guard` 有功能重叠。
5. **不支持的特性**（第六节）：是否漏掉你认为必须的？
6. **风险项**（第七节）：是否有未识别的风险？

也欢迎指出你认为：

- 实现成本被低估的项（尤其是 heal 的语言检测）
- 实现成本被高估的项
- 路线图中缺失的能力
