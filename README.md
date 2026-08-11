# GameHelm（游舵）

GameHelm（游舵）是一个只使用 Go 标准库构建的多游戏服务器 Web 控制台。它通过配置文件统一管理任意数量的 systemd 游戏服务，并为每次启动提供自动关闭、倒计时和续时能力。

## 功能

- 密码登录、浏览器会话、CSRF 防护和登录失败限速
- 通过配置文件管理同一用户下的任意数量 systemd user services，允许同时运行
- 每次启动自动计时 4 小时，最后 1 小时可反复续时 1 小时
- 倒计时保存在内存中；Web 服务重启后，为仍在运行的游戏服务重新计时 4 小时
- 自动接管从命令行或其他途径启动的游戏服务
- 应用日志统一写入 systemd user journal
- 手机优先的响应式简体中文界面

## 安全说明

服务按需求仅提供 HTTP，并监听所有网络接口。HTTP 会明文传输密码和会话 Cookie，不应通过不可信网络访问。初始密码是 `changeme`；请尽快修改 `config.json`，然后重启服务：

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

`config.json` 位于项目目录。将 `password` 改为强密码，并检查 `services`：`display_name` 是页面显示名称，`unit` 必须填写同一用户下真实的 systemd user unit 名称，例如 `palworld.service`。可用 `systemctl --user list-unit-files --type=service` 查询。

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
  "unit": "palworld.service"
}
```

运行 `./gamehelm -config config.json -check` 确认 unit 已加载。若 GameHelm 已安装，再执行 `systemctl --user restart gamehelm.service` 载入新配置。

确保已安装 Go 1.26.5，然后执行安装：

```bash
sudo ./gamehelm.sh
```

默认访问地址为 `http://服务器IP:8231`，示例配置的初始密码是 `changeme`。

安装脚本会创建或更新：

- `~/.config/systemd/user/gamehelm.service`：GameHelm 的 user service 定义，设置工作目录、启动命令和异常重启策略；脚本会启用并立即启动该服务。
- `/var/lib/systemd/linger/<用户>`：`loginctl enable-linger` 创建的标记，使该用户未登录时 user services 仍能开机运行；卸载 GameHelm 时会保留。
- `gamehelm`：由源码构建的可执行文件，保存在项目目录。
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

如果管理员主动停止 `gamehelm.service`，运行中的游戏服务不会在控制台停机期间准时关闭。控制台再次启动后会接管仍在运行的游戏服务，并从重新发现服务时开始新的 4 小时倒计时。
