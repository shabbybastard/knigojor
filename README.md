# knigojor

Персональный Telegram-бот для работы с электронной библиотекой Flibusta.

Текущая версия — Go-реализация. Она собирается в один статический бинарник без
CGO и поддерживает linux/amd64, linux/arm64 и linux/armv7.

## Установка из релиза

Скачайте нужный файл со страницы GitHub Releases:

- `knigojor-linux-amd64`
- `knigojor-linux-arm64`
- `knigojor-linux-arm` (armv7)

```sh
chmod +x knigojor-linux-amd64
sudo install -m 0755 knigojor-linux-amd64 /usr/bin/knigojor
```

Для Debian/Ubuntu и Fedora/RHEL доступны пакеты:

```sh
sudo dpkg -i knigojor_0.1.0_amd64.deb
# или
sudo rpm -i knigojor-0.1.0-1.x86_64.rpm
```

Пакет устанавливает бинарник в `/usr/bin/knigojor` и unit
`/usr/lib/systemd/system/knigojor@.service`.

### Конфигурация

```sh
mkdir -p ~/.knigojor
cp .env.example ~/.knigojor/.env
chmod 600 ~/.knigojor/.env
# отредактируйте BOT_TOKEN, ALLOWED_USER_ID и зеркала
```

### systemd

Если вы устанавливали deb/rpm, unit `knigojor@.service` уже лежит в системе —
шаг с `cp` пропускайте. Для ручной установки бинарника:

```sh
sudo cp knigojor.service /etc/systemd/system/knigojor@.service
sudo systemctl daemon-reload
```

Запуск от своего пользователя:

```sh
sudo systemctl enable --now knigojor@$USER
```

Логи:

```sh
journalctl -u knigojor@$USER -f
```

## Сборка из исходников

Нужен Go 1.27+.

```sh
make build          # bin/knigojor
make test           # go test -race ./...
make vet            # go vet ./...
make package        # deb/rpm для amd64 и arm64 в dist/
```

Кросс-сборка всех целевых архитектур:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o knigojor-amd64 ./cmd/knigojor
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o knigojor-arm64 ./cmd/knigojor
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -o knigojor-arm ./cmd/knigojor
```

`--version` печатает версию без запуска бота:

```sh
./bin/knigojor --version
```

## CI и релизы

- `.github/workflows/ci.yml` — тесты, `go vet` и проверочная сборка на push/PR.
- `.github/workflows/release.yml` — по тегу `v*` собирает бинарники для
  amd64/arm64/armv7, пакеты deb/rpm и публикует их в GitHub Release.

`VERSION` используется для локальных сборок (`make build`, `make package`).
Публикация релиза запускается только тегом `vX.Y.Z`: workflow собирает
бинарники и пакеты с версией из тега и прикрепляет их к GitHub Release.
Перед релизом обновите `VERSION` в репозитории, затем поставьте тег.

## Конфигурация

См. `.env.example`. Основные параметры:

| Переменная | Назначение | По умолчанию |
|---|---|---|
| `BOT_TOKEN` | токен Telegram-бота | обязательно |
| `ALLOWED_USER_ID` | единственный разрешённый пользователь | обязательно |
| `LOG_LEVEL` | уровень логирования: `DEBUG`, `INFO`, `WARNING`, `ERROR` | `INFO` |
| `MIRROR_1..10` | HTTP-зеркала Flibusta | `flibusta.is`, `flibusta.lib` |
| `ONION_1..10` | onion-зеркала | — |
| `PAGE_SIZE` | книг на одну страницу бота (1..50) | `5` |
| `HTTP_TIMEOUT` | таймаут обычных запросов, сек | `15` |
| `DOWNLOAD_TIMEOUT` | таймаут скачивания книги, сек | `180` |
| `MAX_RETRIES` | повторы HTTP-запросов | `3` |
| `MAX_PARALLEL_REQUESTS` | одновременных HTTP-запросов | `5` |
| `MAX_PARALLEL_DOWNLOADS` | одновременных скачиваний книг | `1` |
| `MAX_FILE_SIZE_MB` | лимит размера книги, MB | `50` |
| `HEALTHCHECK_INTERVAL` | период проверки зеркал, сек | `300` |
| `TOR_ENABLED` | запускать/использовать Tor | `true` |
| `TOR_EXECUTABLE` | путь к бинарнику Tor | `/usr/bin/tor` |
| `TOR_SOCKS_HOST` | адрес SOCKS5 Tor | `127.0.0.1` |
| `TOR_SOCKS_PORT` | порт SOCKS5 Tor | `9050` |
| `TOR_CONTROL_PORT` | control-порт Tor | `9051` |

Бот рассчитан на одного пользователя. Скачивание ограничено лимитом Telegram
(50 MB). Для onion-зеркал нужен установленный Tor.

## Устранение проблем

- `BOT_TOKEN is required` — проверьте `~/.knigojor/.env`.
- Tor не запускается — установите `tor` и проверьте `TOR_EXECUTABLE`.
- Ошибки сети — проверьте зеркала в конфиге.
- Скачанный вручную бинарник не запускается — выполните `chmod +x`.
