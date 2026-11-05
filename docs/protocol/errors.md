# Ошибки сервера и DLL: коды, тексты, правила рендера (Task 64 / T29)

Дата: 2026-11-05. Источник: RE-1 (декомпилятор DLL 6.47.2.26.4) + RE-3 (оракул: replay-server +
DLL под Wine, только loopback). Статусы: **S** — демо-сервер, **O** — оракул DLL, **D** —
декомпилятор, **I** — вывод. Живых торговых команд в этой карточке не было: RQ_TRANSACT-ответы
подставлялись оракулом на записанной сессии.

## Как DLL рендерит ошибки (D)

- **Конструктор ошибки** `FUN_180055840`: хранит код (`param_3` — язык/режим, `param_4` — код
  ошибки) и форматирует текст; **строка формата кода** берётся из таблицы
  `FUN_180055f00(code, lang)`: база `.data:0x1809bb110`, шаг **0x18 (24)**: `{u64 code; char*
  en; char* ru}`; код на `+0`, EN-указатель на `+8`, RU — на `+0x10`; диапазон кодов **0..66**;
  `lang == 0` → RU, иначе EN.
- **Префикс кода в сообщении**: формат `"{%d} "` (`0x180826574`, функция `FUN_1800564a0`) —
  поэтому локальные ошибки выглядят как `{8} Неверное значение параметра 'szClient'.` (O: такие
  тексты сняты в карточках T23/T26/T28).
- **XML результата** (`FUN_1800a5030`, диспетчер команд): успех —
  `<result success="true"%1%/>` (`0x180829ac4`); ошибка без сообщения —
  `<result success="false"/>`; ошибка с сообщением —
  `<result success="false"><message>%1%</message></result>` (`0x180829a70`).
- **Диспетчер**: неизвестная команда → `"Unknown command."`; команда с флагом «нужно соединение»
  без соединения → `"Cannot process this command without connection."` (`FUN_1800a5030`, D; ниже
  O-подтверждение для `set_csp`/`authenticate`).

## Внутренняя таблица кодов DLL (D)

Коды 0..66, база `0x1809bb110`, шаг 0x18. Полная таблица (RU | EN), сгенерирована из DLL
скриптом `.re-run/t49/emit-err-table.py` (вне git):

| Код | RU | EN |
|---|---|---|
| 0 | Ошибка форматирования сообщения. | Message format error. |
| 1 | Не выполнен метод Start(). | Start() is not completed. |
| 2 | Лог-файл "%s" ошибка открытия %u. | Log file "%s" open error %u. |
| 3 | Библиотека остановлена. | Library stopped. |
| 4 | Вне расписания соединений. | Out of connecting schedule. |
| 5 | Соединение с сервером не установлено. | Connection to server failed. |
| 6 | Не загружены рыночные данные. | Loading market data. |
| 7 | Восстановление потерянного соединения. | Reconnecting. |
| 8 | Неверное значение параметра '%s'. | Invalid parameter '%s'. |
| 9 | Нет соединения с сервером. | No connection to server. |
| 10 | Неверные значения параметров '%s' и/или '%s'. | Invalid parameters '%s' and/or '%s'. |
| 11 | Недопустимое сочетание параметров '%s' и '%s'. | Illegal combination of parameters '%s' and '%s'. |
| 12 | Торговые операции в инструменте не доступны для клиента. | Client can not trade the instrument. |
| 13 | Неизвестный код юниона. | Unknown Union code. |
| 14 | Клиент не соответствует юниону. | Client does not belong to the union. |
| 15 | Нет подходящего счёта для торговли этим инструментом. | There is no eligible account for trading this security. |
| 16 | Неоднозначность торгового счёта. Нужно явно указать код клиента. | Ambiguous trade account, the client code should be specified explicitly. |
| 17 | Неизвестный код клиента. | Unknown client code. |
| 18 | Неизвестный код юниона. | Unknown Union code |
| 19 | Должен быть указан клиент и/или юнион. | Client and/or union should be specified. |
| 20 | Неверное значение параметра 'dPrice' для рыночной заявки. | Invalid value of parameter 'dPrice' for market order. |
| 21 | Клиент не допущен к торгам. | Client has no access. |
| 22 | Торговые операции по инструменту недоступны. | Transactions unavailable for this instrument. |
| 23 | Недопустимое значение параметра '%s' для данного инструмента. | Invalid value of parameter '%s' for this instrument. |
| 24 | Неверное значение параметра '%s' - слишком много значащих цифр в дробной части. | Invalid value of parameter '%s' - too many significant digits in fractional part. |
| 25 | Неверное значение параметра '%s' - недопустимый шаг цены. | Invalid value of parameter '%s' - illegal price step. |
| 26 | Неверное значение параметра '%s' - недопустимый шаг для данного значения цены. | Invalid value of parameter '%s' - illegal step for this price. |
| 27 | Неверное значение параметра '%s' - инструмент '%.33s'. | Parameter '%s' invalid value - instrument '%.33s'. |
| 28 | Юнион не найден. | Union not found. |
| 29 | Неверные значения параметров '%s' / '%s' / '%s'. | Invalid values of parameters '%s' / '%s' / '%s'. |
| 30 | Неверные значения параметров '%s' / '%s'. | Invalid values of parameters '%s' / '%s'. |
| 31 | Недопустимый тип транзакции для данного инструмента. | Invalid transaction type for this instrument. |
| 32 | Недопустимое значение параметра '%s' для данной транзакции. | Invalid value of parameter '%s' for this transaction. |
| 33 | Значения параметров 'szOldPWD' и 'szNewPWD', 'byOldTM' и 'byNewTM' одинаковы. | Parameters 'szOldPWD' and 'szNewPWD', 'byOldTM' and 'byNewTM' are equal. |
| 34 | Неверный пароль/ТМ. | Invalid password/TM. |
| 35 | Неверный запрос для данного сервера. | Invalid request for this server. |
| 36 | Неверный формат запроса. | Invalid request format. |
| 37 | Заявка отменена | Order is cancelled |
| 38 | Время действия заявки истекло. | Order timed out. |
| 39 | Отклонено Брокером | Order rejected by Broker. |
| 40 | Отклонено Биржей. | Order rejected by Exchange. |
| 41 | Истек срок выставления заявки. | Order watching period is over. |
| 42 | Заявка снята. | Order is cancelled. |
| 43 | SL: защитное время. | SL: protection time. |
| 44 | SL исполнен. | SL executed. |
| 45 | TP: защитное время. | TP: protection time. |
| 46 | TP исполнен. | TP executed. |
| 47 | TP: режим коррекции. | TP: correction mode. |
| 48 | Ожидание связанной заявки. | Awaiting for linked order. |
| 49 | TP: режим коррекции, защитное время. | TP: correction mode, protection time. |
| 50 | Статус заявки не определен. | Undetermined order state. |
| 51 | Заявка отклонена Партнером. | Order rejected by Partner |
| 52 | Не наступило время активации. | Not activated yet. |
| 53 | Заявка снята Биржей. | Order cancelled by Exchange. |
| 54 | Неактивная заявка. | Inactive order. |
| 55 | Запрос на снятие данной заявки не был передан на Биржу за отведенное время. | Cancel order request failed (timed out in gateway). |
| 56 | Нет данных. | No data. |
| 57 | Юнион '%s': Юникат %d не найден. | Union '%s': category %d not found. |
| 58 | Юнион '%s' Юникат %d: Шаблон %d не найден. | Union '%s' category %d: URT %d not found. |
| 59 | Клиент '%s' Маркат %d: Шаблон %d не найден. | Client '%s' category %d: UT %d not found. |
| 60 | Клиент '%s' не допущен к торгам. | Client '%s' has no access. |
| 61 | Соединение с сервером не установлено после останова сервера. | Connection to server failed after server shutdown. |
| 62 | Вне расписания соединений после останова сервера. | Out of connecting schedule after server shutdown. |
| 63 | Дублирование идентификатора '%s' (%d). | Duplication of identifier '%s' (%d). |
| 64 | Неверное значение параметра '%s' (%d). | Invalid value of parameter '%s' (%d). |
| 65 | Неоднозначность номера заявки. Нужно явно указать код инструмента. | Ambiguous order number, the instrument code should be specified explicitly. |
| 66 | Недопустимое значение параметра транзакции '%s' для данного сервера. | Invalid value of transaction parameter '%s' for this server. |

## RQ_TRANSACT (14): коды ответа сервера (D + O)

Сервер присылает первым байтом ответа **код результата**; тексты — не из таблицы выше, а из
Win32-ресурса строк (RT_STRING, язык **1049/RU**): **ID = 615 + код** (resource `.rsrc`; D —
разбор `.re-run/t49/dump-rsrc-strings.py`). O-подтверждение (fake reply `B code` на replay):
новый код → `<result success="false"><message>…</message></result>`:

| Код | O-текст (RU) | D-resource ID |
|---|---|---|
| 1 | Клиент не допущен к торгам. | 616 |
| 2 | Трейдер не допущен к торгам. | 617 |
| 3 | Клиент не обслуживается данным Трейдером. | 618 |
| 4 | Сделки с собой запрещены. | 619 |
| 5 | Попытка снять несуществующую заявку. | 620 |
| 6 | Транзакции не разрешены. | 621 |
| 0 | успех: `<result success="true" transactionid="N"/>` (O, task 15: ответ `00 10`) | — |

- Коды с параметрами (`%s`/`%I64d`) в resource-строках: 7 (`Не хватает средств по
  обеспечению…`), 8 (`Превышен индивидуальный лимит денежного кредита…`), 9 (`Неверный формат
  запроса.`), 10 (`Обработка предыдущей заявки…`), 15, 18, 19, 21, 22, 23, 24, 25, 26, 27, 28,
  29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52,
  53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66 — D (ID 622..681); O снято только 1..6
  (код 7 при подстановке одного байта дал `Unknown error`, т.к. формат требует `%s` — I).
- Пустой reply (len=0) → `<result success="false"><message>Unknown error</message></result>` +
  лог `RQ_TRANSACT … return incorrect buffer` (O, task 15).
- Неизвестный код вне таблицы сервера → `Unknown error` (O: код 34 на replay дал `Unknown error`,
  т.к. 34 — не код сервера, а код внутренней таблицы; **I**: маппинг server→library кодов DLL, если
  он есть, не найден).

## RQ_HISTORY (11) / RQ_HISTORY_EXT (148) (S/D/O)

- Ошибок-текстов нет: ответ — записи R_HISTORY 0x09, последнее поле — `L status` ∈ [0..3]
  (0 — данных больше нет, 1 — порция выдана, 2 — есть ещё, 3 — ?); колбэк `<candles status="N">`
  (S+O, см. `docs/protocol/history.md`). Отдельного кода ошибки/текста в ответе не найдено (D).
- Для T29 `TestServerErrorTexts` RQ_HISTORY не даёт текст — можно проверять только статусы (I).

## RQ_PWD (46) и RQ_CHANGE_PWD (3) (D + O)

| Кадр | Поле | Значение | Поведение DLL |
|---|---|---|---|
| RQ_PWD reply | `B status` | 0 | успех (далее сессионные ключи) |
| RQ_PWD reply | `B status` | 0x0e | далее C-строка сообщения (D: `FUN_1801bd170`; Go: `parsePwdReply`) → `<result success="false"><message>текст</message></result>` (D) |
| RQ_PWD reply | `B status` | прочие | ошибка авторизации; текст — из внутренней таблицы по коду (D) / I |
| RQ_CHANGE_PWD reply | `B status` | 0 | `<result success="true"/>` (O, task 57) |
| RQ_CHANGE_PWD reply | `B status` | 1 | `Wrong oldpass specified.` (O) |
| RQ_CHANGE_PWD reply | `B status` | 2 + C-строка | текст из строки в `<message>` (O) |
| RQ_CHANGE_PWD reply | пусто | — | фатально: `RQ_CHANGE_PWD incorrect server buffer`, соединение рвётся (O) |

- Полный список кодов RQ_PWD не снят: оракулом подменить reply нельзя (сессионные ключи считаются
  из него, подмена ломает логин) — **I**; D-путь: `FUN_1801bd170` читает текст только для 0x0e.
- Проверка `TestServerErrorTexts` для RQ_PWD: минимум — статус 0x0e + строка; остальные коды — I.

## Известные O-тексты локальных ошибок (примеры, префикс кода в фигурных скобках)

| Текст в `<message>` | Код таблицы | Источник |
|---|---|---|
| `{8} Неверное значение параметра 'szClient'.` | 8 (`Invalid parameter '%s'.`) | O: margin/mct-карточки |
| `{8} Неверное значение параметра 'szUnion'.` | 8 | O |
| `{8} Неверное значение параметра 'TransactionID'.` | 8 | O: cancelnegdeal |
| `{37} Заявка отменена` | 37 (`Order is cancelled`) | O: task 15 |
| `Wrong oldpass specified.` | — (RQ_CHANGE_PWD status 1) | O: task 57 |
| `You have to specify at least one CSP.` / `Unsupported CSP type.` | — (локальные строки set_csp) | D |
| `Cannot process this command without connection.` / `Unknown command.` | — (диспетчер) | D+O |

## Рекомендации для TestServerErrorTexts (T29)

1. Таблица `codes → text` для RQ_TRANSACT: коды 1..6 — O-тексты выше (golden), 7..66 — D-тексты из
   resource (ID 615+код), с явной пометкой «D, O не снято».
2. Тексты `{code} …` — использовать таблицу DLL (0..66) с префиксом `"{code} "`; примеры `{8}`/`{37}`
   зафиксировать в тесте.
3. RQ_HISTORY — только статусы `0..3` (без текстов); RQ_PWD — только `0x0e+text`; RQ_CHANGE_PWD —
   `0/1/2` (O).
4. Неизвестный/пустой ответ — `Unknown error` (O); пустой RQ_CHANGE_PWD — фатальный (O).

## Не проверено / I

- Маппинг `server code → library code` (если DLL его делает) — не найден; O показал, что код 34
  (внутренней таблицы) транзакцией не принимается и даёт `Unknown error`.
- Полные коды RQ_PWD (кроме 0/0x0e) и тексты — I.
- Форматы `%s`/`%I64d` в resource-строках сервера: подстановка значений не проверялась (I).
- EN-ресурсы (язык 1033) в DLL не найдены (D: RT_STRING только 1049 — I).

## Источники

D: `tools/re/ghidra/XrefDecomp.java` по `txmlconnector64-6.47.2.26.4.dll`; функции — выше.
O: `tools/replay-server` + `tools/tcproxy` + DLL под Wine 11.21; скрипты `.re-run/t49/mk-err-dump.py`,
`emit-err-table.py`, `dump-rsrc-strings.py` (вне git); прогоны `.re-run/t49/err{T2,T3,P1}`.
