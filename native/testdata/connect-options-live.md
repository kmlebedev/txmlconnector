# E07-фикстура: обезличенный живой захват `<connect>` default vs options

Дата: 2026-11-04, демо-сервер Finam, DLL `txmlconnector64-6.47.2.26.4.dll` под Wine 11.21,
`tools/re/reference-harness` через логирующий прокси (`tcproxy`, порт 39100).
Сырьё (вне git): `.re-run/cap/r6-{default,options,lang-en}.cap`, `*_ts.log`, `*_xdf.log`,
`last-run.out` — `.re-run/r6/{default,options,lang-en}/`.

Фикстура: `connect-options-live.txt` — эталон для `TestConnectOptionsMatchDLL` (задача 6).

## Три прогона

| Run | `<connect>` | Зачем |
|---|---|---|
| `default` | login/password/host/port/rqdelay=100 | базовая линия, language отсутствует → ru (trailer 0) |
| `options` | + `<autopos>true</autopos><micex_registers>true</micex_registers><utc_time>true</utc_time><push_u_limits>10</push_u_limits><push_pos_equity>10</push_pos_equity>` | проверить, что опции не меняют провод |
| `lang_en` | + `<language>en</language>` | language → последний байт RQ_PWD (0/1) |

После загрузки harness отправлял `get_server_id`, `get_servtime_difference`, `get_markets`,
`server_status`, `disconnect` (видны в списках кадров как RQ_TIME 50 и RQ_LOGOUT 2).

## Независимый эталон: in-place маскирование (важно для ревью T6/P2)

Хвосты `<run>.rq_pwd_tail` — **реальные байты живого захвата**, а не результат перекодирования
Go-кодировщиком. Из расшифрованного payload-а RQ_PWD каждого прогона взят хвост **после proof**
(`payload[proof_len:]`, где `proof_len = login_reply[1]` — длина challenge от сервера), и в нём
**на месте** заменены значения секретных полей байтами той же длины:

| Поле | Длина в захвате | Маска | Как заменено |
|---|---|---|---|
| computer name (`Z16`) | 15 | `TESTHOSTXXXXXXX` | байты строки заменены, NUL на той же позиции |
| user name (`Z32`) | 8 | `TESTUSER` | то же |
| OS (`Z80`) | 24 | `TESTOSXXXXXXXXXXXXXXXXXX` | то же |
| hardware id (`G`) | 7 packed-байт | `0x0000100000000000` | значение той же packed-длины, длина/структура не менялись |
| адрес адаптера #1 (`L`) | 4 packed-байта | `0x00100000` | то же |
| адрес адаптера #2 (`L`) | 5 packed-байт | `0x10000000` | то же |
| trailer (`B`) | 1 | 0 / 0 / 1 | не менялся: это language-флаг (ru/en) |

Никакие байты не сдвигались: NUL-терминаторы, порядок и длины полей — из реального захвата.
Побайтовое сравнение с эталоном: у `default` и `options` хвосты **идентичны** (только challenge
короче/длиннее), у `lang_en` отличие только в последнем байте (trailer 1); полные payload-и
196/204/197 байт и trailer 0/0/1 сохранены.

`rq_login` не менялся и остаётся синтетическим (`session.LoginPayload("TESTLOGIN")`): это не
эталон RQ_PWD и он не проверяется на независимость; в реальном payload-е login заменён на
`TESTLOGIN`.

### Как воспроизвести маскирование (без секретов)

```sh
# 1. Расшифровать сырой захват (нужен TC_PASSWORD из ~/re/demo.env, в git не попадает):
set -a; . ~/re/demo.env; set +a
go run ./tools/wire-decode -payload -1 .re-run/cap/r6-default.cap > /tmp/r6-default.txt

# 2. Вырезать хвост RQ_PWD после proof и замаскировать его in-place (python, без Go-функций):
python3 - <<'PY'
import sys
lines = [l for l in open('/tmp/r6-default.txt') if l.strip() and not l.startswith('#')]
pwd = next(bytes.fromhex(l.split()[7]) for l in lines if 'C>S' in l and 'type=46' in l)
reply = next(bytes.fromhex(l.split()[7]) for l in lines if 'S>C' in l and 'type=1' in l)
proof_len = reply[1]
tail = bytearray(pwd[proof_len:])
# ... заменить байты строк computer/user/OS на TESTHOSTXXXXXXX / TESTUSER / TESTOS... той же длины,
# ... packed-значения HWID/адресов — на канонические значения той же packed-длины (см. таблицу выше)
print(tail.hex())
PY
```

(Полная версия скрипта — `.re-run/r16/mkfix-inplace.py`; в git не кладётся, использует сырьё
`.re-run/`.)

## Что в фикстуре и как этим пользоваться

- `client.*` — маскированные значения из таблицы выше (те же длины, что в захвате); из них
  перекодировать хвост **не нужно** — эталон уже лежит в файле байтами захвата.
- `<run>.rq_pwd_tail` — хвост RQ_PWD **после proof** (proof/challenge исключены), байты живого
  захвата с in-place маскированием. Тест сравнивает построенный кодировщиком payload
  `payload[proof_len:]` с этой строкой; для независимой проверки можно наоборот собрать payload из
  `client.*` и сверить с захватом.
- `<run>.rq_pwd_proof_len_excluded` — сколько байт proof вырезано (128/136/129 — серверная длина
  challenge, фикстура её не содержит).
- `<run>.rq_pwd_payload_len` — полная длина payload RQ_PWD в захвате (196/204/197).
- `<run>.rq_pwd_trailer` — последний байт хвоста: 0 для ru (default/options), 1 для en.
- `<run>.rq_startup_reply_{len,sha256}` — RQ_STARTUP reply для контроля: у default и options
  одинаковый (4355 байт), у `lang_en` — другой (4219 байт): язык меняет локализацию справочников
  (S). Содержимое справочников в фикстуру не включено, только хэш.
- `<run>.c2s_types` / `<run>.s2c_types` — RLE-последовательности типов кадров (NxM = тип N
  повторён M раз). У default и options они совпадают; у lang_en RQ_MAIN-опросов меньше из-за
  тайминга закрытия. RQ_MAIN (5) — опросы, RQ_TIME (50) в конце — команда
  `get_servtime_difference`, RQ_LOGOUT (2) — `disconnect`.

## Что исключено (red lines)

- пароль (`TC_PASSWORD`), реальный логин, challenge/proof и сессионные ключи, UID/SID,
  client/union, RQ_LOGIN reply payload, RQ_PWD proof;
- реальные host/user/OS/HWID/адреса адаптеров (заменены in-place масками той же длины);
- сырые cap и логи DLL (содержат данные счёта) — только в `.re-run/`, в git не попадают.

## Подтверждено этим захватом (S)

- `default` vs `options` не меняют **ничего** на проводе: RQ_LOGIN payload побайтно, хвост
  RQ_PWD побайтно (после in-place маскирования — идентичен), RQ_STARTUP побайтно; отличаются только
  длины challenge/proof (серверные) и число RQ_MAIN-опросов (тайминг).
- `language=en` меняет только последний байт RQ_PWD (0→1) и локализует справочники (RQ_STARTUP
  reply 4219 vs 4355 байт) — см. `docs/protocol/connect.md`, раздел «Параметры <connect>».
- RQ_PWD trailer: 0 для ru, 1 для en; поля хвоста до trailer побайтно одинаковы.

## Проверка независимости (для ревью T6)

- эталонные хвосты получены заменой байт в реальном payload-е (см. скрипт выше), Go-кодировщик
  (`session.PwdPayload`) при сборке эталона **не использовался**;
- длины payload-ов 196/204/197 и trailer 0/0/1 сохранены; `default.rq_pwd_tail ==
  options.rq_pwd_tail`, `lang_en` отличается только последним байтом;
- повторная сборка этим скриптом даёт те же байты, что и в файле (проверено побайтно).

Воспроизведение: `go test ./native/...`; сырьё и генератор — `.re-run/r6/`, `.re-run/r16/`
(не в git).
