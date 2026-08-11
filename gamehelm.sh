#!/usr/bin/env bash
set -euo pipefail

# 默认把项目目录属主作为 GameHelm 和游戏服务的运行用户。
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PROJECT_DIR=${GAMEHELM_DIR:-$SCRIPT_DIR}
RUN_USER=${GAMEHELM_USER:-$(stat -c '%U' "$PROJECT_DIR")}
EXPECTED_GO='go version go1.26.5 '
TEMP_DIR=

RUN_GROUP=
RUN_UID=
RUN_HOME=
RUNTIME_DIR=
USER_UNIT_DIR=
SERVICE_FILE=

usage() {
  cat <<'EOF'
用法：sudo ./gamehelm.sh [install|uninstall]

  install     安装并启动 GameHelm user service（默认）
  uninstall   卸载 GameHelm，保留游戏 user services 和 linger
EOF
}

cleanup_temp() {
  if [[ -n $TEMP_DIR && -d $TEMP_DIR ]]; then
    rm -rf -- "$TEMP_DIR"
  fi
}
trap cleanup_temp EXIT

require_root() {
  if [[ ${EUID} -ne 0 ]]; then
    echo "请使用 root 权限运行：sudo ./gamehelm.sh $1" >&2
    exit 1
  fi
}

load_run_user() {
  local passwd_entry passwd_name passwd_value passwd_gid passwd_gecos passwd_shell
  if ! passwd_entry=$(getent passwd "$RUN_USER"); then
    echo "运行用户不存在：$RUN_USER" >&2
    exit 1
  fi
  IFS=: read -r passwd_name passwd_value RUN_UID passwd_gid passwd_gecos RUN_HOME passwd_shell <<<"$passwd_entry"
  if [[ $RUN_UID -eq 0 ]]; then
    echo "GameHelm 不允许使用 root 作为运行用户" >&2
    exit 1
  fi
  if [[ ! -d $RUN_HOME ]]; then
    echo "运行用户 home 目录不存在：$RUN_HOME" >&2
    exit 1
  fi
  RUN_GROUP=$(id -gn "$RUN_USER")
  RUNTIME_DIR=/run/user/$RUN_UID
  USER_UNIT_DIR=$RUN_HOME/.config/systemd/user
  SERVICE_FILE=$USER_UNIT_DIR/gamehelm.service
}

run_as_user() {
  runuser -u "$RUN_USER" -- env \
    "HOME=$RUN_HOME" \
    "XDG_RUNTIME_DIR=$RUNTIME_DIR" \
    "DBUS_SESSION_BUS_ADDRESS=unix:path=$RUNTIME_DIR/bus" \
    "$@"
}

user_systemctl() {
  run_as_user /usr/bin/systemctl --user "$@"
}

ensure_user_manager() {
  systemctl start "user@$RUN_UID.service"
  local attempt
  for attempt in {1..50}; do
    if [[ -S $RUNTIME_DIR/bus ]]; then
      return
    fi
    sleep 0.1
  done
  echo "用户 systemd manager 未就绪：$RUN_USER" >&2
  exit 1
}

escape_sed_replacement() {
  printf '%s' "$1" | sed 's/[&|\\]/\\&/g'
}

install_gamehelm() {
  require_root install
  load_run_user

  # 使用固定工具链构建，避免部署出不可复现的二进制。
  if [[ $(go version) != "$EXPECTED_GO"* ]]; then
    echo "需要 Go 1.26.5" >&2
    exit 1
  fi
  cd "$PROJECT_DIR"
  go build -buildvcs=false -trimpath -ldflags='-s -w' -o gamehelm ./cmd/gamehelm

  # 首次安装复制示例配置；已有配置始终由管理员维护。
  if [[ ! -f config.json ]]; then
    cp config.example.json config.json
  fi
  chmod 0600 config.json
  chmod 0755 gamehelm
  chown "$RUN_USER:$RUN_GROUP" "$PROJECT_DIR" config.json gamehelm

  # linger 让 user services 在无人登录时仍可开机运行。
  loginctl enable-linger "$RUN_USER"
  ensure_user_manager

  # 配置中的所有游戏 unit 必须已经存在于同一 user manager。
  run_as_user "$PROJECT_DIR/gamehelm" -config "$PROJECT_DIR/config.json" -check

  TEMP_DIR=$(mktemp -d /tmp/gamehelm-install.XXXXXX)
  local service_tmp=$TEMP_DIR/gamehelm.service
  local project_value
  project_value=$(escape_sed_replacement "$PROJECT_DIR")
  sed -e "s|@PROJECT_DIR@|$project_value|g" deploy/gamehelm.service >"$service_tmp"

  install -d -o "$RUN_USER" -g "$RUN_GROUP" -m 0755 "$USER_UNIT_DIR"
  install -o "$RUN_USER" -g "$RUN_GROUP" -m 0644 "$service_tmp" "$SERVICE_FILE"
  user_systemctl daemon-reload
  run_as_user /usr/bin/systemd-analyze --user verify "$SERVICE_FILE"
  user_systemctl enable --now gamehelm.service

  echo "安装完成：http://127.0.0.1:8231（初始密码：changeme）"
}

uninstall_gamehelm() {
  require_root uninstall
  load_run_user
  ensure_user_manager

  # 只卸载 GameHelm；游戏 user services 和 linger 保持不变。
  user_systemctl disable --now gamehelm.service >/dev/null 2>&1 || true
  rm -f -- "$SERVICE_FILE"
  user_systemctl daemon-reload
  user_systemctl reset-failed gamehelm.service >/dev/null 2>&1 || true

  # 清理 GameHelm 运行文件，保留源码与示例配置。
  rm -f -- \
    "$PROJECT_DIR/gamehelm" \
    "$PROJECT_DIR/config.json" \
    "$PROJECT_DIR/state.json"

  echo "卸载完成：GameHelm user service 和运行文件已清理。"
}

case ${1:-install} in
  install)
    install_gamehelm
    ;;
  uninstall)
    uninstall_gamehelm
    ;;
  -h|--help|help)
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
