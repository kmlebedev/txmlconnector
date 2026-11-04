# RQ_SEC_SPEC (15) и `get_securities_info` → `<sec_info>`: живые фикстуры (задача 11)

Дата захвата: 2026-11-04 (вечерняя сессия), демо-сервер Finam, DLL
`txmlconnector64-6.47.2.26.4.dll` под Wine 11.21 через `tools/tcproxy`.
Сырьё (вне git): `.re-run/r11/` (основной прогон), `.re-run/r12/` (батч-проба), `.re-run/cap/secinfo-live.cap`.

## Прогоны (S)

**r11:** `connect`; `subscribe` alltrades отдельными командами для SBER (26), облигации SU26224RMFS4
(10043), SiZ6 (20583) — каждая подписка заставляет DLL отправить RQ_SEC_SPEC (15) и получить ответ;
затем две команды `get_securities_info`:
- формой с `security`: `<security><market>1</market><seccode>SBER</seccode></security>` +
  `<security><market>1</market><seccode>SU26224RMFS4</seccode></security>` +
  `<security><market>4</market><seccode>SiZ6</seccode></security>`;
- формой с `secid`: `<secid>26</secid><secid>20583</secid>`.

Обе команды вернули `<result success="true"/>` и **5 колбэков `<sec_info>`** (3 + 2).
**Важно:** на команды `get_securities_info` DLL не отправляет ни одного запроса — в логе захвата
между командами (t≈12.7/15.7 с) нет ни одного C>S-кадра: `<sec_info>` рендерится из **кэша**
каталога (RQ_STARTUP + R_SEC_NEW) и уже полученного spec-кэша (D: обработчик `FUN_180197e60` →
`FUN_1800f02f0`, рендер `FUN_180105990`; если spec-кэша нет — исключение `GetSecSpec returned null`).

**r12 (батч-проба):** одна команда `subscribe` с тремя `<secid>` в одном `<alltrades>`:
- RQ_SUBSCRIBE — один кадр на 3 элемента: `5067 002a 04 | 017b 0025 04 | 001a 0025 04`;
- **RQ_SEC_SPEC (15) — один кадр: `5067 017b 001a`** (список W secid, без счётчика и терминатора;
  порядок — обратный порядку элементов в XML);
- ответ — один кадр 265 байт, состоящий из трёх записей (по одной на бумагу).

## Запрос RQ_SEC_SPEC (15) — S/D

Payload = простой список `W secid` без счётчика: r11 отдельные кадры `001a`, `273b`, `5067`
(по одному на подписку), r12 батч `5067017b001a`. Пример из старого захвата
`00040379017b001a` — тот же формат: 4 secid (`0004`, `0379`, `017b`, `001a`).
Отправляет `FUN_1803bac90` через `FUN_180355d50(type=0xf,"RQ_SEC_SPEC")`; тот же функция читает ответ.

## Ответ RQ_SEC_SPEC — D, S

Payload = подряд идущие записи (парсер `FUN_1803bac90`, строки 220–341 деки):
`W secid; G mask (wide packed, FUN_1801bd9b0);` далее значения по установленным битам mask
**в порядке возрастания бита** (конец потока — 0xffff или конец буфера). После разбора
`symbol+0xec = 1` (spec загружен).

| Бит | Тип | Поле структуры (offset) | Смысл (S/D) |
|---|---|---|---|
| 0 | Z75 | symbol+0x1f | secname |
| 1 | Z9 | symbol+0x190 | pname |
| 2 | Z4 | symbol+0x1d8 | валюта режима (на демо `RUR`) |
| 3 | G | symbol+0x1a0 | facevalue (в единицах 10^-decimals режима) |
| 4 | L | symbol+0x1a8 | (I) |
| 5 | L | symbol+0x1ac | FUT: clearing_price |
| 6 | L | symbol+0x1e0 | (I) |
| 7 | L | symbol+0x1e4 | (I) |
| 8 | L | symbol+0x1b0 | mat_date (дни Unix) |
| 9 | L | symbol+0x1b4 | coupon_value (/100) |
| 10 | L | symbol+0x1b8 | coupon_period (дни) |
| 11 | L | symbol+0x1bc | coupon_date (дни Unix) |
| 12 | G | symbol+0x1c0 | (I) |
| 13–16 | L | +0x1c8/0x1cc/0x1d0/0x1d4 | (I) |
| 17–20 | L | +0x1e8/0x1ec/0x1f0/0x1f4 | 19 = minprice, 20 = maxprice (FUT) |
| 21–23 | B | +0x19a/0x19b/0x19c | (I) |
| 24,25 | G | local | FUT: buy_deposit, sell_deposit (/100) |
| 26,27 | G | local | (I) |
| 28,29 | B | +0x240/0x241 | (put_call и пр. — I) |
| 30 | Z14 | — | (I) |
| 31 | Z21 | — | (I) |
| 32 | Z3 | — | currencyid (`RUR` → в XML `RUB`) |
| 33 | L | +0x278 | (I) |
| 34 | B | +0x27c | (I) |
| 35 | G | +0x280 | (I) |
| 36 | Z12 | — | isin |
| 37 | Z20 | — | (I) |
| 38,39 | L | +0x244/0x248 | (I) |
| 40 | Z160 | — | url профиля (на демо у SiZ6) |
| 41 | Z100 | — | (I) |

**Сверка S:** все три ответа r11 разобраны по этой таблице до конца (0 лишних байт): SBER — mask
`0x1d0060503d`, облигация — `0x1d00405f3f`, SiZ6 — `0x10d4358c125`. Значения совпали с
`<sec_info>`: SiZ6 `clearing_price=71478`, `minprice=67699`, `maxprice=75257`,
`buy_deposit=7516.86`, `sell_deposit=7601.48`; облигация `facevalue=10000000` (при decimals=4 →
`1000.0000`), `coupon_value=3441` (→34.41), `coupon_period=182`, `coupon_date=20782` дней
(→25.11.2026), `mat_date=21692` дней (→23.05.2029), `isin=RU000A0ZYUA9`; SBER `facevalue=300`
(decimals=2 → 3.00), `isin=RU0009029540`.

## `<sec_info>` — D, S

Рендер `FUN_180105990` из каталога+spec: `<sec_info secid="N">` + элементы в порядке:
`secname, seccode, market, pname, mat_date, clearing_price, minprice, maxprice, (bgo_c, bgo_nc,
bgo_buy для опционов | buy_deposit, sell_deposit для фьючерсов), coupon_value, coupon_date,
coupon_period, facevalue, put_call, opt_type, accruedint, point_cost, lot_volume, isin, regnumber,
buybackprice, buybackdate, currencyid` + `</sec_info>`. Пропускаются пустые/нулевые поля.
Отправка `FUN_1800dba90`. На демо так пришли 5 колбэков (см. фикстуру).

## Файлы

| Файл | Что внутри |
|---|---|
| `secinfo-live.hex.gz` | gzip(hex): 281 байт — три ответа RQ_SEC_SPEC подряд (records secid 26, 10043, 20583) |
| `secinfo-dll.xml.gz` | gzip(text): 5 колбэков `<sec_info>` (3 из формы `security`, 2 из формы `secid`), по одному на строку |
| `secspec-request.hex` | `5067017b001a` — payload RQ_SEC_SPEC из батч-пробы r12 (W secid list) |

## Как использовать в тестах

- `TestSecSpecPayloadMatchesDLL`: для подписки на secid-list [26,379,20583] (обратный порядок
  XML-элементов) payload = `5067017b001a`.
- `TestSecInfoMatchesDLL`: разобрать `secinfo-live.hex.gz` по таблице масок в структуры spec,
  разложить по каталогу (из `startup-demo.hex`/`main1-demo.hex.gz`) и отрендерить `<sec_info>`;
  сравнить с `secinfo-dll.xml.gz` (порядок: SBER, облигация, SiZ6, SBER, SiZ6 — как в захвате).

## Обезличивание

Spec и `<sec_info>` — рыночные данные бумаг (имена, даты, ISIN, ГО), логинов/клиентов/юнионов/
UID/счетов в них нет; secid/seccode — публичные идентификаторы. Сырые cap/логи DLL — в `.re-run/`.

## Не проверено

- Смысл бит 4,6,7,12–17,21–23,26,27,30,31,33–35,37–39,41 (встречались значения, но соответствие
  полям PDF 4.21 не установлено) — I.
- Поведение при отсутствии spec-кэша у `get_securities_info` (D: исключение `GetSecSpec returned
  null`; живьём не воспроизводили) — I.
- Точное правило масштаба facevalue (совпало с decimals режима для SBER=2 и облигации=4) — S на
  двух бумагах, полное правило — I.
- Формат ответа для бумаг с пустыми полями (маска без бита 0) — I.
