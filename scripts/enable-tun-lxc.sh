#!/usr/bin/env bash
#
# enable-tun-lxc.sh — пробрасывает /dev/net/tun в LXC-контейнер.
# Запускать НА ХОСТЕ (Proxmox или чистый LXC), от root.
#
# Использование:
#   ./enable-tun-lxc.sh [CTID] [--no-restart] [--docker] [--apparmor=nesting|unconfined]
#
# Если CTID не указан — скрипт спросит его интерактивно (и покажет список).
#
# Флаги:
#   --no-restart              только правит конфиг, не перезапускает контейнер
#   --docker                  вывести конфиг для проброса TUN в Docker внутри LXC
#   --apparmor=nesting        профиль lxc-container-default-with-nesting (мягко)
#   --apparmor=unconfined     снять AppArmor полностью (МЕНЬШЕ ИЗОЛЯЦИИ)
#
# Примеры:
#   ./enable-tun-lxc.sh 105
#   ./enable-tun-lxc.sh 105 --docker
#   ./enable-tun-lxc.sh 105 --apparmor=nesting
#
set -euo pipefail

# --- Разбор аргументов ------------------------------------------------------
CTID=""
NO_RESTART=0
DOCKER=0
APPARMOR=""          # "", "nesting" или "unconfined"
for arg in "$@"; do
    case "$arg" in
        --no-restart)          NO_RESTART=1 ;;
        --docker)              DOCKER=1 ;;
        --apparmor=nesting)    APPARMOR="nesting" ;;
        --apparmor=unconfined) APPARMOR="unconfined" ;;
        --apparmor=*)          echo "Неверное значение --apparmor: '${arg#*=}' (nesting|unconfined)" >&2; exit 1 ;;
        -*)                    echo "Неизвестный флаг: $arg" >&2; exit 1 ;;
        *)                     CTID="$arg" ;;
    esac
done

if [[ "$(id -u)" -ne 0 ]]; then
    echo "Нужны права root. Запусти через sudo." >&2
    exit 1
fi

# --- Интерактивный ввод CTID, если не указан -------------------------------
# Читаем/пишем через /dev/tty, чтобы работало и при запуске через `curl | bash`
# (когда stdin занят телом скрипта).
if [[ -z "$CTID" ]]; then
    if [[ ! -e /dev/tty ]]; then
        echo "CTID не указан и нет терминала для интерактивного ввода." >&2
        echo "Использование: $0 <CTID> [--no-restart] [--docker] [--apparmor=...]" >&2
        exit 1
    fi
    echo "Доступные контейнеры:" > /dev/tty
    if command -v pct >/dev/null 2>&1; then
        pct list > /dev/tty 2>/dev/null || true
    elif command -v lxc-ls >/dev/null 2>&1; then
        lxc-ls -f > /dev/tty 2>/dev/null || true
    fi
    printf "Введите CTID контейнера: " > /dev/tty
    read -r CTID < /dev/tty
    if [[ -z "$CTID" ]]; then
        echo "CTID не введён — выход." >&2
        exit 1
    fi
fi

# --- Находим конфиг контейнера (Proxmox vs чистый LXC) ---------------------
if [[ -f "/etc/pve/lxc/${CTID}.conf" ]]; then
    CONF="/etc/pve/lxc/${CTID}.conf"
    FLAVOR="proxmox"
elif [[ -f "/var/lib/lxc/${CTID}/config" ]]; then
    CONF="/var/lib/lxc/${CTID}/config"
    FLAVOR="lxc"
else
    echo "Не нашёл конфиг контейнера '${CTID}'." >&2
    echo "Проверял: /etc/pve/lxc/${CTID}.conf и /var/lib/lxc/${CTID}/config" >&2
    exit 1
fi
echo "[*] Конфиг: $CONF (тип: $FLAVOR)"

# --- Определяем unprivileged ------------------------------------------------
# Proxmox: строка 'unprivileged: 1'. Чистый LXC: наличие lxc.idmap.
UNPRIV=0
if [[ "$FLAVOR" == "proxmox" ]]; then
    grep -qE '^unprivileged:\s*1' "$CONF" && UNPRIV=1
else
    grep -qE '^\s*lxc\.idmap' "$CONF" && UNPRIV=1
fi
if [[ "$UNPRIV" -eq 1 ]]; then
    echo "[*] Контейнер: UNPRIVILEGED"
else
    echo "[*] Контейнер: privileged"
fi

# --- Определяем версию cgroup ----------------------------------------------
if [[ -f /sys/fs/cgroup/cgroup.controllers ]]; then
    ALLOW_KEY="lxc.cgroup2.devices.allow"   # cgroup v2 (современные ядра)
else
    ALLOW_KEY="lxc.cgroup.devices.allow"    # cgroup v1
fi
echo "[*] cgroup ключ: $ALLOW_KEY"

ALLOW_LINE="${ALLOW_KEY}: c 10:200 rwm"
MOUNT_LINE="lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file"

# --- Хелперы правки конфига -------------------------------------------------
add_line() {
    local line="$1"
    if grep -qF -- "$line" "$CONF"; then
        echo "[=] Уже есть: $line"
    else
        echo "$line" >> "$CONF"
        echo "[+] Добавлено: $line"
    fi
}

# Устанавливает lxc.apparmor.profile, заменяя существующую строку если есть.
set_apparmor() {
    local prof="$1"
    local line="lxc.apparmor.profile: $prof"
    if grep -qE '^\s*lxc\.apparmor\.profile' "$CONF"; then
        sed -i -E "s|^[[:space:]]*lxc\.apparmor\.profile.*|$line|" "$CONF"
        echo "[~] AppArmor профиль обновлён: $prof"
    else
        echo "$line" >> "$CONF"
        echo "[+] Добавлено: $line"
    fi
}

# бэкап на всякий случай
cp -a "$CONF" "${CONF}.bak.$(date +%Y%m%d%H%M%S)"

add_line "$ALLOW_LINE"
add_line "$MOUNT_LINE"

# --- AppArmor: применяем по флагу, иначе только предупреждаем ---------------
if [[ -n "$APPARMOR" ]]; then
    case "$APPARMOR" in
        nesting)    set_apparmor "lxc-container-default-with-nesting" ;;
        unconfined) set_apparmor "unconfined"
                    echo "[!] AppArmor снят полностью — изоляция контейнера снижена." ;;
    esac
elif [[ "$UNPRIV" -eq 1 ]]; then
    cat <<'EOF'

[!] ВНИМАНИЕ: unprivileged-контейнер.
    Профиль AppArmor по умолчанию (lxc-container-default-cgns) может блокировать
    создание TUN-интерфейса даже при проброшенном /dev/net/tun.
    Если после запуска awg-quick всё ещё падает с "Operation not permitted" —
    перезапусти скрипт с флагом:

      --apparmor=nesting      # мягко: lxc-container-default-with-nesting
      --apparmor=unconfined   # жёстко: снять AppArmor (МЕНЬШЕ ИЗОЛЯЦИИ)

    Без флага скрипт AppArmor НЕ трогает. Проброшенного TUN часто достаточно.
EOF
fi

# --- Docker: подсказка для проброса внутрь докера --------------------------
print_docker_help() {
    cat <<'EOF'

[docker] Панель гоняет AWG в Docker ВНУТРИ LXC?
    Проброса в LXC мало — самому docker-контейнеру тоже нужен TUN и NET_ADMIN.

    docker run:
      --device /dev/net/tun:/dev/net/tun \
      --cap-add NET_ADMIN \
      --sysctl net.ipv4.ip_forward=1

    docker-compose.yml (в описании сервиса):
      cap_add:
        - NET_ADMIN
      devices:
        - /dev/net/tun:/dev/net/tun
      sysctls:
        - net.ipv4.ip_forward=1

    После правки compose: docker compose up -d --force-recreate
EOF
}

# --- Перезапуск контейнера -------------------------------------------------
if [[ "$NO_RESTART" -eq 1 ]]; then
    echo
    echo "[✓] Конфиг обновлён (--no-restart, контейнер не трогаю):"
    echo "      $ALLOW_LINE"
    echo "      $MOUNT_LINE"
    [[ -n "$APPARMOR" ]] && echo "      lxc.apparmor.profile: $APPARMOR (см. точное имя в конфиге)"
    echo
    echo "[!] Правки уже в конфиге, но применятся только при следующем старте контейнера."
    echo "    Профиль AppArmor меняется исключительно на старте — live-перезагрузки для него нет."
    echo "    Применить:"
    if [[ "$FLAVOR" == "proxmox" ]]; then
        echo "      pct stop $CTID && pct start $CTID"
    else
        echo "      lxc-stop -n $CTID && lxc-start -n $CTID"
    fi
    [[ "$DOCKER" -eq 1 ]] && print_docker_help
    exit 0
fi

echo "[*] Перезапускаю контейнер $CTID..."
if [[ "$FLAVOR" == "proxmox" ]]; then
    pct stop "$CTID" || true
    pct start "$CTID"
    RUN() { pct exec "$CTID" -- "$@"; }
else
    lxc-stop -n "$CTID" || true
    lxc-start -n "$CTID"
    RUN() { lxc-attach -n "$CTID" -- "$@"; }
fi

# ждём пока поднимется
sleep 3

# --- Проверка внутри контейнера --------------------------------------------
echo "[*] Проверяю /dev/net/tun внутри контейнера..."
RUN sh -c 'mkdir -p /dev/net; [ -c /dev/net/tun ] || mknod /dev/net/tun c 10 200; chmod 600 /dev/net/tun; ls -l /dev/net/tun'

# --- Docker: детект и подсказка --------------------------------------------
# Если докер реально есть внутри — сразу печатаем конфиг проброса, без второго
# запуска. Флаг --docker нужен лишь чтобы форсить вывод (напр. при --no-restart,
# когда контейнер не поднят и задетектить docker нельзя).
if [[ "$DOCKER" -eq 1 ]] || RUN sh -c 'command -v docker >/dev/null 2>&1'; then
    echo
    echo "[i] Внутри контейнера обнаружен Docker (или указан --docker)."
    print_docker_help
fi

echo
echo "[✓] Готово. TUN проброшен. Запускай панель / awg-quick up внутри контейнера."
