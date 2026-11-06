# Восстановление DSH через CLI (`/usr/local/bin/dsh`)

Инструкция на случай, когда Desktop DSH не стартует (recovery-диалог / crash при boot),
а чат недоступен. Все команды проверены на этой машине (CLI 0.2.2-rc.1).

## 1. Собрать диагностику

```bash
# самый свежий crash-лог
ls -t "$HOME/Library/Logs/DeepSeek Harness"/crash-*.log | head -1

# весь boot-state (логи, профиль, git, процессы, порты) в один архив
bash /Users/whitefox/GolandProjects/txmlconnector/tools/dsh-crash-bundle.sh
cat ~/dsh-crash-latest.txt
```

Пришлите агенту путь из последней строки (`~/dsh-crash-*.tar.gz`) — он прочитает архив с диска.

## 2. Быстрая диагностика CLI

```bash
/usr/local/bin/dsh plugin --profile desktop ls      # зависимости профиля
/usr/local/bin/dsh --profile web --dump-config | head  # состав web-профиля
```

Важно: `dsh --profile desktop --dump-config` **специально запрещён** («profile is managed
exclusively by the Electron application»); desktop-профиль инспектируется через файлы
`~/.dsh/profiles/desktop/package.json` и `cordis.patch.yml`.

## 3. Лечение по возрастанию

### 3.1. Аварийно отключить плагин (быстрее всего)

```bash
bash /Users/whitefox/GolandProjects/txmlconnector/tools/dsh-plugin-disable.sh
/usr/local/bin/dsh plugin --profile desktop install   # синхронизировать pnpm/lockfile
open -a "DeepSeek Harness"
```

Скрипт делает бэкап `package.json.disable-*` и убирает `@local/dsh-ai-terminal` из bundles.
DSH должен подняться без плагина.

### 3.2. Починить зависимости профиля

```bash
/usr/local/bin/dsh plugin --profile desktop install
```

Это тот же `pnpm install` в каталоге профиля, который выполняет плагин-менеджер. Помогает при
рассинхроне `package.json` / `pnpm-lock.yaml` / `node_modules`.

### 3.3. Поднять рабочий Harness без Desktop

```bash
/usr/local/bin/dsh web --host 127.0.0.1 --port 18080 --no-open
# в выводе будет URL с token — открыть в браузере
```

`dsh web` использует отдельный web-профиль и работает даже при сломанном desktop-профиле.
Альтернатива с rescue-профилем:

```bash
/usr/local/bin/dsh rescue --from-default-profile web
```

### 3.4. Сбросить desktop-профиль (последнее средство)

```bash
ts=$(date +%Y%m%d-%H%M%S)
mv ~/.dsh/profiles/desktop ~/.dsh/profiles/desktop.broken-$ts
open -a "DeepSeek Harness"
```

При отсутствии `~/.dsh/profiles/desktop/package.json` Desktop создаёт shipped-профиль заново
(проверено по коду `dsh-app-boot`: `initProfile` из шаблона `desktop`). Настройки/плагины
придётся вернуть заново; старый профиль остаётся в `desktop.broken-*`.

### 3.5. Включить плагин обратно

```bash
bash /Users/whitefox/GolandProjects/txmlconnector/tools/dsh-plugin-enable.sh
open -a "DeepSeek Harness"
```

Скрипт добавляет bundle и `link:`-зависимость, делает бэкап и запускает `dsh plugin install`.

## 4. Проверка после запуска

```bash
# не появился ли новый crash-лог (должен быть старый)
ls -t "$HOME/Library/Logs/DeepSeek Harness"/crash-*.log | head -1
# жив ли backend плагина (если плагин включён)
curl -s -m 3 http://127.0.0.1:18090/api/health | head -c 200
```

## 5. Ограничения

- `dsh headless "..."` требует API-ключ DeepSeek в окружении (`DEEPSEEK_API_KEY`), иначе
  `MISSING_CREDENTIAL`; для простой проверки CLI достаточно `dsh --version` и `dsh plugin ls`.
- Не запускайте два `dsh web`/Desktop на одном `DSH_HOME` одновременно — возможны конфликты
  storage/сессий.
- Секреты (`~/.dsh/.credentials.yaml`, `~/re/demo.env`) в crash-bundle не попадают.