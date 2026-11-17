# Ход миграции на Go

Основной план: [2026-11-17-migrate-to-golang.md](2026-11-17-migrate-to-golang.md).
Инструкция и контракт: [docs/go-migration.md](../docs/go-migration.md).

## Текущий статус

После первого среза владелец сообщил: **«проверил, все работает»**. Это
подтверждение предложенного пути локальных `doctor`, импорта/повторного запуска
и QR/2FA, а не агентский live-тест. Секреты владельца агенту не передавались.
Отдельных результатов для каждого пограничного сценария QR (другой аккаунт без
2FA, expiry, неверный пароль, отмена) в диалоге нет: их нельзя выдавать за
самостоятельно наблюдавшиеся интеграционные проверки.

После этого реализованы **семь MCP-инструментов и `serve`**. На предложение
подключить Go MCP и проверить чтение/поиск/download/разрешённую отправку/reply
владелец повторно сообщил: **«проверил, все работает»**. Личная Go-установка
принята на основании этого сообщения; отдельных сетевых логов или деталей
каждого peer не собиралось, агент live-запросы не выполнял.

**Миграция завершена.** После приёмки удалены отслеживаемые Python/secretary/Business
и старые инструкции. Секреты, пользовательские данные и MCP-конфигурация не
изменялись агентом. Ниже сохранены доказательства проверок и известные ограничения.

## Реализовано

### Setup и auth (первый срез)

- `init`, `auth login --qr`, `doctor [--network]`, Go 1.27.4 darwin/arm64.
- gotd v0.166.1, официальный MCP SDK v1.8.0, нативный Security.framework через
  `keybase/go-keychain`/cgo. Внешний `qrencode`, Python/uv и subprocess с секретами
  не нужны для работы бинарника.
- Старые имена API credentials/Telethon StringSession и приоритет Keychain → env.
  Найденное некорректное значение не подменяется env.
- Go-сессия в **`telegram-mcp-go-session`**, account прежний `$USER`.
  Библиотечные записи до проверки авторизации остаются в RAM; только затем
  атомарный SecItemUpdate/Add. Старую Telethon-запись не меняем; file fallback нет.
- Telethon импорт через `session.TelethonSession()`, встроенный QR, обработчик
  update до token export, 2FA без echo, повтор неверного пароля, отмена с
  восстановлением терминала. Без интерактивных stdin/stderr login отклоняется.

### MCP и MTProto (второй срез)

- `serve` и `tg_me`, `tg_dialogs`, `tg_history`, `tg_search`, `tg_message`,
  `tg_send`, `tg_download` с документированными схемами/defaults/null.
- Ленивое подключение: initialize/list/невалидные вызовы/запрещённые sends не
  обращаются к credentials или Telegram. Нет интерактивного входа из `serve`.
- Вызовы сериализуются; каждое допустимое действие открывает ограниченное по
  времени соединение, проверяет авторизацию и закрывает его после операции.
  Межпроцессный kernel lock исключает параллельное использование Go-сессии;
  файл в macOS Cache содержит только lock, не секреты. Python он не обнаруживает.
- Numeric peers получают access_hash из bounded main/archive dialogs, в том
  числе после нового процесса. Min entities не затирают известный usable hash.
  Нет предположения access_hash=0 или незаметного неограниченного сканирования.
- Диалоги: до 500 просмотренных; история/поиск: до 200 результатов и 500
  просмотренных сообщений. История oldest-first, поиск newest-first; ID bounds
  эксклюзивные, since inclusive/until exclusive. Правильный global rate/peer/ID
  cursor и rehydration приватного канала покрыты mocked RPC-тестами.
- Недоверенные тексты/заголовки/авторы внутри `untrusted-telegram-content`,
  вложенные теги и атрибуты экранированы. Это не sandbox от prompt injection.
  Snippet 500 символов, полный текст до 16 384 символов/сообщение,
  суммарный результат до 2 MiB. Никаких readHistory/read acknowledgements.
- Отправка только напрямую через пользовательский MTProto. Default allowlist
  `me`, проверка до credentials/подключения. Буквальный marker вне markup во
  всех non-broadcast сообщениях; same-peer reply предварительно проверяется.
  Нет Business/bot fallback, send-as identity или paid-message flags.
- Диалекты: CommonMark gotd (`*italic*`, `**bold**`, `__bold__`), gotd HTML, plain.
  Проверены actual text/entities, UTF-16 offsets с emoji/маркером, ссылки,
  inline/pre code и language. Go-версия с этим документированным диалектом
  принята владельцем; совместимость с Telethon не обещается.
- Один cryptographic random_id и один логический send RPC. Application retries
  и RPC timeout resend выключены; protocol retransmissions используют тот же ID.
  Network/non-RPC или server 5xx после начала send явно сообщает **UNKNOWN**, без слепого повтора.
  Подтверждённый send не превращается в кажущийся неуспех из-за teardown error.
- FloodWait возвращается с длительностью без hidden sleep. Для скачивания
  намеренно используются raw bounded upload.getFile RPC, а не downloader с
  внутренними FloodWait retries. Обычный таймаут 60 s, download 2 min.
- Скачивание фото/документов до 100 MiB: проверка объявленного/фактического размера,
  exclusive random .bin filename, 0600, confined directory handle, cleanup
  partial file при ошибке/отмене. Не используем имя файла из Telegram и не
  перезаписываем существующие файлы.
- Найден и исправлен регрессией lifecycle gap SDK: EOF отменял handler, но отмена
  родительского контекста `Run` не обязательно отменяла активный tool call.
  Добавлена явная связь lifetimes через `context.AfterFunc`; EOF и остановка
  сервера теперь отменяют активный backend, который освобождает ресурсы/lock.
- Учтена ещё одна особенность gotd: `Client.Run` нормализует `context.Canceled`
  в nil. Auth теперь явно требует подтверждённого Commit перед успехом; tools
  сохраняют реальный operation outcome, даже если Run вернул nil при отмене.
  Отмена до вызова и UNKNOWN send покрыты отдельными regression-тестами.

### Очистка после приёмки (третий срез)

- Удалены отслеживаемые `src/telegram_mcp/*.py`, `scripts/*.py`, Python-тесты,
  `pyproject.toml`, `uv.lock`, `.python-version`, `deploy/secretary.env.example`
  и `deploy/telegram-secretary.service`. Это файлы репозитория, не установленные
  системные службы или реальные env/secrets.
- README полностью переписан для Go; старые Business/VPS/secretary инструкции
  удалены. Подробное руководство переведено из pending acceptance в эксплуатацию.
- Makefile теперь содержит только локальные Go build/fmt/vet/test/race/lint,
  setup/login/doctor/serve. Нет CI/release workflow. `clean` удаляет только
  `bin/telegram-mcp`, не окружения, Keychain, сессии или скачанные файлы.
- Игнорируемые `.venv`, Python caches и прочие локальные артефакты намеренно
  не удалялись рекурсивно. Содержимое пользовательских env/session не читалось.
- Исторические планы и верификация сохранены; указанные в них Python-пути
  относятся к исходной ревизии, а не к текущей структуре Go-проекта.
- После очистки повторены Go build/checks и stdio smoke без Python/uv/qrencode
  в PATH, без чтения настоящих credentials и без Telegram-запросов.

### Финальное ревью перед коммитом

База сравнения — `e6391b2d0879706480e691271ada6facdde404fe`, включая
индекс, рабочее дерево и новые файлы. Проверены соответствие актуальному плану
и реализация: allowlist до credentials, сохранение/импорт сессии, QR/2FA,
отмена/lifecycle, send UNKNOWN/no retry, ограничения чтения и cleanup файлов.
Проверка выполнена последовательно одним агентом: отдельные sub-agent tools
недоступны; это не независимое стороннее security-аудирование.

Воспроизведены падающими offline-тестами и исправлены два дефекта:

1. **P2 — преждевременное завершение пагинации.** Короткая страница Telegram
   не означает конец выдачи. Исправлены история, chat/global search, диалоги и
   поиск numeric peer; peer-local чтение продолжает cursor по `MessageEmpty.ID`.
   Повторный cursor и общий scan cap по-прежнему останавливают чтение.
2. **P2 — потеря дробной части временных границ.** RFC3339 допускает дробные
   секунды: локальные `since`/`until` теперь сравниваются без усечения;
   exclusive `offset_date` округляется вверх, без переполнения TL int32.

Добавлены пять top-level regression-тестов в `internal/telegram/read_test.go`;
всего 44 top-level Go-теста. После исправлений прошли `make check`, `make build`,
`git diff --check` и проверка индекса. Реальный собранный бинарник повторно
прошёл offline initialize/list/denied send/EOF/help при изолированном PATH,
без Python/uv/qrencode, с пустым stderr. Реальные credentials не читались,
Telegram-запросы и отправки не выполнялись. Новых блокирующих замечаний в этом
ревью не выявлено; эксплуатационные ограничения ниже остаются в силе.

## Проверки

| Проверка | Результат |
|---|---|
| `gofmt`, `go vet ./...`, `go test ./...` | ✅ |
| `go test -race ./...` | ✅ |
| `golangci-lint run ./...` | ✅ 0 issues |
| `go build -o bin/telegram-mcp ./cmd/telegram-mcp` | ✅ macOS arm64 |
| Настоящий собранный бинарник: stdio initialize `2025-11-25`, list семи tools, запрещённый send | ✅ без credentials и Telegram; чистые stdout/stderr, EOF exit 0 |
| CLI subprocess: EOF/SIGTERM, чистый stdout | ✅ offline regression |
| Активный backend: отмена по EOF и parent cancellation | ✅ regression, повторный прогон 20 раз |
| Mocked RPC: история/global pagination, приватный ID/hash после restart, границы, dialog cap | ✅ |
| Marker, HTML/CommonMark/entities/UTF-16, существующий reply, send UNKNOWN/no retry | ✅ |
| File lifecycle: permissions, no overwrite, oversized/truncated/failure/cancel cleanup | ✅ |
| Keychain priority/transaction/cancellation и kernel lock exclusion/release | ✅ fake store, настоящие секреты не читаются |
| Hidden terminal input/Unicode/backspace/Ctrl+C/context cancellation | ✅ pseudoterminal |
| Закреплённый gotd QR regression suite | ✅ первый срез, mocked RPC |
| Python regression до удаления старой реализации | ✅ исторический baseline: 139 passed; Python-тестов больше нет |
| Реальный auth/import/QR/restart на Mac | ✅ по сообщению владельца |
| Реальные read/search/private ID/download | ✅ по общему сообщению владельца после предложенного checklist, без агентских live-запросов |
| Реальная разрешённая отправка/форматирование/reply | ✅ по сообщению владельца; агент сообщения не отправлял |
| Приёмка Go-установки и очистка репозитория | ✅ подтверждение владельца и удаление legacy-файлов после него |

## Ограничения, которые не скрываем

1. Восстановление numeric peer ограничено 500 диалогами: для более старого
   private peer нужно поднять чат в recent dialogs или использовать public username.
2. На каждый вызов новое соединение: немного выше latency, зато проще lifetime.
3. Global search не выдаёт continuation token наружу; пагинация есть внутри вызова.
4. External media/web previews не скачиваются. CDN redirect не запрашивается;
   неожиданный обязательный redirect выдаёт явное ограничение.
5. Файлы имеют random `.bin` имена, не Telegram filenames; MIME/расширения как
   удобство не реализованы. Размер по умолчанию не снимается параметром инструмента.
6. Default `me` не означает разрешение aliases/каналов: allowlist не расширяется
   автоматически при разрешении peer, его изменение требует рестарта.
7. Нельзя запускать Python и Go с импортированным auth key одновременно. Новый
   Go lock исключает другие Go network-операции, но не старый Python MCP.
8. Недоступная Go-запись не считается отсутствующей и не заменяется legacy key;
   нет Go-session env/file fallback. Это явно описанное консервативное ограничение.
9. Публичный релиз, CI/CD, распространение бинарников и другие ОС не входят
   в завершённую миграцию; это отдельный будущий объём.

## Итог

Auth и подключённый MCP приняты владельцем двумя последовательными сообщениями.
Go-реализация, локальная сборка/проверки и документация готовы; legacy-код удалён.
Открытых блокеров миграции в диалоге нет. Детальная live-матрица каждого edge case
не составлялась и не подменяется утверждением, что агент наблюдал все сценарии.

При обновлениях достаточно локальных Go-проверок и осознанного owner-controlled
smoke checklist из руководства. Реальные отправки по-прежнему требуют явного
запроса и соблюдения allowlist. CI/CD или публичная поставка не добавлялись;
их оценка и реализация потребуют отдельной задачи.
