# claude-toolkit 最终实施计划书（v2）

> 定位：可执行的最终计划。综合了任务计划书（Epic 1–6）、现有 v0.1 实现基线、以及对照 Claude Code 官方 hooks 规范逐项核验后的裁决。
> 文档依据：Claude Code hooks 官方 skill / CHANGELOG（至 2.1.220）/ issue #79321、#81340、#83353、#77851。
> 一句话原则：**"纯 Go 单二进制"管的是怎么实现；`.py`/`.ts` 文件管的是处理什么；外部 CLI 只是"可选调用、缺省静默降级"。**

---

## 一、硬性约束（不可违反）

| # | 约束 | 依据 |
|---|---|---|
| 1 | 单二进制、纯 stdlib、零第三方 Go 依赖 | 已达成（`go.mod` 仅 stdlib） |
| 2 | 不捆绑、不要求任何外部运行时 | 外部工具一律 `PATH` 探测 + 静默降级（详见第八节契约） |
| 3 | 拒绝走官方 hook 通道：`exit 0 + JSON permissionDecision: deny`；**绝不用 exit 1 表达拒绝** | 官方规范：exit 2 = block，exit 1 = 非阻塞错误（stderr 进 transcript，工具照跑）→ 用 exit 1 拒绝 = 拦截成功率 0% |
| 4 | `settings.json` 是用户财产：只合并 hooks，不注入 env，不覆盖用户键 | 现有 `installer` 三原则（备份 / 拒绝损坏文件 / 原子写） |
| 5 | 不引入常驻进程作为默认路径 | 代理只能是显式启动的可选模块 |
| 6 | 不依赖 `updatedInput` 做关键能力 | ≤2.1.220 存在已确认的静默丢弃回归（#79321 Windows / #81340 macOS）+ 多 hook 时序竞争（#83353） |
| 7 | 不在 hook 内跑长任务（测试） | command hook 默认超时 ~60s；`go test` 冷启动易超时 → 测试走独立子命令 |

---

## 二、现状基线（已交付，本计划书在其上增量）

- **hooks**：`guard`（PreToolUse，12 条规则）/ `format`（PostToolUse，gofmt/rustfmt/shfmt/prettier/ruff/black）/ `enrich`（SessionStart，git 状态）
- **CLI**：`init`（自举安装，幂等）/ `manage`（capability 开关：list/enable/disable/enable-all/disable-all + 终端 TUI）/ `doctor`（自检含 selftest）/ `run`（hook 运行时，fail-open）
- **阶段 0 加固**（已完成）：`permissionDecision: defer` 构造器、SessionStart 输出 10000 字符硬截断、cwd 来源优先 `$CLAUDE_PROJECT_DIR`、`dispatcher.Remaining()` 超时暴露、`StopFailure`/`WorktreeCreate`/`PostToolUseFailure` 预留事件常量
- **plugin**：`.claude-plugin/plugin.json` + `commands/toolkit.md`（`/toolkit` slash command，驱动 `manage`），plugin 自身零 hook、零依赖（README 已声明）
- **文档**：README 含 `## Dependencies` 章节（外部工具可选、降级契约、市场评审声明）
- 测试基线：`go test -race ./...` 全绿；`gofmt`/`vet` 干净

---

## 三、阶段 1：`heal` —— PostToolUse 自愈 + 增量测试（核心价值，2–3 周）

> 现有 `format` 只做格式化；本阶段升级为"格式化 + 自动修复 + 测试闭环"。

### 1.1 Go 修复器（扩展 `format.go` 或新建 `post/go_healer.go`）
- 优先级改为 **`goimports -w` → fallback `gofmt -w`**（现状是 gofmt 优先；goimports 处理缺失/未排序 import，gofmt 只格式化）
- 静默成功，仅在文件实际变化时通过 `additionalContext` 提示 Claude 重读（沿用现有 `format` 逻辑）

### 1.2 Python 修复器
- `ruff format` + `ruff check --fix --select F401,F821` → fallback `black -q`
- 与 1.1 同一降级契约

### 1.3 增量测试 —— **独立子命令，不在 hook 内执行**（对任务计划书 Task 3.2 的修正）
- 新增 `claude-toolkit test <file>`：解析目标文件 → 推断 `_test.go` / `test_*.py` / `tests/test_*.py` → `go test -run <TestName> -count=1` 或 `pytest <file> --maxfail=1 --tb=short`
- PostToolUse hook 只做**探测**（识别测试目标是否存在），通过 `additionalContext` 提示 Claude 调用 `claude-toolkit test`；失败结果经 `decision: block` 回灌
- 理由：hook 超时 ~60s 会杀测试进程；独立子命令不受限

### 1.4 日志截断器（`truncate` 前置）
- 拦截 `go test`/`pytest`/`npm test` 输出，保留 fail 行 + 前后 3 行 context，丢弃 banner 噪音；截断到 35 行内（任务计划书要求）

**验收门**
- [ ] 故意引入缺失 import → Edit 后 hook 自动修复（goimports 或 gofmt）→ 测试运行 → 失败回灌
- [ ] `go test -race ./...` 在 fixture 仓库通过
- [ ] `claude-toolkit test` 单测执行时间不受 hook 超时约束（可 >60s）
- [ ] 长输出测试失败日志截断 ≤35 行

---

## 四、阶段 2：guard 加固（1.5–2 周）

### 2.1 死循环检测（对任务计划书 Task 2.2 的修正）
- **PostToolUse 记录**退出码 → **PreToolUse 查询**：同一命令连续失败 ≥3 次（含中间无代码变更）→ deny
- 状态文件放 `~/.claude-toolkit/state/bash_history.json`（0700，`pkg/dir` 已预留），**不用 /tmp**（1777 可被他人读、多会话并发写损坏）
- 理由：PreToolUse 时看不到退出码（退出码在执行后才产生），计划书的"单 hook ring-buffer"方案在 PreToolUse 侧无法判定失败

### 2.2 Git 分支防护
- `git commit`/`git push` 解析当前分支；黑名单 `main`/`master`/`release/*`/`production`；项目根 `.claude-toolkit-allow` 白名单

### 2.3 高熵凭证检测
- `Write`/`Edit` 的 `content`/`new_string` 上 Shannon 熵 > 4.5 + base64/hex 形态 → deny（附"疑似凭证"提示）；与现有路径白名单并存

### 2.4 补充规则
- `git reset --hard`（任务计划书要求）加入 deny 表；日志类 `cat /var/log/...`/`journalctl` → 建议 `tail -100`/`--since=10m`

**验收门**
- [ ] 同一失败命令连续 3 次被拦；`guard_test` 保持零新增误报（现有测试表是误报防线）
- [ ] main 分支 commit/push 被拦；白名单文件生效
- [ ] 写入含 AWS key 字符串的文件被拦

---

## 五、阶段 3：enrich 升级（1 周）

- SessionStart 注入扩展：`go version` / `python --version` / venv 路径（`$VIRTUAL_ENV`）/ 包管理器版本
- 注册 `UserPromptSubmit`：每次提问前注入 `cwd` + 简短 dirty 状态
- **环境改写（updatedInput）明确否决**，用只读注入替代：让 Claude 自己用 `./.venv/bin/pytest`（效果一致，零静默失败路径）

**验收门**
- [ ] SessionStart 注入 <2000 字符，含全部字段
- [ ] 受 10000 字符硬截断约束（已实现）

---

## 六、阶段 4：AST 摘要 CLI（1–1.5 周）

- `claude-toolkit ast <path>`（采纳任务计划书命名；ROADMAP 的 `summary` 并入）
  - Go：`go/parser` + `go/ast` 抽取 Package / Struct / Interface / Function 签名
  - Python：**纯 Go regex** 提取**顶层** `def`/`class` 签名，不处理嵌套/decorator 多行参数（边界写死在文档，避免准确率承诺过度）
  - 输出 JSON（不含函数体），供 Claude 低 token 读取
- 附带 `claude-toolkit rules`（列出/解释全部内置规则）

**验收门**
- [ ] `ast main.go` 返回合法压缩 JSON，无函数体
- [ ] `ast script.py` 提取顶层 class/function 签名，全程不调用 Python
- [ ] 1 万行 Go 仓库摘要 <50 KB

---

## 七、阶段 5–7：通知、代理、生命周期（各 ≤1 周，可并行）

### 7.1 OS 桌面通知（**可选、默认关**，对任务计划书 Task 4.2 的调整）
- PreToolUse 记时间戳 → PostToolUse 对比墙钟差；超阈值（默认关，开启时默认 60s）或失败 → `osascript`（macOS）/`notify-send`（Linux）+ 终端 `\a`
- 理由：PostToolUse payload 无执行耗时字段（官方未提供，#77851）；10s 阈值对 `go test` 太吵

### 7.2 本地代理（**独立可选模块**，对 Epic 5 的降级）
- `claude-toolkit proxy` 显式启动（127.0.0.1:8080 → api.anthropic.com），429 指数退避 + jitter 重试 ≤4 次
- **不自动注入 `ANTHROPIC_BASE_URL`**：用户自行承担单点故障；README 明示"代理挂 = Claude Code 不可用"
- Token HUD **不依赖代理**：会话内 `/cost` 已可查；如需注入，走 UserPromptSubmit/PreCompact 简短水位

### 7.3 生命周期补完
- `upgrade [--check-only]`、`uninstall [--purge-config]`、`log --follow`；所有写操作支持 `--dry-run`（init/manage 已有）

### 7.4 分发
- Windows e2e CI（install.ps1）、Homebrew tap、Scoop manifest；plugin 随归档发布（已就位）

---

## 八、外部依赖契约（社区/市场评审红线）

| 工具 | 用途 | 缺失行为 |
|---|---|---|
| `git` | enrich | context 为空 |
| `gofmt`/`goimports` | Go 格式化/修复 | 不格式化 |
| `rustfmt`/`shfmt` | Rust/Shell 格式化 | 不格式化 |
| `prettier` | Web/配置格式化 | 不格式化 |
| `ruff`/`black` | Python 格式化/修复 | ruff 缺→black；black 缺→跳过 |
| `pytest` | Python 增量测试（阶段 1.3） | test 命令报告缺口 |

- 全部：`PATH` 运行时探测 + **静默降级为 no-op**；`doctor` 的 `formatters`/`git` 检查项列出本机情况
- 契约已写入 README `## Dependencies`、`scripts/install.sh` 注释与完成提示、`commands/toolkit.md`

---

## 九、明确否决清单（含原因）

| 项 | 否决原因（量化） |
|---|---|
| exit 1 + stderr 表达拒绝 | 官方语义：exit 1 = 非阻塞错误，工具照常执行 → 拦截成功率 0% |
| `init` 自动注入 `ANTHROPIC_BASE_URL` | 代理进程挂掉 → Claude Code 可用性损失 100%；以 100% 可用性风险换小概率 429 优化 |
| `updatedInput` 重写 Bash 命令 | ≤2.1.220 有已确认静默丢弃回归（#79321/#81340）+ 多 hook "最后完成者赢" 时序竞争（#83353） |
| hook 内跑 `go test`/`pytest` | 默认超时 ~60s 会杀测试进程；改独立子命令 |
| 常驻 daemon 作为默认路径 | 与"按需调用、零延迟"原则冲突 |

---

## 十、风险与缓解

| 风险 | 缓解 |
|---|---|
| hook API 变更 | dispatcher 抽象层；`go test ./internal/dispatcher` 锁行为 |
| 死循环检测误拦（用户故意重试） | 3 次阈值 + 仅"连续失败且无代码变更" + 可 `disable` |
| 高熵检测误伤（正常 base64 内容） | 熵 >4.5 且匹配 key 形态才触发；doctor selftest 覆盖 |
| goimports/ruff 缺失 | 降级链已有（goimports→gofmt；ruff→black） |
| 代理单点故障 | 显式启动 + README 明示，不注入 env |

---

## 十一、执行顺序与依赖

```
阶段 1 (heal) ──► 阶段 4 (ast)
      │              ▲
      ▼              │
阶段 2 (guard) ──► 阶段 3 (enrich)
      │
      ▼
阶段 5/6/7（通知/代理/生命周期，可并行）
```

阶段 1 是唯一拉开与社区方案差距的能力，优先。阶段 2/3 在 1 之后做集成。每阶段完成跑 `go test -race ./...` + `make check`（vet/gofmt/shellcheck）+ doctor selftest 作为回归门。
