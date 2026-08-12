# 外部插件运行时

Sub2API fork 的插件系统采用 **外部 HTTP 插件** 模型：

- 插件是独立目录（可选独立容器），不是主进程内 `.so` / 脚本热加载
- 主服务只负责清单发现、菜单聚合、UI 托管/代理、认证后的受控 API 调用
- 增删改插件 **不需要重建主容器**；只需写挂载目录，或更新插件侧车

## 目录约定

默认目录：

```text
/app/data/plugins/<plugin-id>/
  manifest.json
  .api-secret          # 仅当声明 api 时需要；隐藏文件，不可被 UI 直接读取
  public/
    index.html
    ...
```

- 目录名必须等于 `manifest.id`
- 静态资源只能发布在 `public/`
- 控制文件（`manifest.json`、`.api-secret`）不会通过 UI 路径暴露

## manifest.json

```json
{
  "schema_version": 1,
  "id": "reaction-grid",
  "name": "反应网格",
  "version": "1.0.0",
  "description": "纯静态小游戏",
  "enabled": true,
  "visibility": "user",
  "sort_order": 100,
  "runtime": {
    "type": "static",
    "entry": "public/index.html",
    "spa": false
  }
}
```

字段要点：

| 字段 | 说明 |
|------|------|
| `enabled` | `false` 时立即从菜单/运行时下线；禁用状态不要求资源完整 |
| `visibility` | `user` 或 `admin` |
| `runtime.type` | `static`：读本地 `public/`；`proxy`：转发到侧车 UI |
| `runtime.base_url` / `api.base_url` | 主机名必须是 `sub2api-plugin-<id>` |
| `api.secret_file` | 隐藏文件名，内容 32–256 位可打印 ASCII，无空格 |

## 热加载

- 主服务按 `plugins.refresh_interval_seconds`（默认 2s）请求驱动刷新
- 前端登录后每 5 秒轮询 `/api/v1/plugins`
- 管理员可在「插件运行时」页点击「立即刷新」

典型操作：

```bash
# 部署示例静态插件
cp -r examples/plugins/reaction-grid /app/data/plugins/

# 禁用
# 编辑 manifest.json: "enabled": false
# 或直接删除目录

# 无需 docker compose rebuild / recreate 主镜像
```

Compose 中 `sub2api_data:/app/data` 已覆盖 `/app/data/plugins`。若要在开发机直接编辑：

```yaml
volumes:
  - ./plugins:/app/data/plugins
```

## 安全边界

1. **UI 是无权限沙箱壳**
   - 前端用 `sandbox="allow-scripts"` iframe 加载
   - 插件文档强制独立 CSP：`connect-src 'none'`、`sandbox allow-scripts`
   - 不向插件 URL 附带主站 JWT / Cookie

2. **API 走主站认证桥**
   - 浏览器：`POST /api/v1/plugins/:id/invoke`（需登录）
   - iframe：通过 `postMessage` 请求父页面代调
   - 主服务替换身份头，**不转发**浏览器 `Authorization` / `Cookie`
   - 上游只收到：
     - `Authorization: Bearer <plugin-secret>`
     - `X-Sub2API-User-ID`
     - `X-Sub2API-User-Role`
     - `X-Sub2API-Plugin-ID`
     - `X-Sub2API-Plugin-Protocol`
     - `X-Sub2API-Request-ID`

3. **不是万能代理**
   - 方法白名单、body/响应大小限制、禁止跟随重定向
   - 不透传上游 `Set-Cookie`
   - 不允许插件清单把 API 指到任意外网主机（防 SSRF）

4. **不是进程内插件**
   - 不能注入计费/调度事务钩子
   - 不能直接访问主库
   - 需要改核心请求链仍要发版

## 前端消息桥

插件页 fragment 会带：

```text
#protocol=sub2api-plugin-v1&capability=<random>
```

调用示例：

```js
const params = new URLSearchParams(location.hash.slice(1))
const capability = params.get('capability')

function invoke(request) {
  const request_id = crypto.randomUUID()
  return new Promise((resolve, reject) => {
    function onMessage(event) {
      const data = event.data
      if (!data || data.protocol !== 'sub2api-plugin-v1' || data.type !== 'result') return
      if (data.capability !== capability || data.request_id !== request_id) return
      window.removeEventListener('message', onMessage)
      if (data.ok) resolve(data.response)
      else reject(data.error)
    }
    window.addEventListener('message', onMessage)
    parent.postMessage({
      protocol: 'sub2api-plugin-v1',
      type: 'invoke',
      capability,
      request_id,
      request
    }, '*')
  })
}

// request 例：
// { method: 'POST', path: 'scores', content_type: 'application/json', body: '{"score":7}' }
```

## 侧车插件

1. 插件目录放 manifest + `.api-secret`
2. 侧车容器加入同一 Docker 网络，主机名必须是 `sub2api-plugin-<id>`
3. 侧车与主服务共享只读密钥文件
4. 侧车只信任 `Authorization: Bearer <secret>`，不要信任浏览器直接请求
5. 游戏私有数据（历史/排行榜/局内状态）由侧车自己持久化，见下「侧车持久化」

```yaml
services:
  sub2api-plugin-echo:
    image: your-plugin:latest
    container_name: sub2api-plugin-echo
    networks: [default]
    volumes:
      - ./plugins/echo/.api-secret:/run/secrets/plugin-api:ro
      - ./plugins/echo/data:/app/data
```

## 侧车持久化

侧车是独立进程，游戏自己的数据（局历史、排行榜、任务进度……）**由侧车自己持久化**，主站不代持也不审计。约定：

- **存哪**：侧车容器内的 `DATA_DIR`（默认 `./data`，通过 bind mount 落到插件目录旁的宿主目录），重建容器/换镜像数据不丢
- **怎么存**：单文件 JSON 原子写（参照实现：examples/plugins/coinflip 侧车的 `store.go` FileStore）或 SQLite/bbolt，按游戏数据量自选；结构由各游戏自己设计
- **不存什么**：余额缓存不落盘——余额权威在主站，重启后经 `/me` 实时查询（`POST /api/v1/internal/game/query`）恢复
- **边界**：侧车数据删了游戏从头开始，主站账本/流水不受影响；余额与审计仍以主站为准
- **备份**：随插件目录（宿主机普通目录）一起走

## 管理接口

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/plugins` | 当前用户可见插件列表 |
| `POST` | `/api/v1/plugins/:id/invoke` | 认证后调用插件 API |
| `GET` | `/api/v1/admin/plugins` | 诊断：已加载插件与错误 |
| `POST` | `/api/v1/admin/plugins/refresh` | 强制刷新注册表 |
| `GET/HEAD` | `/plugin-runtime/:id/ui/*path` | 插件 UI 静态/代理 |

管理 UI：侧栏 **插件运行时** → `/admin/plugins`

## 配置

```yaml
plugins:
  enabled: true
  directory: "/app/data/plugins"
  refresh_interval_seconds: 2
  proxy_timeout_seconds: 30
  max_request_body_bytes: 2097152
  max_response_body_bytes: 16777216
```

## 示例

仓库内示例：

```text
examples/plugins/reaction-grid/   # 纯静态小游戏（无侧车）
examples/plugins/phaser-demo/     # Phaser 4 静态游戏（验证游戏引擎产物接入）
examples/plugins/coinflip/        # proxy 侧车 + 游戏账本（stake/payout 完整流）
```

复制到运行时目录后，用户侧栏会出现对应菜单，无需重建主容器。

## Phaser 游戏接入

Phaser（2D HTML5 游戏引擎）的构建产物可直接作为 `static` 插件。要点：

1. **Vite base './'**：构建产物所有资源引用必须相对路径，否则 `/assets/...` 会 404（插件 UI 挂在 `/plugin-runtime/:id/ui/` 子路径）。PhaserEditor vite-ts 模板已内置。
2. **Loader 资源**：`this.load.image/audio/json/...` 走 XHR，依赖插件 UI CSP 的 `connect-src 'self'`（v1 已放宽，仅同源、匿名请求，不携带主站 Cookie）。
3. **沙箱限制**：iframe 无 `allow-same-origin` → 无 localStorage、无主站 JWT。存档与登录态数据经父页面 postMessage 桥或侧车。
4. **全屏**：iframe 已带 `allow="fullscreen"`。
5. **账本游戏**：要下注/发奖时用侧车模式，见下方「游戏账本」。

接入流：本地 `npm run build` → `dist/*` 拷入插件 `public/` → 挂到运行时目录。示例 `examples/plugins/phaser-demo/`（含 `game-src/` 源码与重建脚本说明）。

## 游戏账本（余额变动）

游戏插件可以调用主站内部账本 API 移动用户余额。**游戏侧车永远不直接写数据库**；每次下注/发奖都产生 `type=game` 的余额流水，可在管理后台余额历史中审计。

### 声明（manifest）

```json
{
  "id": "coinflip",
  "api": {
    "base_url": "http://sub2api-plugin-coinflip:8080",
    "secret_file": ".api-secret"
  },
  "game": {
    "enabled": true,
    "max_stake": 10,
    "max_payout": 100
  }
}
```

- `game.enabled` 开启后必须同时声明 `api.secret_file`（账本用同一把共享密钥鉴权）
- `max_stake` 限制 `stake` / `refund` 单笔金额；`max_payout` 限制 `payout` / `bonus`
- 限额为 0 或缺失 → 清单校验失败，插件不上线

### 内部 API

#### 动账：POST /api/v1/internal/game/transactions

```http
POST /api/v1/internal/game/transactions
Authorization: Bearer <该插件的 .api-secret 内容>
```

```json
{
  "game_id": "coinflip",
  "kind": "stake",
  "round_id": "5f9c1b2e-...",
  "user_id": 123,
  "amount": 1.5,
  "note": "第 3 局下注"
}
```

`kind` 四类：

| kind | 方向 | 语义 |
|------|------|------|
| `stake` | - | 下注预扣；余额不足直接失败 |
| `payout` | + | 赢家奖金 |
| `refund` | + | 中断/取消退还（受 `max_stake` 限制） |
| `bonus` | + | 活动奖励 |

### 语义保证（主站强制）

1. **鉴权**：Bearer 必须等于该 game 的 `.api-secret`；插件停用/清单失效立即冻结
2. **限额**：超过 `max_stake` / `max_payout` 拒绝
3. **原子性**：余额变动、`type=game` 流水、幂等标记在同一数据库事务提交
4. **幂等**：同一 `(round_id, kind)` 只会结算一次；重放返回首次结果，不重复动账
5. **不欠费**：`stake` 使余额为负时整笔拒绝，不产生流水

### 响应

```json
{ "code": 0, "message": "success", "data": { "replayed": false, "balance_after": 8.5 } }
```

错误（`code` 为 HTTP 状态码）：

| 状态 | reason | 含义 |
|------|--------|------|
| 401 | — | 游戏凭证无效/插件停用 |
| 403 | `GAME_AMOUNT_TOO_LARGE` | 超过清单限额 |
| 403 | `GAME_INSUFFICIENT_BALANCE` | 用户余额不足 |
| 409 | `GAME_ROUND_IN_PROGRESS` | 同轮交易处理中（请稍后重试） |
| 409 | `GAME_ROUND_SETTLED` | 同 `(round_id, kind)` 已结算 |

#### 查询：POST /api/v1/internal/game/query

```http
POST /api/v1/internal/game/query
Authorization: Bearer <该插件的 .api-secret 内容>
```

```json
{ "game_id": "coinflip", "user_id": 123 }
```

响应：`{ "code": 0, "message": "success", "data": { "user_id": 123, "balance": 45.5 } }`

- 只读查询用户当前余额（权威值来自 users 表），用于侧车 `/me` 等展示端点
- 鉴权与动账一致：Bearer 必须等于该 game 的 `.api-secret`；插件停用即拒绝
- 用户不存在返回 404（`USER_NOT_FOUND`）
- **信任边界**：持 secret 的侧车可查询任意 `user_id` 的余额。侧车与动账同级信任（管理员部署的协作组件），密钥泄需轮换 `.api-secret`
- `data` 为对象，将来扩展字段（昵称/头像等）不破坏契约

### 审计

- 流水类型 `game`，管理后台余额历史可见（含中文/英文文案）
- `notes` 固定前缀：`game:<game_id>:<kind>:<round_id> <备注>`
- 后续做余额分类时按 `type` 或 `game:<id>` 前缀聚合

### 推荐下注流

```text
1. sidecar 收到开局请求 → POST stake（先扣，失败不开局）
2. 结算：
   - 赢 → POST payout（奖金）
   - 输 → 不动（stake 已扣）
   - 中断 → POST refund
3. round_id 必须由 sidecar 生成并持久化，绝不信任客户端
```
