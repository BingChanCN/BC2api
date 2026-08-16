# Plugin examples

## import-override

管理员静态工具：导入账号 JSON 前覆盖名称、并发、优先级、备注、extra，或清掉静态/受管代理绑定。不改凭证，也不调用导入 API。处理完后回到账号页导入。

```bash
cp -r examples/plugins/import-override /path/to/data/plugins/
```

要求：目录名 = `manifest.id` = `import-override`，`visibility=admin`。

## reaction-grid

纯静态小游戏，演示：

1. 热加载菜单
2. 沙箱 iframe
3. 无 API 插件

部署：

```bash
cp -r examples/plugins/reaction-grid /path/to/data/plugins/
# 等待最多数秒，或在管理端点“立即刷新”
```

要求：

- 目录名 = `manifest.id` = `reaction-grid`
- 入口文件：`public/index.html`

## coinflip（游戏账本示例）

proxy 模式侧车，演示余额下注/发奖：

```text
侧车：sub2api-plugin-coinflip（与主服务同网络）
  1. POST /play（经主站 invoke 桥）→ 主站扣 stake
  2. 开奖：赢 → 主站发 payout(2x)；输 → 不动
  3. 发奖失败 → 自动 refund 下注
```

构建运行：

```bash
cd sidecar && go build -o coinflip main.go
SUB2API_BASE_URL=http://sub2api:8080 \
PLUGIN_SECRET_FILE=../.api-secret \
./coinflip
```

清单里 `game.max_stake=10 / max_payout=20`，与侧车 `maxStake` 保持一致。
上线前请把 `.api-secret` 换成随机密钥（32–256 位可打印 ASCII）。
