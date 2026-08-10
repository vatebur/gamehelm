# 游戏服务器 Web 控制台

一个只使用 Go 标准库的简体中文控制页面，用于独立启动、倒计时和停止帕鲁世界与泰拉瑞亚 systemd 服务。

## 功能

- 密码登录、浏览器会话、CSRF 防护和登录失败限速
- 独立控制配置文件指定的两个 systemd 游戏服务，允许同时运行
- 每次启动自动计时 4 小时，最后 1 小时可反复续时 1 小时
- 绝对截止时间持久化，Web 服务恢复时继续计时或补执行逾期关闭
- 自动接管从命令行启动的游戏服务
- 10 MB 应用日志轮转并保留 5 份
- 手机优先的响应式简体中文界面

## 安全说明

服务按需求仅提供 HTTP，并监听所有网络接口。HTTP 会明文传输密码和会话 Cookie，不应通过不可信网络访问。初始密码是 `changeme`；请尽快修改 `config.json`，然后重启服务：

```bash
sudo systemctl restart webctrl.service
```

真实 `config.json`、运行状态、日志和二进制均已被 Git 忽略。

## 构建与测试

```bash
source /etc/profile
vfox use golang@1.26.5
go test ./...
go build -o webctrl .
```

## 安装

确认已选择 Go 1.26.5，然后执行：

```bash
sudo ./install.sh
```

脚本会安装：

- `/etc/systemd/system/webctrl.service`
- `/etc/sudoers.d/webctrl`

安装脚本会以项目目录属主作为默认运行用户，并从未纳入 Git 的 `config.json` 读取实际游戏 unit，动态生成 systemd 与 sudoers 文件。可通过 `WEBCTRL_USER` 和 `WEBCTRL_DIR` 环境变量覆盖运行用户和安装目录。sudoers 仅允许该用户启动和停止配置中的两个游戏 unit；其余源码、配置、状态、日志和二进制均保留在项目目录中。

访问地址：`http://服务器地址:8231`

## 文件

- `config.json`：实际配置，明文保存密码，不纳入 Git
- `config.example.json`：可提交的配置示例
- `state.json`：两个服务的持久化截止时间，不纳入 Git
- `webctrl.log`：认证、控制动作和异常日志，不写入 journald
- `deploy/webctrl.service`：使用非敏感占位符的 systemd unit 模板
- `deploy/webctrl.sudoers`：使用非敏感占位符的最小命令授权模板

如果管理员主动停止 `webctrl.service`，运行中的游戏不会在 Web 服务停机期间准时关闭；Web 服务再次启动后会立即补执行已经逾期的关闭操作。
