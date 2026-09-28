# 第三方软件与来源

| 软件 | 固定版本与用途 | 许可证 | 源码与二进制来源 |
| --- | --- | --- | --- |
| Mihomo | `v1.19.31`，随 FPK 打包的 TUN 内核 | GPL-3.0（以该标签的 `LICENSE` 为准） | [源码标签](https://github.com/MetaCubeX/mihomo/tree/v1.19.31)、[官方发布资产](https://github.com/MetaCubeX/mihomo/releases/tag/v1.19.31) |
| `gopkg.in/yaml.v3` | `v3.0.1`，Go YAML 解析与生成 | MIT/Apache 双许可 | [源码](https://github.com/go-yaml/yaml/tree/v3.0.1) |
| `MetaCubeX/meta-rules-dat` | 固定提交 `0f3410e013082b242f381750899a533078914f34`，国内域名与 IP 分流数据 | GPL-3.0 | [域名规则](https://github.com/MetaCubeX/meta-rules-dat/blob/0f3410e013082b242f381750899a533078914f34/geo/geosite/cn.mrs)、[IP 规则](https://github.com/MetaCubeX/meta-rules-dat/blob/0f3410e013082b242f381750899a533078914f34/geo/geoip/cn.mrs) |

Mihomo 官方发布资产的压缩文件 SHA-256：

| 架构 | 文件 | SHA-256 |
| --- | --- | --- |
| x86_64 | `mihomo-linux-amd64-compatible-v1.19.31.gz` | `04cf9f09671704f839ddbee2e93069dc831a4123a75281e725d1d96ab9ac1afc` |
| arm64 | `mihomo-linux-arm64-v1.19.31.gz` | `9e0f11afbf38426b8bd88fdc594678f8161c57eccb4e1b77acb12b493904f1d4` |

内置国内分流数据的 SHA-256：`cn-domain.mrs` 为 `6c4f403acc88c339a9aa77ecd7f711bc2506699cf810681b868547fcd1a480a1`，`cn-ip.mrs` 为 `4cc9ab3b7e2bbd18e0420e09af42818d0748a596dd3daaed470bb2d9958d118d`。

构建的 FPK 包含本项目根目录的 `LICENSE`，第三方许可位于应用目录 `licenses/MIHOMO_LICENSE`、`licenses/YAML_LICENSE` 与 `licenses/RULES_LICENSE`。对应原文也保存在仓库 `licenses/`。构建工具 `fnpack 1.2.3` 从[fnOS 官方文档](https://developer.fnnas.com/docs/cli/fnpack)指定地址取得，仅用于构建，不随 FPK 分发。

开发时阅读了 [RROrg/fn-apps](https://github.com/RROrg/fn-apps)、[scoltzero/msf](https://github.com/scoltzero/msf) 和 [adan323/Mihomo-fpk](https://github.com/adan323/Mihomo-fpk) 的当前项目说明与相关结构。前两个仓库采用 GPL-3.0；第三个仓库的 README 声称 MIT，但当前仓库根目录未找到许可证文件。没有复制这些参考项目的源码、样式或构建脚本。
