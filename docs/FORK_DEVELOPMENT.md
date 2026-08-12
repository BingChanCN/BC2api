# BC2api / Sub2API Fork 全修改与开发流程

> 给新成员：本文说明 **相对官方 Wei-Shaw/sub2api 我们改了什么、为什么改、以后怎么加功能、怎么同步上游、怎么验证**。  
> 读完应能独立：拉代码 → 判断改动落点 → 开发 → 测 → 部署/热更新插件。

---

## 1. 仓库身份

| 项 | 值 |
|----|----|
| 本地路径 | `D:/VPSManager/sub2api`（或你的 clone 路径） |
| 官方上游 | `Wei-Shaw/sub2api`（remote: `upstream`） |
| 自有 fork | `BingChanCN/BC2api`（remote: `origin`） |
| 二开主线 | `fork/main`（跟踪 `origin/fork/main`） |

```bash
git remote -v
# origin    git@github.com:BingChanCN/BC2api.git
# upstream  https://github.com/Wei-Shaw/sub2api.git   # 或等价 SSH

git branch -vv
# * fork/main ... [origin/fork/main]
```

**原则**：日常开发只在 `fork/main`（或从它拉出的 feature 分支）。不要在 `main`/`upstream/main` 上直接写业务。

---

## 2. 为什么全量 Fork

官方客制化边界（OEM / 运营配置）适合「换皮 + 改规则 + iframe 嵌页」，**不适合**：

- 不改代码加新模型厂 / 新协议
- 深度业务钩子、入库小游戏、新管理页
- 不重建容器也能反复加功能

因此选择 **全量 fork**，并在 fork 上搭 **外部插件运行时**，把「以后常变的功能」尽量赶到插件目录，而不是每次改主镜像。

相关文档：

| 文档 | 内容 |
|------|------|
| `FORK.md` | 分支 / remote 速查 |
| `docs/FORK_DEVELOPMENT.md` | **本文：全流程与改动清单** |
| `docs/PLUGINS.md` | 插件契约、安全边界、部署细节 |
| `examples/plugins/` | 可复制的示例插件 |

---

## 3. 架构分层：先选落点，再写代码

新需求先按下面顺序选型，**能往上走就不往下走**。

```text
L0  后台配置 / OEM
    site_name、logo、home_content、custom_menu_items、支付/OAuth/限流…
    → 不改代码

L1  外部插件（推荐）
    运营页、小游戏、独立业务 API、管理小工具
    → 写 /app/data/plugins/<id>/，不重建主容器

L2  主仓前端二开
    必须共用主站路由/布局/登录态组件时
    → frontend/src/features/custom/**、views/custom/**

L3  主仓后端二开
    新一等 API、必须进主库的表/逻辑
    → backend/internal/custom/** 或独立 package

L4  核心改动（慎用）
    gateway / 计费 / 调度 / 账号平台枚举
    → 合并上游成本极高，需明确评审
```

### 3.1 插件系统一句话

插件是 **独立目录（可选独立容器）**；主服务只做：

1. 扫描 `manifest.json`（热刷新）
2. 聚合侧栏菜单
3. 托管/代理插件 UI（沙箱 iframe）
4. 登录用户通过受控 `invoke` 调插件 API（替换身份，不转发 JWT/Cookie）

**不是** Go `.so` 热加载，**不能**在主进程执行插件代码，**不能**直连主库。

---

## 4. 相对官方的实际改动清单

以下是 fork 相对上游 **已落地/进行中的二开面**（插件基座）。  
新成员合并冲突或 code review 时优先看这些路径。

### 4.1 新增（独立文件，与上游冲突概率低）

| 路径 | 作用 |
|------|------|
| `backend/internal/plugin/manifest.go` | 清单 schema、校验、公开 DTO |
| `backend/internal/plugin/registry.go` | 目录扫描、热刷新、角色过滤、诊断 |
| `backend/internal/plugin/runtime.go` | UI 静态/代理、API invoke、响应封装；CSP `connect-src 'self'`（Phaser Loader 同源 XHR） |
| `backend/internal/plugin/runtime_test.go` | 热刷新 / 静态隔离 / 凭证替换测试 |
| `backend/internal/server/routes/plugins.go` | 路由注册 |
| `frontend/src/api/plugins.ts` | 列表 / invoke / 管理诊断 API |
| `frontend/src/stores/plugins.ts` | 热列表 store + 轮询 |
| `frontend/src/stores/__tests__/plugins.spec.ts` | store 测试 |
| `frontend/src/features/plugins/bridge.ts` | iframe `postMessage` 协议解析 |
| `frontend/src/features/plugins/__tests__/bridge.spec.ts` | 桥协议测试 |
| `frontend/src/views/user/PluginView.vue` | 用户插件沙箱页 `/plugins/:id`（iframe `allow-fullscreen` + `allow="fullscreen"`） |
| `frontend/src/views/admin/PluginsView.vue` | 管理诊断页 `/admin/plugins` |
| `frontend/src/i18n/locales/{zh,en}/admin/plugins.ts` | 管理文案 |
| `docs/PLUGINS.md` | 插件契约 |
| `docs/FORK_DEVELOPMENT.md` | 本文 |
| `examples/plugins/reaction-grid/**` | 纯静态示例小游戏 |
| `examples/plugins/phaser-demo/**` | Phaser 4 静态游戏示例（`public/` 产物 + `game-src/` 源码） |
| `examples/plugins/coinflip/**` | 游戏账本示例（proxy 侧车 + stake/payout/refund） |
| `examples/plugins/README.md` | 示例说明 |
| `backend/internal/service/game_ledger.go` | 游戏账本：限额/幂等/动账/`type=game` 流水同事务 |
| `backend/internal/service/game_ledger_test.go` | 账本事务/重放/余额不足测试（sqlmock） |
| `backend/internal/repository/game_ledger_repo.go` | 事务内幂等表读写（`RETURNING` 避免 Exec 限制） |
| `backend/internal/handler/game_ledger_handler.go` | `POST /api/v1/internal/game/transactions`（Bearer=插件密钥） |
| `backend/internal/plugin/game_policy_test.go` | 游戏密钥鉴权/限额/清单校验测试 |
| `backend/migrations/221_user_group_rate_ceilings.sql` | 用户倍率上限表 |
| `backend/internal/repository/user_group_rate_ceiling_repo.go` | 上限仓储 |
| `backend/internal/service/user_group_rate_ceiling.go` | 上限比较/读写 |

### 4.2 修改（与上游同文件，同步时要人工看）

| 路径 | 改了什么 |
|------|----------|
| `backend/internal/config/config.go` | `PluginConfig` + 默认值；CSP 默认 `frame-src` 含 `'self'` |
| `backend/internal/server/router.go` | 创建 Registry/Runtime 并 `RegisterPluginRoutes` |
| `backend/internal/server/middleware/security_headers.go` | 必需 CSP 指令补 `frame-src 'self'` |
| `backend/internal/web/embed_on.go` | SPA fallback **绕过** `/plugin-runtime/`（勿改成 `/plugins/`：后者是前端页面路由） |
| `deploy/config.example.yaml` | `plugins:` 配置块 |
| `deploy/docker-compose.yml` | 注释说明 `/app/data/plugins` 热加载与可选 bind-mount |
| `frontend/src/App.vue` | 登录后拉插件列表 + 轮询；登出清理 |
| `frontend/src/api/index.ts` | 导出 `pluginsAPI` |
| `frontend/src/stores/index.ts` | 导出 `usePluginStore` |
| `frontend/src/router/index.ts` | `/plugins/:id`、`/admin/plugins` |
| `frontend/src/components/layout/AppSidebar.vue` | 动态插件菜单 +「插件运行时」入口 |
| `frontend/src/i18n/locales/{zh,en}/common.ts` | `nav.plugins` |
| `frontend/src/i18n/locales/{zh,en}/misc.ts` | `pluginPage.*` |
| `frontend/src/i18n/locales/{zh,en}/admin/index.ts` | 合并 plugins 文案模块 |
| `backend/internal/domain/constants.go` | `AdjustmentTypeGame`、game kind、notes 前缀 |
| `backend/internal/plugin/manifest.go` | `game` 段声明 + 校验（需 `api.secret_file`、正数限额） |
| `backend/internal/plugin/registry.go` | `GamePolicy`：Bearer=插件密钥鉴权 + 限额 |
| `backend/internal/repository/redeem_code_repo.go` | `Create` 支持事务上下文（`clientFromContext`） |
| `backend/internal/repository/wire.go` | `NewGameLedgerRepository` |
| `backend/internal/service/wire.go` | `NewGameLedgerService` |
| `backend/internal/handler/handler.go` + `wire.go` | `Handlers.GameLedger` |
| `backend/cmd/server/wire_gen.go` | 手工同步 Wire 图（无 wire 工具时保持结构一致） |
| `backend/internal/server/routes/plugins.go` | 内部账本路由 |
| `backend/internal/server/router.go` | `SetPluginRegistry` 绑定 |
| `frontend/src/components/admin/user/UserBalanceHistoryModal.vue` | 余额历史支持 `game` 类型 |
| `frontend/src/i18n/locales/{zh,en}/admin/overview.ts` | `typeGame` 文案 |
| `frontend/src/i18n/locales/{zh,en}/dashboard.ts` | 游戏余额变动文案 |
| `frontend/src/views/user/AvailableChannelsView.vue` | 渠道页倍率上限设置 |
| `frontend/src/api/groups.ts` | 用户倍率上限读写 API |
| `frontend/src/i18n/locales/{zh,en}/dashboard.ts`（availableChannels） | 上限设置文案 |
| `FORK.md` | 插件与落点约定 |
| `.gitignore` | 放行 `docs/PLUGINS.md`、`docs/FORK_DEVELOPMENT.md` |

### 4.3 关键路由 / 配置契约

| 方法 | 路径 | 谁用 | 鉴权 |
|------|------|------|------|
| GET/HEAD | `/plugin-runtime/:id/ui/*path` | iframe UI | 公开静态壳（权限在菜单 + API） |
| GET | `/api/v1/plugins` | 侧栏 / 插件页 | JWT |
| POST | `/api/v1/plugins/:id/invoke` | 父页面 API 桥 | JWT |
| GET | `/api/v1/admin/plugins` | 管理诊断 | Admin |
| POST | `/api/v1/admin/plugins/refresh` | 强制刷新 | Admin |

配置键（`config.yaml` / env 映射到 viper）：

```yaml
plugins:
  enabled: true
  directory: "/app/data/plugins"   # 本地开发默认 ./data/plugins
  refresh_interval_seconds: 2
  proxy_timeout_seconds: 30
  max_request_body_bytes: 2097152
  max_response_body_bytes: 16777216
```

### 4.4 安全边界（写插件/审 PR 时必守）

1. **不把主站 JWT 放进 iframe URL / fragment 业务字段**（仅随机 capability 给消息桥）
2. **不转发**浏览器 `Authorization` / `Cookie` 给插件侧车
3. 侧车只认 **插件独立密钥** + `X-Sub2API-User-*` 最小身份
4. API/UI 上游主机名必须是 `sub2api-plugin-<id>`（防清单 SSRF）
5. 静态 UI 只服务 `public/**`；隐藏控制文件不可 Web 访问
6. 插件 **不能** 注入计费/调度事务；需要核心钩子 → L4 评审

---

## 5. 日常开发流程

### 5.1 环境

```bash
# 后端：Go 版本以 backend/go.mod 为准（当前 go 1.26.x）
# 前端
cd frontend && pnpm install

# 代理（若本机网络需）
export HTTP_PROXY=http://127.0.0.1:7897
export HTTPS_PROXY=http://127.0.0.1:7897
```

Windows 上若未装系统 Go，可用工作区便携工具链（勿提交进 git）：

```bash
# 示例：仓库旁的 go/ 仅本机工具，不进 origin
./go/bin/go.exe test ./internal/plugin
```

### 5.2 加一个「不重建主容器」的功能（推荐路径）

1. 复制示例：
   ```bash
   cp -r examples/plugins/reaction-grid /app/data/plugins/my-tool
   # 改目录名、manifest.id、name、public/*
   ```
2. 需要后端时：
   - 增加侧车容器，hostname = `sub2api-plugin-my-tool`
   - 写 `.api-secret`（32–256 可打印 ASCII）
   - manifest 声明 `api.base_url` / `secret_file` / `allowed_methods`
3. 等 ≤ 数秒，或管理端「插件运行时」→ 立即刷新
4. 用户侧栏应出现菜单；打开 `/plugins/my-tool`

**不需要** `docker compose build sub2api`。

### 5.3 改主仓前端

```bash
git checkout fork/main
git pull origin fork/main
git checkout -b feature/xxx

# 优先新目录，少改上游已有大文件
# frontend/src/features/custom/...
# frontend/src/views/custom/...

cd frontend
pnpm test:run path/to/spec.ts
pnpm typecheck
pnpm build
```

### 5.4 改主仓后端

```bash
cd backend
# 优先 backend/internal/custom 或新 package
go test ./internal/plugin ./internal/yourpkg
go build -tags embed -o sub2api ./cmd/server
```

插件基座相关测试（改插件运行时必跑）：

```bash
cd backend && go test ./internal/plugin ./internal/server/middleware ./internal/config
cd frontend && pnpm test:run \
  src/stores/__tests__/plugins.spec.ts \
  src/features/plugins/__tests__/bridge.spec.ts
pnpm typecheck
```

### 5.5 提交与推送

```bash
git add -A
git status   # 确认没有把 go/、node_modules、密钥、大文件加进去
git commit -m "feat(plugins): ..."
git push -u origin HEAD
```

SSH 私钥若有 passphrase，在本机终端先 `ssh-add` 再 push（非交互环境无法输密码）。

---

## 6. 同步官方上游

```bash
git fetch upstream
git checkout fork/main
git checkout -b sync/upstream-$(date +%Y%m%d)
git merge upstream/main
# 冲突高发文件见 §4.2
# 解决后：
go test ./internal/plugin ./internal/server/middleware ./internal/config
cd frontend && pnpm typecheck && pnpm test:run src/stores/__tests__/plugins.spec.ts src/features/plugins/__tests__/bridge.spec.ts
git checkout fork/main
git merge sync/upstream-YYYYMMDD
git push origin fork/main
```

### 同步时检查清单

- [ ] `router.go` 仍注册 `RegisterPluginRoutes`
- [ ] `config.go` 仍有 `Plugins` / defaults / CSP `frame-src 'self'`
- [ ] `embed_on.go` 仍绕过 `/plugin-runtime/`（勿被 SPA `index.html` 吃掉；前端页仍是 `/plugins/:id`）
- [ ] 前端侧栏 / App 轮询 / 路由仍在
- [ ] 插件单测 + 安全头单测通过

---

## 7. 部署与热更新

### 7.1 主镜像（改了主仓才需要）

```bash
# 按你们现有 deploy 流程 build/push 镜像
# 仅当改了 backend/frontend 主仓代码时才重建 sub2api 镜像
```

### 7.2 插件（日常）

数据卷已包含插件目录：

```text
sub2api_data → /app/data → /app/data/plugins
```

可选开发 bind-mount（见 `deploy/docker-compose.yml` 注释）：

```yaml
volumes:
  - ./plugins:/app/data/plugins
```

侧车插件需加入同一 Docker 网络，**容器名/hostname = `sub2api-plugin-<id>`**，并与主服务共享密钥文件（只读）。

### 7.3 运维入口

- 管理 UI：`/admin/plugins`
- 强制刷新：`POST /api/v1/admin/plugins/refresh`
- 诊断字段：已加载列表 + 逐插件错误（不含密钥与上游 URL）

---

## 8. 代码落点约定（减少冲突）

| 类型 | 推荐路径 |
|------|----------|
| 插件业务 | `/app/data/plugins/<id>/`（运行时）或独立插件仓库 |
| 插件示例/模板 | `examples/plugins/<id>/` |
| 主站前端功能模块 | `frontend/src/features/custom/**` |
| 主站前端页面 | `frontend/src/views/custom/**` |
| 主站后端新包 | `backend/internal/custom/**` 或 `backend/internal/<feature>/` |
| 文档 | `docs/` 下 **allowlist** 文件（见 `.gitignore` 的 `!docs/xxx.md`） |

**避免**：

- 在 `gateway` / billing / 调度大文件里塞运营逻辑
- 把密钥、`.api-secret`、本地 `data/` 提交进 git
- 为插件再发明第二套鉴权（复用 JWT → invoke → 插件密钥）

---

## 9. 决策速查

| 需求 | 做法 |
|------|------|
| 换站名/Logo/首页 | OEM 设置（L0） |
| 侧栏挂活动页/小游戏 | 静态插件（L1） |
| 游戏写分/扣余额 | 插件 API + 侧车；余额变更优先走现有 Admin API / 主站服务，勿直连 DB |
| 深度共用主站组件 | 主仓前端（L2） |
| 新一等管理资源进主库 | 主仓后端（L3） |
| 新 AI 厂商/协议 | 核心改动（L4），整条链评审 |
| 不重建容器调功能 | **只动插件目录/侧车** |

---

## 10. 验收标准（插件相关 PR）

PR 作者至少自证：

1. `go test ./internal/plugin` 通过  
2. 相关前端测试 + `pnpm typecheck` 通过  
3. 手动或说明：
   - 放入示例插件后侧栏出现菜单  
   - `enabled: false` 后菜单与页面不可用  
   - 若有 API：上游收不到浏览器 JWT/Cookie，只收到插件密钥与用户 id/role  
4. 未把密钥写入仓库  
5. 若改了 §4.2 同文件，说明与上游合并策略  

---

## 11. 已知限制 / 非目标

- 无通用进程内 hook / 脚本沙箱  
- 插件 invoke **不支持** WebSocket / 任意流式透明代理（首版 JSON/文本桥）  
- 插件 UI 源码默认是「可加载的静态壳」，权限边界在 **菜单可见性 + invoke 鉴权**  
- 仓库历史中可能存在大文件告警（如 `backend/repository.test`），新提交不要再引入 >50MB 文件  

---

## 12. 新人第一周建议路径

1. 读本文 §3–§4，再读 `docs/PLUGINS.md`  
2. 本地跑通：后端 `go test ./internal/plugin`、前端两个 plugin 测试  
3. 复制 `reaction-grid` 到 data/plugins，登录看侧栏  
4. 用管理页「立即刷新 / 诊断错误」走一遍  
5. 领任务时先填落点：L0 / L1 / L2 / L3 / L4，再开工  

---

## 13. 维护义务

| 角色 | 义务 |
|------|------|
| 改插件运行时的人 | 更新 `docs/PLUGINS.md` + 本文 §4 清单 |
| 改 fork 约定的人 | 更新 `FORK.md` + 本文 |
| 同步上游的人 | 走 §6 检查清单并补测 |
| 写新插件的人 | 不把密钥提交进主仓；优先独立插件目录 |

**文档即契约**：行为与文档冲突时，先修实现或先改文档，不要默默分叉。
