# GameHelm（游舵）

GameHelm（游舵）是一个只使用 Go 标准库构建的多游戏服务器 Web 控制台。它通过配置文件统一管理任意数量的 systemd 游戏服务，并为每次启动提供自动关闭、倒计时和续时能力。

## 功能

- 密码登录、浏览器会话、CSRF 防护和登录失败限速
- 通过配置文件管理同一用户下的任意数量 systemd user services，允许同时运行
- 每次启动自动计时 4 小时，最后 1 小时可反复续时 1 小时
- 绝对截止时间持久化，Web 服务恢复时继续计时或补执行逾期关闭
- 自动接管从命令行或其他途径启动的游戏服务
- 应用日志统一写入 systemd user journal
- 手机优先的响应式简体中文界面

## 安全说明

服务按需求仅提供 HTTP，并监听所有网络接口。HTTP 会明文传输密码和会话 Cookie，不应通过不可信网络访问。初始密码是 `changeme`；请尽快修改 `config.json`，然后重启服务：

```bash
systemctl --user restart gamehelm.service
```

真实 `config.json`、运行状态和二进制均已被 Git 忽略。

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

确保已安装 Go 1.26.5，然后执行安装：

```bash
sudo ./gamehelm.sh
```

默认访问地址为 `http://服务器IP:8231`，示例配置的初始密码是 `changeme`。

安装脚本会创建或更新：

- `~/.config/systemd/user/gamehelm.service`：GameHelm 的 user service 定义，设置工作目录、启动命令和异常重启策略；脚本会启用并立即启动该服务。
- `/var/lib/systemd/linger/<用户>`：`loginctl enable-linger` 创建的标记，使该用户未登录时 user services 仍能开机运行；卸载 GameHelm 时会保留。
- `gamehelm`：由源码构建的可执行文件，保存在项目目录。
- `config.json`、`state.json`：实际配置和计时状态，保存在项目目录且不提交到 Git。

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
- `state.json`：各项服务的持久化截止时间，不纳入 Git
- `cmd/gamehelm/`：命令行入口
- `internal/app/`：配置、认证、服务控制、状态和 Web 页面
- `deploy/gamehelm.service`：使用非敏感占位符的 systemd unit 模板
- `tests/`：配置、控制器和 HTTP 集成测试

如果管理员主动停止 `gamehelm.service`，运行中的游戏服务不会在控制台停机期间准时关闭；控制台再次启动后会立即补执行已经逾期的关闭操作。
