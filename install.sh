#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PROJECT_DIR=${GAMEHELM_DIR:-$SCRIPT_DIR}
RUN_USER=${GAMEHELM_USER:-$(stat -c '%U' "$PROJECT_DIR")}
EXPECTED_GO='go version go1.26.5 '

escape_sed_replacement() {
  printf '%s' "$1" | sed 's/[&|\\]/\\&/g'
}

if [[ ${EUID} -ne 0 ]]; then
  echo "请使用 root 权限运行：sudo ./install.sh" >&2
  exit 1
fi

cd "$PROJECT_DIR"
if [[ $(go version) != "$EXPECTED_GO"* ]]; then
  echo "需要 Go 1.26.5。请先运行：vfox use golang@1.26.5" >&2
  exit 1
fi

go test ./...
go build -buildvcs=false -trimpath -ldflags='-s -w' -o gamehelm ./cmd/gamehelm

if [[ ! -f config.json ]]; then
  cp config.example.json config.json
fi
legacy_project_dir=$(dirname "$PROJECT_DIR")/webctrl
legacy_value=$(escape_sed_replacement "$legacy_project_dir")
project_value=$(escape_sed_replacement "$PROJECT_DIR")
sed -i \
  -e "/^[[:space:]]*\"state_file\":/s|$legacy_value/|$project_value/|" \
  -e "/^[[:space:]]*\"log_file\":/s|$legacy_value/|$project_value/|" \
  -e '/^[[:space:]]*"log_file":/s|webctrl\.log|gamehelm.log|' \
  config.json
chmod 0600 config.json
touch gamehelm.log
chmod 0600 gamehelm.log

if ! id "$RUN_USER" >/dev/null 2>&1; then
  echo "运行用户不存在：$RUN_USER" >&2
  exit 1
fi
RUN_GROUP=$(id -gn "$RUN_USER")
mapfile -t GAME_UNITS < <(./gamehelm -config config.json -print-install-values)
if [[ ${#GAME_UNITS[@]} -lt 1 ]]; then
	echo "无法从 config.json 读取游戏 unit" >&2
	exit 1
fi

project_value=$(escape_sed_replacement "$PROJECT_DIR")
user_value=$(escape_sed_replacement "$RUN_USER")
group_value=$(escape_sed_replacement "$RUN_GROUP")
systemctl_commands=
for unit in "${GAME_UNITS[@]}"; do
	if [[ -n $systemctl_commands ]]; then
		systemctl_commands+=", "
	fi
	systemctl_commands+="/usr/bin/systemctl start $unit, /usr/bin/systemctl stop $unit"
done
commands_value=$(escape_sed_replacement "$systemctl_commands")
service_tmp=$(mktemp /tmp/gamehelm-service.XXXXXX)
sudoers_tmp=$(mktemp /tmp/gamehelm-sudoers.XXXXXX)
trap 'rm -f "$service_tmp" "$sudoers_tmp"' EXIT

sed \
  -e "s|@PROJECT_DIR@|$project_value|g" \
  -e "s|@RUN_USER@|$user_value|g" \
  -e "s|@RUN_GROUP@|$group_value|g" \
  deploy/gamehelm.service >"$service_tmp"
sed \
	-e "s|@RUN_USER@|$user_value|g" \
	-e "s|@SYSTEMCTL_COMMANDS@|$commands_value|g" \
	deploy/gamehelm.sudoers >"$sudoers_tmp"

install -m 0644 "$service_tmp" /etc/systemd/system/gamehelm.service
install -m 0440 "$sudoers_tmp" /etc/sudoers.d/gamehelm
visudo -cf /etc/sudoers.d/gamehelm

legacy_service=/etc/systemd/system/webctrl.service
if [[ -f $legacy_service ]] && grep -q 'ExecStart=.*/webctrl -config ' "$legacy_service"; then
	systemctl disable --now webctrl.service
	rm -f "$legacy_service" /etc/sudoers.d/webctrl
	echo "已将旧 webctrl.service 迁移为 gamehelm.service"
fi

chown "$RUN_USER:$RUN_GROUP" "$PROJECT_DIR"
chown "$RUN_USER:$RUN_GROUP" config.json gamehelm.log
chmod 0755 gamehelm

systemctl daemon-reload
systemctl enable --now gamehelm.service
echo "安装完成：http://127.0.0.1:8231（默认密码：changeme）"
