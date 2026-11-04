# E05: длинный обрыв связи (RE-2) — живой захват DLL

Дата: 2026-11-04, ~21:42–21:45 local (UTC+5). Демо-сервер Finam, DLL под Wine, тот же сценарий,
что `recover-short-live.md`, но прокси остановлен на **150 с** — больше `session_timeout`.

## Процедура

Как в short-сценарии (см. `recover-short-live.md`), отличие: `kill -TERM tcproxy`, пауза **150 с**,
затем прокси снова на 127.0.0.1:39100. Harness: `sleep:160s`, затем `server_status`, `disconnect`.

## Тайминг (`*_ts.log`, `last-run.out`, `*_xdf.log`)

| Событие | Время (local) |
|---|---|
| RQ_PWD: `Session timeout 120000 ms.`, `Request timeout 20000 ms.` | 21:42:28.448 |
| обрыв, первая `ReConnecting to 127.0.0.1:39100...` | 21:42:32.510 |
| 120 попыток `ReConnecting`, каждая `Connect error #8: WSAE:10061`, шаг ~1.004 с | 21:42:32.510 … 21:44:32.050 |
| `RQ_MAIN [-1] Request timeout.` (throw, DLL сдалась) | 21:44:33.055 |
| колбэк `<server_status ... connected="false"/>` | 21:44:34.064 (harness t=127.892) |
| прокси поднят снова | 21:45:02 |
| `server_status` → `<result success="false"><message>Cannot process this command without connection.</message></result>` | 21:45:16 |
| `disconnect` → `<result success="false"><message>Соединение не установлено…</message></result>` | 21:45:19 |

## Что установлено (S)

- Окно восстановления = `session_timeout` из RQ_PWD = **120000 мс**: DLL прекратила попытки через
  ~120.5 с после начала обрыва (сразу после 120-й попытки с шагом ~1 с), до возвращения прокси на
  150 с. Это подтверждает RE-1 (окно = session_timeout, ретраи ровно раз в секунду) и снимает вопрос
  о единицах: серверное значение 120 (секунды) нормировано DLL в 120000 мс.
- После истечения окна DLL бросает `"Request timeout."`; текст лога: `RQ_MAIN [-1] Request timeout.`
  (имя запроса, [-1] — серверный таймаут), затем XML-колбэк `connected="false"`.
- Автоматического «нового логина» DLL не делает: прокси поднят на 150 с, но второго соединения нет
  (файл `recover-long-b.cap` пуст, 0 строк); без команды `connect` клиент остаётся отключённым.
- Пока связи нет, DLL-команды возвращают ошибки: `server_status` — «Cannot process this command
  without connection.», `disconnect` — «Соединение не установлено…» (success="false").
- Повторный колбэк `<server_status ... connected="true" recover="true"/>` пришёл сразу на обрыве
  (как в коротком сценарии); «Recover unavailable.» / «Recover limit exceeded.» в логах нет —
  соответствует RE-1 (ветка limit в 6.47 недостижима).

## Счётчики

- первое соединение (до обрыва): 384 строки захвата, до 4 с после логина;
- второе соединение: 0 кадров (DLL не переподключалась);
- попыток `ReConnecting to `: 120.

## Файлы

- `recover-long-live.hex` — комментарии без кадров: recovery-кадров нет (соединение не поднялось);
- `recover-long-live.xml` — колбэки DLL (recover="true" на обрыве, connected="false" после таймаута);
- этот файл — процедура, тайминги, выводы.
