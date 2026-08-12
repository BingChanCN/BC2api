# Plugin examples

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
