# GameHelm（游舵）2.0

GameHelm 是使用 Go 构建的轻量级服务管理 Web 控制台，统一管理同一用户下的 systemd user services，以及本机 Docker Engine 中配置好的容器。适用于 Web 应用、后台任务、开发环境和游戏服务器，支持异步启停、定时关闭、续时和无限运行，界面适配手机。

默认访问地址为 `http://服务器IP:8231`。`public_origin` 和 `trusted_proxies` 都是可选配置，示例默认注释。需要 HTTPS 反向代理时启用对应配置。HTTP 访问适用于可信局域网，远程访问可以使用 HTTPS。

## 配置

```bash
cp config.example.json config.json
chmod 600 config.json
```

编辑 `config.json`，填写自己的口令及实际受管服务。配置支持 `//` 和 `/* */` 注释。初始口令至少 12 个字符、最多 1024 字节。首次启动会在监听端口前，将 `auth.password` 原子替换为带随机盐的 Argon2id `auth.password_hash`，保留文件中的注释、其他字段及原有排版。配置文件所在目录需要运行用户的写权限。

| 字段 | 说明 |
| --- | --- |
| `web.listen` | 默认 `0.0.0.0:8231` |
| `web.public_origin` | 可选，对外 HTTP 或 HTTPS 来源地址，用于来源校验。HTTPS 地址会启用 Secure Cookie |
| `web.trusted_proxies` | 可选，可信代理 CIDR 列表。省略时按实际连接来源限速 |
| `auth.password` | 初次启动或更换密码时填写的明文，与 `password_hash` 互斥 |
| `auth.password_hash` | 启动后生成的 Argon2id 哈希 |
| `docker.socket` | 使用 Docker 时填写本机 Unix socket 的绝对路径 |
| `services.<id>.display_name` | 页面名称 |
| `services.<id>.runtime` | `systemd` 或 `docker` |
| `services.<id>.unit` | systemd 完整 `.service` 名称 |
| `services.<id>.container` | Docker 完整容器名 |
| `services.<id>.run_duration` | 默认 `4h`，支持 Go 时长格式或 `infinite` |
| `services.<id>.extension_duration` | 默认 `1h`，至少 `5m`，且小于有限运行时长 |

有限运行时长至少为 `5m`，空字符串与 `null` 会被拒绝。续时在剩余时间进入一个续时窗口后开放，每次在原截止时间上增加配置的续时长度。

Docker 配置片段如下。先通过 Docker 或 Compose 创建容器，再将其加入配置。

```json
"docker": { "socket": "/run/user/1000/docker.sock" },
"services": {
  "worker": {
    "display_name": "后台任务",
    "runtime": "docker",
    "container": "worker",
    "run_duration": "4h",
    "extension_duration": "1h"
  }
}
```

推荐使用运行用户的 Rootless Docker socket。常规 Docker 可填写 `/var/run/docker.sock`，管理员须授予运行用户访问权限，该权限具有宿主机 root 级能力。

## 计时与故障

倒计时、续时次数、会话及操作进度保存在内存中。GameHelm 重启后按当前配置重新接管仍在运行的服务，重新获得完整时长，续时次数归零。控制台停机期间服务由原运行时托管，已经提交的停止操作仍可能完成。

同一个 GameHelm 进程运行期间，Docker 或 systemd 暂时不可达会显示「异常」并保留计时。恢复后若仍为同一次运行则继续原计时，已经到期的服务会自动关停。服务外部重启、Docker 自动重启或容器重建会开启新一轮计时。

配置在 GameHelm 启动时加载。修改后重启 GameHelm 生效。换密码时将 `auth.password_hash` 替换为新的 `auth.password` 再重启，原会话随重启失效。

## 检查与安装

```bash
./gamehelm -config config.json -check-config
./gamehelm -config config.json -check
sudo GAMEHELM_VERSION=v2.0.0 ./gamehelm.sh
```

`-check-config` 只读校验配置，`-check` 进一步检查全部目标。正常启动允许目标暂时不可达，面板会显示异常。安装脚本从指定 GitHub Release 下载 Linux 二进制并验证 SHA-256。使用本地构建产物时执行以下命令。

```bash
sudo GAMEHELM_BINARY=/绝对路径/gamehelm-linux-amd64 ./gamehelm.sh
```

脚本以项目目录属主作为运行用户，可以用 `GAMEHELM_USER` 和 `GAMEHELM_DIR` 指定用户及路径。运行用户须为普通用户。受管 systemd unit 和 GameHelm 位于同一用户的 systemd manager 下。脚本配置 linger，并安装 `~/.config/systemd/user/gamehelm.service`。

```bash
systemctl --user restart gamehelm.service
journalctl --user -u gamehelm.service -f
sudo ./gamehelm.sh uninstall
```

## 构建与验证

使用 `go.mod` 指定的 Go 工具链。

```bash
go test -race ./...
go build -trimpath -o gamehelm ./cmd/gamehelm
node --check internal/web/assets/app.js
```

代码按 `config`、`auth`、`control`、`runtime`、`web` 拆分，由 `internal/app` 组装。完整设计与验收标准见 [2.0 设计方案](docs/v2-design.md)。
