# Quick start: коннектор TRANSAQ в DeepSeek Harness Desktop

Как за 5 шагов подключить `dshtrading-connector-transaq` к приложению DeepSeek Harness (DSH)
Desktop, чтобы агент видел MOEX через инструменты `ru_*`. Подробности о коннекторе — в
[README](README.md), контракт шлюза — в [gateway-v1.md](../docs/ai-terminal/gateway-v1.md).

Схема: `bin/terminal` (Go, TRANSAQ) отдаёт read-only шлюз `/api/gw/v1/*` на loopback, а
коннектор в профиле Desktop (`~/.dsh/profiles/desktop`) регистрирует рынок `ru` / провайдер
`finam` в реестрах хоста dsh-trading (`@dshtrading/base`) и 6 инструментов агента. Заявки только
dry-run, реальной торговли нет.

## Требования

- macOS, DeepSeek Harness Desktop **0.2.2-rc.1** (`dsh --version`), Go 1.27+, `openssl`.
- Checkout этого репозитория (команды ниже — из его корня).
- Доступ к npm при первой установке: скрипт ставит в профиль хост `@dshtrading/base` 0.6.x
  (пакеты dsh-trading распространяются под PolyForm Noncommercial 1.0.0; сам коннектор их код
  не содержит).
- Порт **18095** свободен. 18090 занят бэкендом плагина AI-терминала, если он включён; у того
  бэкенда свой случайный токен, поэтому коннектору нужен отдельный `bin/terminal`.

## 1. Токен шлюза

Один токен на шлюз и на приложение; хранится в файле с правами 600, в чат и логи не попадает:

```bash
mkdir -p ~/.config/txmlconnector
[ -s ~/.config/txmlconnector/gateway-token ] || (umask 077; openssl rand -hex 16 > ~/.config/txmlconnector/gateway-token)
```

## 2. Шлюз

```bash
make terminal
TERMINAL_API_TOKEN="$(cat ~/.config/txmlconnector/gateway-token)" \
  bin/terminal -mode offline -addr 127.0.0.1:18095 -llm off
```

`-mode offline` — встроенный снимок демо-данных (`source: "seed"`). Для живых котировок —
`-mode live` с `TRANSAQ_*`/`TC_*` в окружении (см. [docs/ai-terminal/README.md](../docs/ai-terminal/README.md));
коннектор эти переменные не читает. Окно терминала со шлюзом оставьте открытым.

Проверка в другом окне:

```bash
curl -s -H "X-Terminal-Token: $(cat ~/.config/txmlconnector/gateway-token)" http://127.0.0.1:18095/api/gw/v1/info
```

Ожидается JSON с `"market":"ru","provider":"finam"`.

## 3. Профиль Desktop

Закройте DSH Desktop и выполните:

```bash
tools/dsh-connector-enable.sh
```

Скрипт делает бэкап `package.json` и `cordis.patch.yml` рядом с ними (`*.connector-<время>`) и
добавляет в `~/.dsh/profiles/desktop`:

- зависимость `@local/dshtrading-connector-transaq` → `link:` на каталог коннектора в этом checkout;
- хост `@dshtrading/base` (^0.6.3) в зависимости и в `dsh.profile.bundles`;
- строку профиля между маркерами `# dshtrading-connector-transaq: BEGIN/END`:

```yaml
- insert:
    - id: dsh-trading-ru-connector-transaq
      name: '@local/dshtrading-connector-transaq'
      config:
        enabled: true
        gatewayUrl: 'http://127.0.0.1:18095'
        tokenRef: TERMINAL_API_TOKEN
```

Затем он запускает `dsh plugin --profile desktop install`. Повторный запуск безопасен: строка не
дублируется. Порт, имя токена и профиль меняются переменными `GATEWAY_URL`, `TOKEN_REF`,
`PROFILE`, `DSH_HOME` (см. `tools/dsh-connector-enable.sh --help`).

## 4. Токен для приложения

Desktop, запущенный из Dock или Finder, не видит переменных вашего shell. Коннектор на каждый
запрос сначала спрашивает сервис `credentials` хоста, а затем переменную окружения
`TERMINAL_API_TOKEN`. Проще всего отдать её через launchd:

```bash
launchctl setenv TERMINAL_API_TOKEN "$(cat ~/.config/txmlconnector/gateway-token)"
```

Значение живёт до перезагрузки macOS; после перезагрузки повторите команду до запуска DSH.

## 5. Перезапуск и проверка

```bash
osascript -e 'quit app "DeepSeek Harness"'; open -a "DeepSeek Harness"
```

В новом чате попросите агента: «Вызови ru_get_ticker для SBER». Ожидается ответ с
`"symbol": "SBER.TQBR"` и `"source": "seed"` (или `"live"` в живом режиме). Другие
инструменты: `ru_get_klines`, `ru_get_orderbook`, `ru_get_trades`, `ru_list_instruments`,
`ru_get_paper_account`.

## Если что-то не так

| Симптом | Причина и что делать |
|---|---|
| Агент не видит `ru_*` | Строки нет в профиле или DSH не перезапущен: проверьте `grep -A6 'connector-transaq: BEGIN' ~/.dsh/profiles/desktop/cordis.patch.yml` и перезапустите приложение |
| `TRADING_AUTH_FAILED` | Токен приложения не совпадает с токеном шлюза: повторите шаг 4 и перезапустите DSH; шлюз запускайте из того же файла токена |
| `TRADING_NETWORK` | Шлюз не запущен или не на 18095: шаг 2, проверка `curl` |
| `TRADING_SYMBOL_NOT_FOUND` | Инструмента нет в universe шлюза: `ru_list_instruments` покажет доступные |
| DSH не стартует после правки | Откатите профиль из бэкапа: `cp ~/.dsh/profiles/desktop/package.json.connector-<время> ~/.dsh/profiles/desktop/package.json` (и `cordis.patch.yml.connector-<время>`), либо `tools/dsh-connector-enable.sh --remove` |

Отключить коннектор: `tools/dsh-connector-enable.sh --remove`, затем перезапуск DSH. Хост
`@dshtrading/base` при этом остаётся установленным; убрать его — `dsh plugin --profile desktop remove @dshtrading/base`
и удалить `@dshtrading/base` из `dsh.profile.bundles` в `~/.dsh/profiles/desktop/package.json`.

## Ограничения

- Вкладки MOEX в интерфейсе нет: апстрим dsh-trading держит закрытый список рынков. Данные
  доступны агенту через `ru_*`.
- Хост `@dshtrading/base` добавляет в Desktop свои роли, пресеты и инструменты других рынков
  (крипто, US, CN, HK). Если это мешает основному профилю, подключайте коннектор к отдельному
  профилю: `PROFILE=trading-web tools/dsh-connector-enable.sh` и запуск
  `dsh --profile trading-web` (web UI в браузере); профиль `desktop` CLI запускать не даёт, он
  принадлежит приложению.

## Что проверено

- 2026-11-09: `tools/dsh-connector-enable.sh` на изолированной копии профиля (`DSH_HOME` во
  временном каталоге, пакеты dsh-trading 0.6.3, dsh 0.2.2-rc.1) — строка и `link:` добавлены,
  повторный запуск строку не дублирует. Хост, запущенный из этого профиля против
  `bin/terminal -mode offline` на 18095, зарегистрировал `ru/finam` в `tradingMarketDataRegistry`
  и `tradingTradeRegistry`, все 6 инструментов `ru_*` доступны.
- 2026-11-06: агентский вызов `ru_get_ticker SBER` → `SBER.TQBR` в реальном хосте dsh-trading
  ([приёмка](../docs/ai-terminal/acceptance-pr67-2026-11-06.md)).
- **Не проверено:** сам профиль `desktop` приложения (скрипт на нём не запускался) и передача
  токена через `launchctl setenv` в DSH Desktop.
