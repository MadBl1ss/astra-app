# legacy/ — снимок старой Стрелки

Это копия `tray/`, `package/` и `scripts/` из репозитория
[MadBl1ss/strelka-sing-box](https://github.com/MadBl1ss/strelka-sing-box),
коммит `7b7a730` (ветка `claude/notifications-server-check-teu0ox`: трей со
сглаживанием состояний и «липким» auto).

Правила:

- **Только для чтения.** Здесь ничего не правим и не собираем. Это источник,
  из которого этапы переносят рабочую логику в `core/`.
- Переносим вместе с тестами: у каждого файла `X.go` рядом лежит `X_test.go`.
- Папка удаляется целиком, когда новое приложение догонит трей (этап 08).

Что откуда брать:

| Файл | Что в нём | Куда уезжает |
| --- | --- | --- |
| `tray/api.go` | клиент Clash API, разбор config.json | `core/internal/clash`, этап 01 |
| `tray/boot_windows.go` | запуск движков, проверка портов | `core/internal/engine`, этап 01 |
| `tray/subscription.go` | скачивание подписок, разбор ссылок | `core/internal/subs`, этап 02 |
| `tray/xray.go` | конфиг Xray для REALITY/XHTTP | `core/internal/xray`, этап 02 |
| `tray/rules.go`, `rules_page.go`, `rules.html` | списки правил, страница правил | `core/internal/rules`, этап 02; экран «Правила», этап 06 |
| `tray/modes.go` | режимы Rule/Global | `core/internal/confgen`, этап 02 |
| `tray/health.go`, `notify.go`, `stats.go` | сглаживание, уведомления, статистика | основа для `core/internal/health` и `incident`, этап 03 |
| `tray/setup_windows.go`, `sys_windows.go` | автозапуск через планировщик, UAC | не переносится: заменяет служба, этап 08 |
| `tray/main.go`, `icon.go` | меню трея, иконка-щит | не переносится: заменяет приложение, этап 05 |
| `package/config.json`, `package/rules/` | шаблон конфига и списки | `core/internal/confgen/assets`, этап 02 |
| `package/test-servers.ps1` | полный тест скорости | идея для бэклога (тест скорости в окне) |
| `scripts/update-lists.mjs` | обновление автосписков | `scripts/`, этап 02 |
