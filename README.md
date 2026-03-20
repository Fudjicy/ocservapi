# ocservapi

`ocservapi` — central control plane для управления `ocserv` endpoint'ами. Система проектируется как модульный монолит на Go с PostgreSQL, локальным файловым хранилищем и обязательной локальной консолью `occtl`.

> В репозитории уже есть рабочие бинарники `ocservapi` и `occtl`. Sprint 1 переводит основное хранилище на PostgreSQL: приложение само создаёт схему SQL-миграциями, хранит `system_instance`, пользователей, сессии, endpoint access, deployments и audit в PostgreSQL, а `occtl` продолжает работать через те же API и локальный shell/TUI.

## Что должно входить в систему

- `ocservapi` — основной backend/API.
- `PostgreSQL` — источник истины для логического состояния установки.
- локальное файловое хранилище — сертификаты, ключи, шаблоны, артефакты деплоя.
- `occtl` — локальная CLI/TUI-консоль для работы через API.

## Ключевые требования

- БД является источником истины для logical identity системы.
- Повторный запуск не должен пересоздавать installation identity.
- Миграции должны применяться безопасно и только вперёд.
- `occtl` обязан уметь проверять состояние системы локально, без web UI и внешних мессенджеров.
- Права администратора ограничиваются только назначенными endpoint'ами.

---

# Установка и первый запуск

Ниже — рекомендуемая последовательность установки на Ubuntu 24.04.

## 1. Установка PostgreSQL

Сначала установите PostgreSQL на central-сервере:

```bash
sudo apt update
sudo apt install -y postgresql postgresql-client
```

Проверьте, что сервис запущен:

```bash
sudo systemctl enable --now postgresql
sudo systemctl status postgresql
```

## 2. Создание БД и пользователя для `ocservapi`

Создайте отдельного пользователя и отдельную базу:

```bash
sudo -u postgres psql
```

Внутри `psql` выполните:

```sql
CREATE ROLE ocservapi_user WITH LOGIN PASSWORD 'change_me_now';
CREATE DATABASE ocservapi OWNER ocservapi_user;
\c ocservapi
GRANT ALL PRIVILEGES ON DATABASE ocservapi TO ocservapi_user;
```

Для быстрой проверки подключения:

```bash
PGPASSWORD='change_me_now' psql \
  -h 127.0.0.1 \
  -U ocservapi_user \
  -d ocservapi \
  -c 'select current_database(), current_user;'
```

## 3. Подготовка каталогов приложения

Создайте системные каталоги для конфигурации, данных и ключей:

```bash
sudo mkdir -p /etc/ocservapi
sudo mkdir -p /var/lib/ocservapi
sudo mkdir -p /var/lib/ocservapi/pki
sudo mkdir -p /var/lib/ocservapi/artifacts
sudo mkdir -p /var/log/ocservapi
```

Рекомендуемые права:

```bash
sudo chown -R root:root /etc/ocservapi
sudo chmod 0755 /etc/ocservapi
sudo chmod -R 0750 /var/lib/ocservapi
```

## 4. Создание master key

Master key нужен для шифрования содержимого таблицы `secrets`.

Пример генерации ключа:

```bash
sudo sh -c 'umask 077 && openssl rand -hex 32 > /etc/ocservapi/master.key'
sudo chmod 0600 /etc/ocservapi/master.key
```

Проверьте права:

```bash
stat -c '%a %n' /etc/ocservapi/master.key
```

Ожидаемое значение прав — `600`.

## 5. Создание bootstrap-конфига

Создайте файл `/etc/ocservapi/config.yaml`:

```yaml
postgres:
  dsn: "postgres://ocservapi_user:change_me_now@127.0.0.1:5432/ocservapi?sslmode=disable"

server:
  listen: "127.0.0.1:8080"

storage:
  data_dir: "/var/lib/ocservapi"
  master_key_path: "/etc/ocservapi/master.key"

bootstrap:
  display_name: "ocservapi"
  owner_username: "owner"
  owner_password: "change_me_owner_password"

logging:
  level: "info"
```

Что важно:

- bootstrap-конфиг используется только для старта приложения;
- runtime-настройки и installation identity хранятся в PostgreSQL;
- bootstrap owner создаётся только при первом старте пустой базы;
- bootstrap-конфиг не должен перезаписывать runtime-настройки существующей установки.

## 6. Сборка бинарей

Соберите backend и локальную консоль из текущего репозитория:

```bash
go build -o bin/ocservapi ./cmd/ocservapi
go build -o bin/occtl ./cmd/occtl
```

После сборки будут доступны два бинаря: `bin/ocservapi` и `bin/occtl`.

## 7. Первый запуск `ocservapi`

На первом запуске приложение должно:

1. прочитать bootstrap-конфиг;
2. подключиться к PostgreSQL;
3. проверить доступность БД;
4. автоматически применить SQL-миграции;
5. создать `system_instance`;
6. создать bootstrap owner с паролем;
7. завершить инициализацию без destructive-операций.

Пример запуска:

```bash
./bin/ocservapi --config /etc/ocservapi/config.yaml
```

Или через systemd unit после подготовки сервиса.

## 8. Повторный запуск

Повторный запуск должен:

- сохранить существующий `installation_id`;
- применять только недостающие SQL-миграции;
- не пересоздавать owner поверх рабочей установки;
- не делать destructive reset и повторный seed.

---

# Проверка установки

После старта нужно убедиться, что backend подключён к правильной БД, installation identity существует, а локальная консоль видит систему.

## 1. Базовая проверка PostgreSQL

Проверьте, что backend использует нужную базу:

```bash
PGPASSWORD='change_me_now' psql \
  -h 127.0.0.1 \
  -U ocservapi_user \
  -d ocservapi \
  -c '\dt'
```

После первого старта должны появиться системные таблицы, включая `system_instance`, `users`, `sessions`, `endpoints`, `endpoint_admin_access`, `deployments` и `audit_events`.

## 2. Проверка logical identity

Проверьте наличие installation identity через CLI:

```bash
./bin/occtl --api http://127.0.0.1:8080 auth login --username owner --password change_me_owner_password
./bin/occtl --api http://127.0.0.1:8080 system info
```

Что это подтверждает:

- система инициализирована;
- installation identity создана и доступна через API;
- при повторном запуске приложение продолжает использовать ту же identity.

## 3. Проверка доступности API

Если backend слушает HTTP API локально:

```bash
curl -fsS http://127.0.0.1:8080/health
```

Если health endpoint отличается, используйте фактический URL проекта.

## 4. Обязательная проверка через `occtl`

Минимальный набор команд локальной проверки должен выглядеть так:

```bash
./bin/occtl --api http://127.0.0.1:8080 auth login --username owner --password change_me_owner_password
./bin/occtl --api http://127.0.0.1:8080 system info
./bin/occtl --api http://127.0.0.1:8080 endpoint list
./bin/occtl --api http://127.0.0.1:8080 auth whoami
./bin/occtl --api http://127.0.0.1:8080 deployment list
./bin/occtl --api http://127.0.0.1:8080 audit list
```

Через эти команды администратор должен иметь возможность убедиться, что система рабочая и отвечает через API.

## 5. Проверка TUI / локального shell

Для интерактивной локальной проверки запустите:

```bash
./bin/occtl shell
```

или:

```bash
./bin/occtl tui
```

На стартовом экране обязательно должны отображаться:

- имя системы;
- installation ID;
- адрес API;
- статус подключения к API;
- статус подключения backend к БД;
- число доступных endpoint'ов;
- текущий пользователь;
- его роль.

## 6. Проверка раздела `System info`

В `occtl system info` или в одноимённом экране TUI должны отображаться:

- installation ID;
- display name системы;
- текущая версия приложения;
- версия схемы/миграций;
- DSN в безопасном виде без пароля;
- путь к файловому хранилищу;
- наличие master key;
- количество endpoint'ов;
- количество admins;
- при необходимости — число записей audit и deployments.

---

# Документация по переносу установки

Цель переноса: переместить backend на новый сервер без потери logical identity.

## Что нужно перенести

- PostgreSQL базу `ocservapi`;
- каталог данных `/var/lib/ocservapi`;
- bootstrap-конфиг `/etc/ocservapi/config.yaml`;
- master key `/etc/ocservapi/master.key`;
- TLS-материалы API, если они используются отдельно.

## Что важно проверить после переноса

1. новый экземпляр приложения подключается к перенесённой БД;
2. приложение видит существующую запись в `system_instance`;
3. installation ID остался прежним;
4. runtime-настройки подхватываются из БД;
5. приложение не выполняет destructive init и не создаёт новую installation identity.

---

# Резервное копирование

## Что обязательно бэкапить

- PostgreSQL базу;
- файловое хранилище `/var/lib/ocservapi`;
- bootstrap-конфиг;
- master key.

## Пример резервного копирования БД

```bash
pg_dump -Fc \
  -h 127.0.0.1 \
  -U ocservapi_user \
  -d ocservapi \
  -f ocservapi.dump
```

## Пример резервного копирования файлового хранилища

```bash
sudo tar -C /var/lib -czf ocservapi-files.tar.gz ocservapi
```

## Критически важное замечание

Бэкап БД без файлового хранилища и master key недостаточен для полного восстановления рабочей системы, особенно если используются сертификаты, ключи и зашифрованные секреты.

---

# Минимальный чек-лист приёмки

Система соответствует базовому сценарию, если:

- приложение поднимается на пустой PostgreSQL;
- миграции применяются автоматически;
- повторный запуск не ломает существующие данные;
- installation identity не пересоздаётся;
- `occtl` позволяет проверить system info, endpoint list, audit и deployments;
- локальная консоль работает как CLI и как TUI.

---

# Что уже реализовано в репозитории

В текущем состоянии доступны:

- backend `ocservapi` с HTTP API (`/health`, `/auth/login`, `/auth/whoami`, `/system/info`, `/endpoints`, `/deployments`, `/audit`, `/access`);
- локальный клиент `occtl` с командами `auth login`, `auth whoami`, `system info`, `endpoint list`, `deployment list`, `audit list`;
- интерактивный локальный режим `occtl shell` / `occtl tui`;
- автоматическое создание SQL-схемы в PostgreSQL на пустой базе;
- таблица `system_instance` с устойчивым `installation_id`;
- bootstrap owner-пользователь с password auth;
- безопасный вывод DSN без пароля в `system info`.

# Следующие шаги разработки

Для дальнейшего сближения с полной технической спецификацией стоит добавить:

- усиление password storage до отдельного криптографического модуля / KDF-провайдера;
- расширение PostgreSQL store новыми CRUD-операциями для endpoint management;
- REST-операции создания/изменения endpoint'ов и управления доступом;
- отдельные PKI-провайдеры для server/client CA;
- managed SSH/deploy executor и rollback flow;
- расширение TUI до полноэкранной навигации со списками и подтверждениями опасных действий.
