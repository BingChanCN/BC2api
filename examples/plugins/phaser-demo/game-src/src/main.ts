import Phaser from 'phaser'

// 演示目标：验证 Phaser 构建产物在 sub2api 插件沙箱（/plugin-runtime/:id/ui/）内的行为。
// - 所有资产路径保持相对（配合 vite base './'）
// - assets/config.json 通过 Loader 的 XHR 加载，依赖插件 UI CSP 的 connect-src 'self'
// - 沙箱无 localStorage / 无主站 JWT：需要登录态数据时经父页面 postMessage 桥（见 coinflip 示例）
class MainScene extends Phaser.Scene {
  private sprites: Phaser.GameObjects.Arc[] = []

  constructor() {
    super('MainScene')
  }

  preload(): void {
    this.load.json('demo-config', 'assets/config.json')
  }

  create(): void {
    const centerX = this.scale.width / 2
    const centerY = this.scale.height / 2

    this.cameras.main.setBackgroundColor('#15171b')

    this.add.text(centerX, centerY - 90, 'Phaser 4', {
      fontFamily: 'Arial, sans-serif',
      fontSize: '42px',
      color: '#ffffff'
    }).setOrigin(0.5)

    const config = this.cache.json.get('demo-config') as { label?: string }
    this.add.text(centerX, centerY - 48, config?.label ?? 'sub2api plugin runtime', {
      fontFamily: 'Arial, sans-serif',
      fontSize: '16px',
      color: '#8dd9ff'
    }).setOrigin(0.5)

    // 无需外部图片的图形纹理 + 指针交互
    for (let i = 0; i < 5; i++) {
      const circle = this.add.circle(centerX - 120 + i * 60, centerY + 40, 22, 0x14b8a6, 0.9)
      circle.setInteractive({ useHandCursor: true })
      circle.on('pointerdown', () => {
        this.tweens.add({ targets: circle, scale: 1.6, duration: 120, yoyo: true })
      })
      this.sprites.push(circle)
    }

    this.tweens.add({
      targets: this.sprites,
      y: '+=12',
      duration: 900,
      yoyo: true,
      repeat: -1,
      ease: 'Sine.inOut',
      delay: (target: unknown, _key: string, index: number) => index * 120
    })
  }
}

new Phaser.Game({
  type: Phaser.AUTO,
  parent: 'game',
  scale: {
    mode: Phaser.Scale.RESIZE,
    width: '100%',
    height: '100%'
  },
  scene: MainScene
})
