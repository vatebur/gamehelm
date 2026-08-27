# GameHelm（游舵）

GameHelm（游舵）是一个只使用 Go 标准库构建的多游戏服务器 Web 控制台。它通过配置文件统一管理任意数量的 systemd 游戏服务，并为每次启动提供自动关闭、倒计时和续时能力。

## 功能

- 密码登录、浏览器会话、CSRF 防护和登录失败限速
- 通过配置文件管理同一用户下的任意数量 systemd user services，允许同时运行
- 每个服务独立配置运行与续时时长，也可设为无限运行
- 倒计时保存在内存中；Web 服务重启后，按最新配置重新接管仍在运行的服务
- 自动接管从命令行或其他途径启动的游戏服务
- 应用日志统一写入 systemd user journal
- 手机优先的响应式简体中文界面

## 安全说明

服务按需求仅提供 HTTP，并监听所有网络接口。HTTP 会明文传输密码和会话 Cookie，不应通过不可信网络访问。示例配置密码是 `changeme`；请尽快修改 `config.json`，然后重启服务：

```bash
systemctl --user restart gamehelm.service
```

真实 `config.json` 和二进制均已被 Git 忽略。

## 构建与测试

```bash
source /etc/profile
vfox use golang@1.26.5
go test ./...
go build -o gamehelm ./cmd/gamehelm
vfox use nodejs@26.7.0
node --check internal/app/web/assets/app.js
```

## 安装

先创建并修改实际配置：

```bash
cp config.example.json config.json
nano config.json
```

`config.json` 位于项目目录，支持以下参数：

| 参数 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `listen` | 否 | `0.0.0.0:8231` | Web 服务监听地址。 |
| `password` | 是 | 无 | 控制台登录密码；请将示例值 `changeme` 改为强密码。 |
| `services` | 是 | 无 | 要管理的服务集合，至少包含一个服务。 |
| `services.<id>` | 是 | 无 | 服务标识，最多 64 个字符；必须以小写字母或数字开头，且只能包含小写字母、数字、下划线和连字符。 |
| `services.<id>.display_name` | 是 | 无 | 页面显示名称，不能是空字符串或纯空白。 |
| `services.<id>.unit` | 是 | 无 | 同一用户下真实的 systemd user unit 名称，例如 `palworld.service`；必须以 `.service` 结尾，只能包含字母、数字、下划线、点、`@` 和连字符，且不能与其他服务重复。 |
| `services.<id>.run_duration` | 否 | `4h` | 本次运行时长。有限值采用 Go 标准时长格式且不能少于 5 分钟；也可设为区分大小写的小写值 `infinite`，此时服务不会自动关闭，`extension_duration` 不生效且可省略。只有省略字段时才使用默认值，空字符串和 `null` 均为配置错误。 |
| `services.<id>.extension_duration` | 否 | `1h` | 每次续时时长。采用 Go 标准时长格式，不能少于 5 分钟，并且必须小于有限的 `run_duration`；`run_duration` 为 `infinite` 时不生效，但若填写仍须满足格式和最小时长要求。只有省略字段时才使用默认值，空字符串和 `null` 均为配置错误。 |

Go 标准时长格式支持 `48h`、`1h30m`、`1.5h` 和 `5m`，不支持 `1d`。可用 `systemctl --user list-unit-files --type=service` 查询可填写的 unit。

### 添加游戏 systemd user unit

游戏服务必须与 GameHelm 属于同一个普通用户。登录该用户后创建 unit；不要用 `sudo systemctl --user`。以下示例中的目录、启动脚本和停止信号需要按游戏实际要求修改：

```bash
mkdir -p ~/.config/systemd/user
nano ~/.config/systemd/user/palworld.service
```

```ini
[Unit]
Description=Palworld dedicated server
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
WorkingDirectory=/home/palworld/servers/palworld
ExecStart=/home/palworld/servers/palworld/start-server.sh
Restart=on-failure
RestartSec=5
KillSignal=SIGINT
TimeoutStopSec=120

[Install]
WantedBy=default.target
```

`ExecStart` 必须使用绝对路径。启动脚本应保持前台运行并让 systemd 跟踪真实游戏进程；需要管道、重定向或多条命令时，将它们放入可执行脚本，不要直接写 shell 语法。创建后验证并试运行：

```bash
systemd-analyze --user verify ~/.config/systemd/user/palworld.service
systemctl --user daemon-reload
systemctl --user start palworld.service
systemctl --user status palworld.service
journalctl --user -u palworld.service -f
systemctl --user stop palworld.service
```

仅当希望游戏随该用户的 systemd manager 自动启动时，才执行 `systemctl --user enable palworld.service`；GameHelm 本身不要求游戏 unit 已启用。最后在 `config.json` 的 `services` 中加入唯一的小写标识，并使 `unit` 与文件名完全一致：

```json
"palworld": {
  "display_name": "帕鲁世界",
  "unit": "palworld.service",
  "run_duration": "6h",
  "extension_duration": "30m"
}
```

运行 `./gamehelm -config config.json -check` 确认 unit 已加载。修改配置后需重启 `gamehelm.service` 才会生效；若 GameHelm 已安装，执行 `systemctl --user restart gamehelm.service` 载入新配置。

安装脚本会自动识别 `amd64` 或 `arm64`，下载 GitHub Releases 中的最新 Linux 二进制并校验 SHA-256；目标机无需安装 Go。执行：

```bash
sudo ./gamehelm.sh
```

如需固定版本，可指定 Release tag：

```bash
sudo GAMEHELM_VERSION=v1.3 ./gamehelm.sh
```

默认访问地址为 `http://服务器IP:8231`，示例配置的初始密码是 `changeme`。

安装脚本会创建或更新：

- `~/.config/systemd/user/gamehelm.service`：GameHelm 的 user service 定义，设置工作目录、启动命令和异常重启策略；脚本会启用并立即启动该服务。
- `/var/lib/systemd/linger/<用户>`：`loginctl enable-linger` 创建的标记，使该用户未登录时 user services 仍能开机运行；卸载 GameHelm 时会保留。
- `gamehelm`：从 GitHub Releases 下载并通过 SHA-256 校验的可执行文件，保存在项目目录。
- `config.json`：实际配置，保存在项目目录且不提交到 Git。

GameHelm 和游戏服务必须运行在同一个普通用户的 systemd user manager 下。脚本不会迁移系统级游戏 unit，也不会创建 `/etc/sudoers.d/gamehelm`。

## 卸载

```bash
sudo ./gamehelm.sh uninstall
```

卸载会清理 GameHelm user service 和运行文件，但保留源码、游戏 user services 和 linger。

## 日志

GameHelm 日志统一保存在 systemd user journal：

```bash
journalctl --user -u gamehelm.service
journalctl --user -u gamehelm.service -f
```

## 文件

- `config.json`：实际配置，明文保存密码，不纳入 Git
- `config.example.json`：可提交的配置示例
- `cmd/gamehelm/`：命令行入口
- `internal/app/`：配置、认证、服务控制、状态和 Web 页面
- `deploy/gamehelm.service`：使用非敏感占位符的 systemd unit 模板
- `tests/`：配置、控制器和 HTTP 集成测试

如果管理员主动停止 `gamehelm.service`，运行中的有限时长游戏服务不会在控制台停机期间准时关闭。控制台再次启动后会接管仍在运行的游戏服务：有限服务从重新发现时开始完整的新计时，无限服务继续保持运行。
