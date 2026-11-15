#!/bin/bash
# Первичная настройка чистого сервера Ubuntu 24.04. Запускать от root один раз:
#   bash setup-server.sh            — пакеты, Docker, пользователь deploy, межсетевой экран, fail2ban
#   bash setup-server.sh harden-ssh — после проверки входа под deploy по ключу: вход только по ключу,
#                                     root по SSH запрещён
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
	echo "запускать от root" >&2
	exit 1
fi

if [ "${1:-}" = "harden-ssh" ]; then
	test -s /home/deploy/.ssh/authorized_keys || { echo "у deploy нет ключа: вход по паролю не отключаю" >&2; exit 1; }
	printf '%s\n' 'PasswordAuthentication no' 'KbdInteractiveAuthentication no' 'PermitRootLogin no' \
		>/etc/ssh/sshd_config.d/10-freightflow.conf
	sshd -t
	systemctl reload ssh
	echo "SSH: вход только по ключу, root запрещён"
	exit 0
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get -y upgrade
apt-get install -y --no-install-recommends \
	docker.io docker-compose-v2 docker-buildx ufw fail2ban unattended-upgrades ca-certificates curl openssl
systemctl enable --now docker

timedatectl set-timezone Europe/Moscow

# Подкачка 2 ГБ: страховка от остановки служб по памяти при импорте и сборке. Образы хостинга
# бывают с подкачкой 512 МБ — она пересоздаётся.
if [ "$(swapon --show=SIZE --bytes --noheadings 2>/dev/null | head -1)" -lt 2000000000 ] 2>/dev/null; then
	swapoff /swapfile 2>/dev/null || true
	rm -f /swapfile
	sed -i '\|^/swapfile |d' /etc/fstab
fi
if ! swapon --show | grep -q /swapfile; then
	fallocate -l 2G /swapfile
	chmod 600 /swapfile
	mkswap /swapfile
	swapon /swapfile
	echo '/swapfile none swap sw 0 0' >>/etc/fstab
	echo 'vm.swappiness=10' >/etc/sysctl.d/90-swap.conf
	sysctl --system >/dev/null
fi

# Пользователь для выкладки: ключи SSH — те же, что у root.
if ! id deploy >/dev/null 2>&1; then
	useradd --create-home --shell /bin/bash --groups docker deploy
fi
install -d -m 700 -o deploy -g deploy /home/deploy/.ssh
if [ -s /root/.ssh/authorized_keys ]; then
	install -m 600 -o deploy -g deploy /root/.ssh/authorized_keys /home/deploy/.ssh/authorized_keys
fi

# Каталог системы. var доступен пользователю контейнеров (uid 10001) и deploy.
install -d -o deploy -g deploy /srv/freightflow /srv/freightflow/build /srv/freightflow/backups /srv/freightflow/transfer
install -d -o 10001 -g deploy -m 2775 /srv/freightflow/var

# Межсетевой экран: SSH, HTTP, HTTPS. Порты контейнеров наружу открывает только Caddy.
ufw default deny incoming
ufw default allow outgoing
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp
ufw --force enable

# fail2ban: блокировка адреса на час после 5 неудачных входов по SSH за 10 минут.
printf '%s\n' '[sshd]' 'enabled = true' 'backend = systemd' 'maxretry = 5' 'findtime = 10m' 'bantime = 1h' \
	>/etc/fail2ban/jail.d/sshd.local
systemctl enable fail2ban
systemctl restart fail2ban

# Автоматические обновления безопасности.
dpkg-reconfigure -f noninteractive unattended-upgrades

echo
echo "Готово. Проверьте вход: ssh deploy@<адрес сервера>, затем: bash setup-server.sh harden-ssh"
