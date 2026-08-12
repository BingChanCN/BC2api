# Sub2API 全量 Fork 说明

本仓库为 **Wei-Shaw/sub2api** 的全量二开基线，用于自定义前端、业务逻辑与运营功能。

> **新成员请先读：** [`docs/FORK_DEVELOPMENT.md`](docs/FORK_DEVELOPMENT.md)  
> 内含：相对官方改了什么、分层落点、开发/测试/同步上游/热更新插件全流程。  
> 插件契约细节：[`docs/PLUGINS.md`](docs/PLUGINS.md)

## Remote

| 名称 | 用途 |
|------|------|
| `upstream` | 官方上游 `https://github.com/Wei-Shaw/sub2api.git`（只读同步） |
| `origin` | 自有 fork：`git@github.com:BingChanCN/BC2api.git` |

```bash
# 首次推送二开主线
git push -u origin fork/main
# 若需默认分支也叫 main：推送后在 GitHub 设 default branch，或
# git push origin fork/main:main
```

## 分支策略

| 分支 | 用途 |
|------|------|
| `fork/main` | 二开主线（日常开发、自定义功能） |
| `upstream/main` | 跟踪官方（`git fetch upstream`） |

### 同步上游

```bash
git fetch upstream
git checkout fork/main
# 推荐 rebase 保持历史干净；冲突多时用 merge
git merge upstream/main
# 或: git rebase upstream/main
```

### 建议隔离自定义改动

- 自定义功能尽量落在独立目录/文件，减少与上游同文件大面积冲突  
  - 前端：`frontend/src/features/custom/**`、`frontend/src/views/custom/**`
  - 后端：`backend/internal/custom/**` 或明确前缀 `custom_*.go`
- 上游大版本合并前先在临时分支试合：`git checkout -b sync/upstream-YYYYMMDD`

## 构建

```bash
# 前端
cd frontend && pnpm install && pnpm build

# 后端（嵌入前端）
cd backend
go build -tags embed -ldflags="-X main.Version=fork-dev" -o sub2api ./cmd/server
```

## 插件系统

fork 已内置 **外部 HTTP 插件运行时**（见 `docs/PLUGINS.md`）：

- 插件目录：`/app/data/plugins/<id>/manifest.json`
- 静态示例：`examples/plugins/reaction-grid`
- 管理页：`/admin/plugins`
- 之后加运营页/小游戏/独立业务 API：**优先写插件**，不要改网关/计费核心
- 自定义代码若必须进主仓，仍建议落在：
  - 前端 `frontend/src/features/custom/**`、`frontend/src/views/custom/**`
  - 后端 `backend/internal/custom/**`

## 范围约定

- **可二开**：前端 UI、自定义页面/小游戏、新 API、运营逻辑、主题与信息架构
- **优先插件**：能做成外部插件热加载的，不进主镜像发版
- **慎改/少改**：gateway 转发、计费、调度核心（合并上游成本极高）
- **优先配置**：能用 Settings / OEM / Admin API 解决的，不写进 fork 核心
