# Развёртывание

Сервер: VDS с Ubuntu 24.04, не меньше 4 vCPU, 8 ГБ ОЗУ и 80 ГБ диска. Каталог системы:
`/srv/freightflow`. Службы: Caddy (HTTPS), приложение, фоновые задачи, PostgreSQL, Valhalla,
служба PDF. Наружу открыты только порты 22, 80 и 443.

Файлы сервера — в `deploy/prod/`: `compose.yaml`, `Caddyfile`, образец настроек `env.example`,
скрипты в `scripts/`. Команды с машины разработки выполняются в Git Bash из корня репозитория;
`<IP>` — адрес сервера.

## 1. Ключ SSH и DNS

1. Создать ключ на своей машине, если его ещё нет: `ssh-keygen -t ed25519`. Открытую часть
   (`~/.ssh/id_ed25519.pub`) добавить в панели хостинга при создании сервера или, если хостинг
   выдал только пароль root, один раз (пароль вводится вручную):
   `cat ~/.ssh/id_ed25519.pub | ssh root@<IP> "mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys"`.
2. У регистратора домена: A-записи `freightflow.ru` и `www.freightflow.ru` → `<IP>`.

## 2. Настройка сервера (один раз)

```bash
scp deploy/prod/scripts/setup-server.sh root@<IP>:/root/
ssh root@<IP> 'bash /root/setup-server.sh'
ssh deploy@<IP> 'docker ps'                 # вход под deploy работает
ssh root@<IP> 'bash /root/setup-server.sh harden-ssh'
```

После `harden-ssh` вход только по ключу и только под `deploy`. У `deploy` нет `sudo`:
перезагрузка и другие действия root — из консоли и кнопок панели хостинга.

## 3. Настройки

```bash
scp deploy/prod/env.example deploy@<IP>:/srv/freightflow/.env
ssh deploy@<IP>
cd /srv/freightflow
for v in FF_DB_PASSWORD FF_SESSION_KEY FF_ENCRYPTION_KEY; do sed -i "s/^$v=$/$v=$(openssl rand -hex 32)/" .env; done
nano .env    # почта, ключ портала Москвы; chmod 600 выставит выкладка
```

Ключ `FF_ENCRYPTION_KEY` после первого входа пользователей не менять: им зашифрованы секреты 2FA.

## 4. Первая выкладка и данные

```bash
deploy/prod/scripts/release.sh deploy@<IP>     # сборка, образы, миграции, запуск
deploy/prod/scripts/push-data.sh deploy@<IP>   # база и var с машины разработки
```

`push-data.sh` заменяет базу на сервере целиком и удаляет из неё учётные записи разработки.
Повторно его запускают только для полной замены данных.

## 5. Учётная запись администратора

```bash
ssh -t deploy@<IP> 'cd /srv/freightflow && docker compose run --rm app admin create-user -email ВАША@ПОЧТА -name "Имя"'
```

Пароль вводится в ответ на запрос. При первом входе на https://freightflow.ru система попросит
подключить двухфакторную защиту (Яндекс Ключ, Google Authenticator) и выдаст резервные коды.
Остальных пользователей — из раздела «Пользователи»: «Пригласить» (ссылка действует 72 часа,
её можно скопировать) или «Создать».

Демонстрационный набор для учётной записи (виды, дашборд, сценарии, отчёты):

```bash
ssh deploy@<IP> 'cd /srv/freightflow && docker compose exec app freightflow demo -email ПОЧТА@УЧЁТКИ'
```

## 6. Резервные копии

```bash
ssh deploy@<IP> '(crontab -l 2>/dev/null; echo "30 3 * * * /srv/freightflow/backup.sh >>/srv/freightflow/backups/backup.log 2>&1") | crontab -'
```

Копии базы хранятся 7 дней в `/srv/freightflow/backups`. Восстановление:
`bash /srv/freightflow/transfer/server-restore.sh backups/freightflow-ГГГГММДД-ЧЧММ.dump`.
Копии лежат на том же сервере: для защиты от потери сервера их стоит периодически забирать
(`scp deploy@<IP>:/srv/freightflow/backups/*.dump ...`).

## Обновление и откат

```bash
deploy/prod/scripts/release.sh deploy@<IP>                               # новая версия (только закоммиченное)
ssh deploy@<IP> 'bash /srv/freightflow/server-release.sh rollback'      # вернуть предыдущий образ
```

Откат не возвращает миграции базы: при несовместимых изменениях схемы — восстановление из копии.

## Обслуживание

```bash
ssh deploy@<IP>
cd /srv/freightflow
docker compose ps                       # состояние служб
docker compose logs -f --tail 100 app   # журнал приложения (worker, caddy, db, routing)
docker compose restart app              # перезапуск службы
cat RELEASE                             # выложенная версия
docker stats --no-stream               # память и процессор служб
```

Проверка доступности российских источников с сервера:
`for u in https://apidata.mos.ru https://data.mos.ru; do curl -s -o /dev/null -w "$u %{http_code}\n" --max-time 15 $u; done`.
Если источники недоступны (сервер вне РФ, так у freightflow.ru), их плановые загрузки на сервере
выключены (`cleanup.sql`: `ingest.source.enabled = false` для `ru_network`), загрузки выполняются
на машине разработки. Перенос `push-data.sh` заменяет базу целиком: учётные записи сервера после
него создаются заново.
