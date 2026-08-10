#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PROJECT_DIR=${WEBCTRL_DIR:-$SCRIPT_DIR}
RUN_USER=${WEBCTRL_USER:-$(stat -c '%U' "$PROJECT_DIR")}
EXPECTED_GO='go version go1.26.5 '

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
go build -buildvcs=false -trimpath -ldflags='-s -w' -o webctrl .

if [[ ! -f config.json ]]; then
  cp config.example.json config.json
fi
chmod 0600 config.json
touch webctrl.log
chmod 0600 webctrl.log

if ! id "$RUN_USER" >/dev/null 2>&1; then
  echo "运行用户不存在：$RUN_USER" >&2
  exit 1
fi
RUN_GROUP=$(id -gn "$RUN_USER")
mapfile -t GAME_UNITS < <(./webctrl -config config.json -print-install-values)
if [[ ${#GAME_UNITS[@]} -ne 2 ]]; then
  echo "无法从 config.json 读取两个游戏 unit" >&2
  exit 1
fi

escape_sed_replacement() {
  printf '%s' "$1" | sed 's/[&|\\]/\\&/g'
}

project_value=$(escape_sed_replacement "$PROJECT_DIR")
user_value=$(escape_sed_replacement "$RUN_USER")
group_value=$(escape_sed_replacement "$RUN_GROUP")
palworld_value=$(escape_sed_replacement "${GAME_UNITS[0]}")
terraria_value=$(escape_sed_replacement "${GAME_UNITS[1]}")
service_tmp=$(mktemp /tmp/webctrl-service.XXXXXX)
sudoers_tmp=$(mktemp /tmp/webctrl-sudoers.XXXXXX)
trap 'rm -f "$service_tmp" "$sudoers_tmp"' EXIT

sed \
  -e "s|@PROJECT_DIR@|$project_value|g" \
  -e "s|@RUN_USER@|$user_value|g" \
  -e "s|@RUN_GROUP@|$group_value|g" \
  deploy/webctrl.service >"$service_tmp"
sed \
  -e "s|@RUN_USER@|$user_value|g" \
  -e "s|@PALWORLD_UNIT@|$palworld_value|g" \
  -e "s|@TERRARIA_UNIT@|$terraria_value|g" \
  deploy/webctrl.sudoers >"$sudoers_tmp"

install -m 0644 "$service_tmp" /etc/systemd/system/webctrl.service
install -m 0440 "$sudoers_tmp" /etc/sudoers.d/webctrl
visudo -cf /etc/sudoers.d/webctrl

chown "$RUN_USER:$RUN_GROUP" "$PROJECT_DIR"
chown "$RUN_USER:$RUN_GROUP" config.json webctrl.log
chmod 0755 webctrl

systemctl daemon-reload
systemctl enable --now webctrl.service
echo "安装完成：http://127.0.0.1:8231（默认密码：changeme）"
