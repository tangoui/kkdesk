---
layout: home

hero:
  name: kkdesk
  text: 点对点的远程桌面
  tagline: 用 Tango 写的 Go 程序。局域网直连，画面和操作不经过任何服务器；异地设备用自托管的跨网服务，通过设备码连接。
  image:
    src: /app-icon.svg
    alt: kkdesk
  actions:
    - theme: brand
      text: 开始使用
      link: /guide/introduction
    - theme: alt
      text: 部署跨网服务
      link: /deploy/server
    - theme: alt
      text: 从源码构建
      link: /dev/build

features:
  - title: 原生画面，低延迟
    details: macOS 用 ScreenCaptureKit 采集、VideoToolbox 硬件编解码，画面交给系统原生视频视图显示。Mac 自连实测延迟中位数 33 ms、P95 50 ms。
  - title: 一个程序，两种角色
    details: 同一个 kkdesk 既能被控也能控制别人，支持远程控制、仅查看、只传文件三种连接方式。Mac 可以控制 Mac 和 Windows。
  - title: 局域网直连
    details: 报地址和 6 位临时密码即可连接，对方需要确认。临时密码可按策略自动更换，也可以设固定密码。
  - title: 自托管跨网
    details: 部署一台 kkdesk-server，异地设备用 KD- 开头的设备码连接。WebRTC 能直连就直连，打不通时走你自己的 TURN 中继。
  - title: 文件传输与剪贴板
    details: 双栏文件窗口，上传下载文件和文件夹，逐个校验 SHA-256；两边复制的文字自动同步。被控端可以随时关掉这两项权限。
  - title: 数据只在本机
    details: 没有账号系统。设置、通讯录和临时密码保存在本机的 kkdesk.json，不会上传。
---
