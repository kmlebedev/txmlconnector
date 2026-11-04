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

## Что в фикстуре и как этим пользоваться

- `rq_login` — **побайтовый** payload RQ_LOGIN, построенный как `session.LoginPayload("TESTLOGIN")`
  (19 байт; в захвате было 18 из-за 8-байтового логина, Z12 — NUL-terminated variable-length).
  Go-тест может сравнить свой `LoginPayload(login)` с этой строкой (для TESTLOGIN).
- `client.*` — **фиктивный** ClientInfo, которым перекодирован хвост RQ_PWD. Реальные строки
  host/user/OS, HWID и адреса адаптеров из захвата в фикстуру не попали; длины контента полей
  сохранены (computer 15, user 8, OS 24, hwid 7 packed-байт, два адреса 4 и 5 packed-байт).
- `<run>.rq_pwd_tail` — хвост RQ_PWD **после proof** (сам proof/challenge исключён), построенный
  через `session.PwdPayload(pwKey(TESTPASS), make([]byte, proofLen), ClientInfo{...}, Trailer)` и
  отрезанный по `proofLen`. Тест должен построить свой payload тем же вызовом и сравнить
  `payload[proofLen:]`.
- `<run>.rq_pwd_proof_len_excluded` — сколько байт proof вырезано (длина challenge от сервера;
  она у сессий разная: 128/136/129 байт — challenge и ключи в фикстуру не входят).
- `<run>.rq_pwd_payload_len` — полная длина payload RQ_PWD в захвате (196/204/197).
- `<run>.rq_pwd_trailer` — последний байт хвоста: 0 для ru (default/options), 1 для en.
- `<run>.rq_startup_reply_{len,sha256}` — RQ_STARTUP reply для контроля: у default и options
  одинаковый (4355 байт), у `lang_en` — другой (4219 байт), потому что язык меняет локализацию
  серверных справочников (S). Содержимое справочников в фикстуру не включено, только хэш.
- `<run>.c2s_types` / `<run>.s2c_types` — RLE-последовательности типов кадров (NxM = тип N
  повторён M раз). У default и options они совпадают; у lang_en RQ_MAIN-опросов меньше просто
  из-за тайминга закрытия. RQ_MAIN (5) — опросы, RQ_TIME (50) в конце — команда
  `get_servtime_difference`, RQ_LOGOUT (2) — `disconnect`.

## Что исключено (red lines)

- пароль (`TC_PASSWORD`), реальный логин (заменён на `TESTLOGIN`), challenge/proof и
  сессионные ключи, UID/SID, client/union, RQ_LOGIN reply payload, RQ_PWD proof;
- реальные host/user/OS/HWID/адреса адаптеров (заменены на фиктивные значения выше);
- сырые cap и логи DLL (содержат данные счёта) — только в `.re-run/`, в git не попадают.

## Подтверждено этим захватом (S)

- `default` vs `options` не меняют **ничего** на проводе: RQ_LOGIN payload побайтно, хвост
  RQ_PWD побайтно, RQ_STARTUP побайтно; отличаются только длины challenge/proof (серверные) и
  число RQ_MAIN-опросов (тайминг).
- `language=en` меняет только последний байт RQ_PWD (0→1) и локализует справочники (RQ_STARTUP
  reply 4219 vs 4355 байт) — см. `docs/protocol/connect.md`, раздел «Параметры <connect>».
- RQ_PWD trailer: 0 для ru, 1 для en; поля хвоста до trailer побайтно одинаковы.

Воспроизведение: `go test ./native/...`; сырьё и генератор — `.re-run/r6/` (не в git).
