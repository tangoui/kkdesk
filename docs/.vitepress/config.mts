import { defineConfig } from 'vitepress'

// The site is built from docs/; the design notes, plans and spike report in
// the same directory are internal and stay out of it.
export default defineConfig({
  lang: 'zh-CN',
  title: 'kkdesk',
  description: '用 Tango 写的远程桌面：局域网直连与自托管跨网连接',
  cleanUrls: true,
  lastUpdated: true,
  srcExclude: ['superpowers/**', 'design/**', 'spike-report.md', 'README.md'],
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/logo.svg' }]],
  themeConfig: {
    logo: { light: '/logo.svg', dark: '/logo-dark.svg' },
    nav: [
      { text: '使用指南', link: '/guide/introduction', activeMatch: '/guide/' },
      { text: '部署跨网服务', link: '/deploy/server', activeMatch: '/deploy/' },
      { text: '开发', link: '/dev/build', activeMatch: '/dev/' },
    ],
    sidebar: {
      '/guide/': [
        {
          text: '开始',
          items: [
            { text: '简介', link: '/guide/introduction' },
            { text: '安装与首次运行', link: '/guide/install' },
          ],
        },
        {
          text: '连接',
          items: [
            { text: '局域网连接', link: '/guide/connect-lan' },
            { text: '跨网连接（设备码）', link: '/guide/remote' },
            { text: '远程控制与控制中心', link: '/guide/control' },
          ],
        },
        {
          text: '功能',
          items: [
            { text: '剪贴板', link: '/guide/clipboard' },
            { text: '文件传输', link: '/guide/file-transfer' },
            { text: '我的设备与远程会话', link: '/guide/devices' },
            { text: '设置', link: '/guide/settings' },
          ],
        },
        {
          text: '参考',
          items: [
            { text: '安全与权限', link: '/guide/security' },
            { text: '常见问题', link: '/guide/faq' },
          ],
        },
      ],
      '/deploy/': [
        {
          text: '部署',
          items: [{ text: '部署跨网服务', link: '/deploy/server' }],
        },
      ],
      '/dev/': [
        {
          text: '开发',
          items: [
            { text: '构建与运行', link: '/dev/build' },
            { text: '命令行参数', link: '/dev/cli' },
            { text: '架构', link: '/dev/architecture' },
            { text: '测试与测量', link: '/dev/testing' },
          ],
        },
      ],
    },
    outline: { level: [2, 3], label: '本页内容' },
    docFooter: { prev: '上一页', next: '下一页' },
    lastUpdated: { text: '最后更新' },
    returnToTopLabel: '回到顶部',
    sidebarMenuLabel: '目录',
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换到浅色',
    darkModeSwitchTitle: '切换到深色',
    search: {
      provider: 'local',
      options: {
        translations: {
          button: { buttonText: '搜索文档', buttonAriaLabel: '搜索文档' },
          modal: {
            noResultsText: '没有找到结果',
            resetButtonTitle: '清除',
            footer: { selectText: '选择', navigateText: '切换', closeText: '关闭' },
          },
        },
      },
    },
    footer: {
      message: '以 <a href="https://www.gnu.org/licenses/agpl-3.0.html">AGPL-3.0</a> 许可证发布 · 基于 Tango 构建',
    },
    socialLinks: [{ icon: 'github', link: 'https://github.com/tangoui/kkdesk' }],
  },
})
