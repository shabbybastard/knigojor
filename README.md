# knigojor

Персональный Telegram-бот для работы с электронной библиотекой Flibusta.

## Установка

1. Установить Python 3.12+ и системные зависимости:
   ```sh
   sudo apt install python3 python3-venv tor
   ```

2. Склонировать репозиторий и собрать бинарник на целевом устройстве:
   ```sh
   git clone <repo> && cd knigojor
   ./build_binary.sh
   cp dist/knigojor ~/knigojor
   chmod +x ~/knigojor
   ```

3. Создать каталог конфигурации:
   ```sh
   mkdir -p ~/.knigojor
   cp .env.example ~/.knigojor/.env
   chmod 600 ~/.knigojor/.env
   # отредактировать ~/.knigojor/.env: BOT_TOKEN, ALLOWED_USER_ID, зеркала
   ```

4. Запуск вручную:
   ```sh
   ~/knigojor
   ```

5. Запуск через systemd (от своего пользователя):
   ```sh
   sudo cp knigojor.service /etc/systemd/system/knigojor@.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now knigojor@$USER
   ```

6. Логи:
   ```sh
   journalctl -u knigojor@$USER -f
   ```

## Разработка и тесты

```sh
python3 -m venv .venv
.venv/bin/pip install -r requirements-dev.txt
.venv/bin/ruff check .
.venv/bin/python -m pytest -q --cov=knigojor --cov-report=term-missing
```

`--version` печатает версию без запуска бота:

```sh
.venv/bin/python main.py --version
```

## Обновление
Пересобрать бинарник (`./build_binary.sh`), заменить `~/knigojor`, перезапустить службу.

## Устранение проблем
- `BOT_TOKEN is required` — проверьте `~/.knigojor/.env`.
- Tor не запускается — установите `tor`, проверьте `TOR_EXECUTABLE`.
- Ошибки сети — проверьте зеркала в конфиге.

## Конфигурация

См. `.env.example`. Основные параметры:

| Переменная | Назначение | По умолчанию |
|---|---|---|
| `BOT_TOKEN` | токен Telegram-бота | обязательно |
| `ALLOWED_USER_ID` | единственный разрешённый пользователь | обязательно |
| `MIRROR_1..10` | HTTP-зеркала Flibusta | `flibusta.is`, `flibusta.lib` |
| `ONION_1..10` | onion-зеркала | — |
| `PAGE_SIZE` | книг на одну страницу бота (1..50) | `5` |
| `HTTP_TIMEOUT` | таймаут обычных запросов, сек | `15` |
| `DOWNLOAD_TIMEOUT` | таймаут скачивания книги, сек | `180` |
| `MAX_RETRIES` | повторы HTTP-запросов | `3` |
| `MAX_FILE_SIZE_MB` | лимит размера книги, MB | `50` |
| `HEALTHCHECK_INTERVAL` | период проверки зеркал, сек | `300` |
| `TOR_ENABLED` | запускать/использовать Tor | `true` |

Бот рассчитан на одного пользователя. Скачивание ограничено лимитом Telegram
(50 MB). Для onion-зеркал нужен установленный Tor.