# Sub2API 全量 Fork 说明

本仓库为 **Wei-Shaw/sub2api** 的全量二开基线，用于自定义前端、业务逻辑与运营功能。

## Remote

| 名称 | 用途 |
|------|------|
| `upstream` | 官方上游 `https://github.com/Wei-Shaw/sub2api.git`（只读同步） |
| `origin` | 自有 fork 远程（创建 GitHub 仓库后绑定） |

```bash
# 绑定自有远程（示例）
git remote add origin https://github.com/<you>/<your-sub2api>.git
git push -u origin fork/main
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

## 范围约定

- **可二开**：前端 UI、自定义页面/小游戏、新 API、运营逻辑、主题与信息架构
- **慎改/少改**：gateway 转发、计费、调度核心（合并上游成本极高）
- **优先配置**：能用 Settings / OEM / Admin API 解决的，不写进 fork 核心
