# GameHelm（游舵）

GameHelm（游舵）是一个只使用 Go 标准库构建的多游戏服务器 Web 控制台。它通过配置文件统一管理任意数量的 systemd 游戏服务，并为每次启动提供自动关闭、倒计时和续时能力。

## 功能

- 密码登录、浏览器会话、CSRF 防护和登录失败限速
- 通过配置文件管理任意数量的 systemd 游戏服务，允许同时运行
- 每次启动自动计时 4 小时，最后 1 小时可反复续时 1 小时
- 绝对截止时间持久化，Web 服务恢复时继续计时或补执行逾期关闭
- 自动接管从命令行或其他途径启动的游戏服务
- 10 MB 应用日志轮转并保留 5 份
- 手机优先的响应式简体中文界面

## 安全说明

服务按需求仅提供 HTTP，并监听所有网络接口。HTTP 会明文传输密码和会话 Cookie，不应通过不可信网络访问。初始密码是 `changeme`；请尽快修改 `config.json`，然后重启服务：

```bash
sudo systemctl restart gamehelm.service
```

真实 `config.json`、运行状态、日志和二进制均已被 Git 忽略。

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

确认已选择 Go 1.26.5，然后执行：

```bash
sudo ./install.sh
```

脚本会安装：

- `/etc/systemd/system/gamehelm.service`
- `/etc/sudoers.d/gamehelm`

安装脚本会以项目目录属主作为默认运行用户，并从未纳入 Git 的 `config.json` 读取实际 unit，动态生成 systemd 与 sudoers 文件。可通过 `GAMEHELM_USER` 和 `GAMEHELM_DIR` 环境变量覆盖运行用户和安装目录。sudoers 仅允许该用户启动和停止配置中的全部游戏 unit；其余源码、配置、状态、日志和二进制均保留在项目目录中。

`services` 是以稳定服务标识为键的对象，可以按需添加更多服务。每个 unit 必须唯一：

```json
"services": {
  "palworld": { "display_name": "帕鲁世界", "unit": "palworld.service" },
  "terraria": { "display_name": "泰拉瑞亚", "unit": "terraria.service" },
  "factorio": { "display_name": "异星工厂", "unit": "factorio.service" }
}
```

修改服务列表后重新运行 `sudo ./install.sh`，脚本会按配置中的全部 unit 重新生成最小权限 sudoers。

访问地址：`http://服务器地址:8231`

## 文件

- `config.json`：实际配置，明文保存密码，不纳入 Git
- `config.example.json`：可提交的配置示例
- `state.json`：各项服务的持久化截止时间，不纳入 Git
- `cmd/gamehelm/`：命令行入口
- `internal/app/`：配置、认证、服务控制、状态和 Web 页面
- `gamehelm.log`：认证、控制动作和异常日志，不写入 journald
- `deploy/gamehelm.service`：使用非敏感占位符的 systemd unit 模板
- `deploy/gamehelm.sudoers`：使用非敏感占位符的最小命令授权模板
- `tests/`：配置、控制器和 HTTP 集成测试

如果管理员主动停止 `gamehelm.service`，运行中的游戏服务不会在控制台停机期间准时关闭；控制台再次启动后会立即补执行已经逾期的关闭操作。
