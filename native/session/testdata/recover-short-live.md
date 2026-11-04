# E05: короткий обрыв связи (RE-2) — живой захват DLL

Дата: 2026-11-04, ~21:38 local (UTC+5). Сервер: демо-сервер Finam (`$TC_HOST:$TC_PORT`).
DLL `txmlconnector64-6.47.2.26.4.dll` под Wine 11.21, XML-команды — `tools/re/reference-harness`
(шаблон RE-2 из `tools/re/README.md`). Логин/пароль — только из `~/re/demo.env`, в фикстурах нет.

## Процедура

```
set -a; . ~/re/demo.env; set +a
go build -o .re-run/e05/tcproxy ./tools/tcproxy
.re-run/e05/tcproxy -listen 127.0.0.1:39100 -upstream "$TC_HOST:$TC_PORT" \
    -out .re-run/cap/recover-short-a.cap &
RE_WORKDIR=$PWD/.re-run/e05/short tools/re/run_dll.sh 5s \
  '<command id="connect"><login>$TC_LOGIN</login><password>$TC_PASSWORD</password>
   <host>127.0.0.1</host><port>39100</port><rqdelay>100</rqdelay></command>' \
  sleep:8s sleep:40s '<command id="server_status"/>' sleep:3s '<command id="disconnect"/>'
# через ~7 с после логина: kill -TERM tcproxy, пауза 5 с, tcproxy снова на 127.0.0.1:39100
#   (второй файл .re-run/cap/recover-short-b.cap); DLL переподключилась сама
```

Расшифровка: `tools/wire-decode` не подходит для второго соединения (оно начинается с RQ_RELOGIN
на ключе по умолчанию, а RQ_RECOVER идёт на сессионном ключе, которого в этом файле нет).
Кадры сняты вспомогательным декодером `.re-run/e05/decode` (берёт сессионные ключи из RQ_PWD
первого захвата и на каждый кадр пробует оба набора ключей, выбирая по CRC). Сырые захваты и
декодер — вне git (`.re-run/`, gitignored).

## Тайминг (из `*_ts.log` и `last-run.out`)

| Событие | Время (local) |
|---|---|
| RQ_PWD, `Session timeout 120000 ms.` | 21:38:05.668 |
| обрыв (kill прокси), первая `ReConnecting to 127.0.0.1:39100...` | 21:38:09.352 |
| попытки connect: `Connect error #8: WSAE:10061`, каждые ~1.004 с | 09.353 … 14.375 |
| `ReConnected.` (прокси поднят) | 21:38:15.379 |
| ` Recover succeeded.` | 21:38:15.676 |

Итого: обрыв ~6.0 с, рукопожатие восстановления ~0.3 с после TCP connect.

## Что подтверждено (S — живой обмен с DLL)

- **RQ_RELOGIN payload = `Z12: login`**, байты совпали с полем логина из RQ_LOGIN: в фикстуре
  `544553544c4f47494e00` (= `TESTLOGIN` + NUL). Захваченный логин был другой строкой (8 байт);
  при обезличивании он заменён на `TESTLOGIN` (Z12 — NUL-terminated, длина следует за строкой).
- **RQ_RELOGIN reply = `B:len, Bn:token, B:sid`**: len=135, token 135 байт, sid=0, хвоста нет.
  `sid` совпал с SID из RQ_PWD (`SID# 0`), поэтому проверка «тот же сервер» прошла.
- **RQ_RECOVER payload = token (те же 135 байт, без префикса длины) + `L:lastSeq`**;
  lastSeq = **61**, байты `d1 03`. Это 2 запроса логина (RQ_LOGIN + RQ_PWD) + 59 обычных запросов
  первого соединения: подтверждает, что счётчик увеличивается на всех типах, кроме 63/64.
- Ключи: RQ_RELOGIN принят на ключе по умолчанию, RQ_RECOVER — на прежнем сессионном ключе;
  обмена новыми ключами нет.
- **Ответ на RQ_RECOVER** пришёл кадром **type=5** (RQ_MAIN — тип прерванного запроса)
  с payload 2216 байт (отложенный ответ). Кадра type=0x40 в захвате нет; DLL приняла
  recovery (в RE-1 проверка допускала `0x40` или тип последнего обычного запроса).
- После восстановления DLL **не** шлёт RQ_LOGIN/RQ_PWD/RQ_STARTUP: на втором соединении только
  RQ_MAIN (и RQ_LOGOUT при disconnect) — сессия, каталог и `secid` сохраняются.
- XML-колбэки (`recover-short-live.xml`): на обрыве `<server_status ... connected="true"
  recover="true"/>` (сразу при потере связи), после успеха — `<server_status ... connected="true"/>`
  без `recover`; `connected="false"` — только на `disconnect`. Повторных `<securities>`/`<client>`/
  `<positions>` после восстановления нет.

## Счётчики по захвату

- первое соединение (до обрыва): 121 кадр, 59 обычных запросов, 4 RQ_MAIN;
- второе соединение (после обрыва): 821 кадр: 817 RQ_MAIN, 2×RQ_RELOGIN, 1×RQ_RECOVER, 1×RQ_LOGOUT;
- колбэков DLL за прогон: 766 строк `CALLBACK:` (из них 5 `server_status`).

## Обезличивание

- логин → `TESTLOGIN`; клиентских/счётных/юнион-полей в фикстурах нет (в кадрах 63/64 их нет
  по формату, из XML оставлены только `server_status`/`overnight`);
- сырые захваты, `last-run.out` и логи DLL — только в `.re-run/`, в git не попадают;
- проверка: grep по `native/session/testdata` на логин/клиента/счёт/UID/SID-значения из живой
  сессии пуст (см. отчёт RE-engineer).

## Файлы

- `recover-short-live.hex` — кадры 63/64 и первый кадр после восстановления (формат:
  `<direction> <request type> <payload hex>`, `#` — комментарии);
- `recover-short-live.xml` — колбэки DLL вокруг восстановления;
- этот файл — процедура, тайминги, подтверждённые факты.
