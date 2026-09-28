# fnProxy

![fnProxy 图标](packaging/icons/icon_256.png)

fnProxy 是面向飞牛 fnOS NAS 宿主机的 Mihomo TUN 代理应用。支持导入 Clash/Mihomo YAML 订阅、选择节点和代理组，以及规则分流、全局和直连模式。

Native Mihomo TUN proxy manager for the fnOS NAS host, with Clash/Mihomo YAML imports, node selection and rule-based routing.

[下载 x86 FPK 安装包](https://github.com/WhitlockXD/fnProxy/releases/download/v0.1.10/fnproxy_0.1.10_x86.fpk) · [查看发布版本](https://github.com/WhitlockXD/fnProxy/releases)

## 功能

- 管理 NAS 宿主机的 TUN 代理，首次启动保持关闭。
- 导入 HTTPS 订阅地址或本地 Clash/Mihomo YAML，选择代理组和节点。
- 规则模式优先采用可识别的订阅规则，再用内置国内直连规则兜底；也可切换全局或直连模式。
- 检测宿主机出口、常用站点连接与 DNS 状态，并提供停止代理和网络恢复操作。

## fnOS 实机截图

### 总览

![fnProxy 在飞牛 fnOS 桌面的总览页面](docs/screenshots/fnos-overview.png)

### 节点与规则

![fnProxy 代理组、节点和运行模式页面](docs/screenshots/fnos-rule-mode.png)

### 连接诊断

![fnProxy 检测 YouTube、Google、GitHub 和百度连接的诊断页面](docs/screenshots/fnos-connectivity.png)

## 安装与使用

在 x86_64 的 fnOS 中手动安装 FPK，打开 fnProxy，导入有效订阅，选择节点后开启系统 TUN。需要排查网络时，可在总览和诊断页运行检测；停止应用代理可使用「停止代理并恢复网络」。内部应用 ID 保持 `fnvpn`，用于从旧版本升级。

若管理页面无法打开，可在 NAS 本地终端以 root 执行恢复命令：

```sh
TRIM_APPDEST=/var/apps/fnvpn/target TRIM_PKGVAR=/var/apps/fnvpn/var \
  /var/apps/fnvpn/target/bin/fnvpn recover
```

## 适用范围

代理范围为 fnOS 宿主机；Docker 容器和其他局域网设备不在覆盖范围内。订阅只提取受支持的节点、代理组和规则，不会完整套用原始 Clash 配置。系统 DNS 指向私有地址或存在 IPv6 默认路由时，应用会拒绝开启。站点连接成功不等于已证明每条流量的实际代理出口。

## 构建与许可

构建需要 Go、Python 3 与 Pillow，以及官方 `fnpack`。在 Windows PowerShell 中运行 `scripts/build.ps1`，默认生成 x86 FPK。

项目代码采用 GPL-3.0；Mihomo、YAML 解析库和规则数据的来源与许可见[第三方声明](THIRD_PARTY_NOTICES.md)。
