#!/usr/bin/env bash
set -euo pipefail

# 默认把项目目录属主作为 GameHelm 和游戏服务的运行用户。
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
PROJECT_DIR=${GAMEHELM_DIR:-$SCRIPT_DIR}
RUN_USER=${GAMEHELM_USER:-$(stat -c '%U' "$PROJECT_DIR")}
RELEASE_REPOSITORY=vatebur/gamehelm
RELEASE_VERSION=${GAMEHELM_VERSION:-latest}
TEMP_DIR=
BINARY_INSTALL_TMP=

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
  if [[ -n $BINARY_INSTALL_TMP ]]; then
    rm -f -- "$BINARY_INSTALL_TMP"
  fi
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

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少安装所需命令：$1" >&2
    exit 1
  fi
}

download_gamehelm() {
  local architecture asset release_base expected_checksum actual_checksum

  if [[ $(uname -s) != Linux ]]; then
    echo "GitHub Releases 二进制仅支持 Linux" >&2
    exit 1
  fi
  case $(uname -m) in
    x86_64|amd64)
      architecture=amd64
      ;;
    aarch64|arm64)
      architecture=arm64
      ;;
    *)
      echo "不支持的 CPU 架构：$(uname -m)（仅支持 amd64 和 arm64）" >&2
      exit 1
      ;;
  esac
  if [[ $RELEASE_VERSION != latest && ! $RELEASE_VERSION =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "无效的 Release 版本：$RELEASE_VERSION" >&2
    exit 1
  fi

  require_command curl
  require_command sha256sum
  TEMP_DIR=$(mktemp -d /tmp/gamehelm-install.XXXXXX)
  asset=gamehelm-linux-$architecture
  if [[ $RELEASE_VERSION == latest ]]; then
    release_base=https://github.com/$RELEASE_REPOSITORY/releases/latest/download
  else
    release_base=https://github.com/$RELEASE_REPOSITORY/releases/download/$RELEASE_VERSION
  fi

  echo "正在下载 GameHelm ${RELEASE_VERSION}（linux/$architecture）..."
  curl --fail --location --silent --show-error --retry 3 \
    --proto '=https' --proto-redir '=https' \
    --output "$TEMP_DIR/$asset" "$release_base/$asset"
  curl --fail --location --silent --show-error --retry 3 \
    --proto '=https' --proto-redir '=https' \
    --output "$TEMP_DIR/SHA256SUMS" "$release_base/SHA256SUMS"

  expected_checksum=$(awk -v asset="$asset" \
    '$2 == asset || $2 == "*" asset { print $1; exit }' "$TEMP_DIR/SHA256SUMS")
  if [[ ! $expected_checksum =~ ^[[:xdigit:]]{64}$ ]]; then
    echo "SHA256SUMS 中未找到有效的 $asset 校验值" >&2
    exit 1
  fi
  actual_checksum=$(sha256sum "$TEMP_DIR/$asset")
  actual_checksum=${actual_checksum%% *}
  if [[ ${actual_checksum,,} != ${expected_checksum,,} ]]; then
    echo "$asset 的 SHA-256 校验失败" >&2
    exit 1
  fi

  # 先写入同一目录的临时文件，再原子替换正在使用的旧版本。
  BINARY_INSTALL_TMP=$PROJECT_DIR/.gamehelm.install.$$
  install -o "$RUN_USER" -g "$RUN_GROUP" -m 0755 \
    "$TEMP_DIR/$asset" "$BINARY_INSTALL_TMP"
  mv -f -- "$BINARY_INSTALL_TMP" "$PROJECT_DIR/gamehelm"
  BINARY_INSTALL_TMP=
}

install_gamehelm() {
  require_root install
  load_run_user

  cd "$PROJECT_DIR"
  download_gamehelm

  # 首次安装复制示例配置；已有配置始终由管理员维护。
  if [[ ! -f config.json ]]; then
    cp config.example.json config.json
  fi
  chmod 0600 config.json
  chown "$RUN_USER:$RUN_GROUP" "$PROJECT_DIR" config.json gamehelm

  # linger 让 user services 在无人登录时仍可开机运行。
  loginctl enable-linger "$RUN_USER"
  ensure_user_manager

  # 配置中的所有游戏 unit 必须已经存在于同一 user manager。
  run_as_user "$PROJECT_DIR/gamehelm" -config "$PROJECT_DIR/config.json" -check

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
    "$PROJECT_DIR/config.json"

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
