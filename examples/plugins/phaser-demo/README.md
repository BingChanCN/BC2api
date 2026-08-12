# Phaser Demo 插件

Phaser 4 静态游戏示例，验证游戏引擎构建产物在 sub2api 插件沙箱内运行：

- `public/` — Vite 构建产物（`base: './'`，全部相对路径），可直接热加载
- `game-src/` — 游戏源码（Phaser 4.2.1 + Vite，源自 PhaserEditor vite-ts 模板）

## 部署

```bash
cp -r phaser-demo /app/data/plugins/
```

几秒后侧栏出现「Phaser Demo」（无需重建主服务）。

## 验证点

1. Phaser 引擎在 `sandbox="allow-scripts"` iframe 内正常运行（WebGL/Canvas）
2. `this.load.json('assets/config.json')` 经 XHR 加载 — 依赖插件 UI CSP 的 `connect-src 'self'`（主服务 v1 已放宽）
3. 点击彩色圆圈的 tween 交互
4. 全屏按钮（浏览器支持时）— 依赖 iframe `allow="fullscreen"`

## 重建产物

```bash
cd game-src
npm install
npm run build
cp -r dist/* ../public/
```

## 注意（与正式游戏相关）

- iframe 沙箱无 `allow-same-origin`：**无 localStorage、无主站 JWT**
- 需要登录态数据 / 余额动账时，经父页面 postMessage 桥或侧车（见 `examples/plugins/coinflip/`）
- 游戏资产用相对路径由 Phaser Loader 加载（XHR，同源）
