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

```yaml
services:
  sub2api-plugin-echo:
    image: your-plugin:latest
    container_name: sub2api-plugin-echo
    networks: [default]
    volumes:
      - ./plugins/echo/.api-secret:/run/secrets/plugin-api:ro
```

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
examples/plugins/reaction-grid/
```

复制到运行时目录后，用户侧栏会出现「反应网格」，打开即可玩，无需侧车、无需重建主容器。
