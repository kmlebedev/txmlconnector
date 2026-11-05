# demo trade cycle: neworder/moveorder/makeordown/cancelorder на ДЕМО (S, 2026-11-05)

Снято DLL 6.47.2.26.4 под Wine 11.21 через `tools/tcproxy` с **демо-сервера** (только
`~/re/demo.env`), по согласию пользователя: минимальный цикл, 1 лот SBER, цена далеко от рынка,
отмена сразу. На проде торговых команд НЕ было. Сырьё — `.re-run/demo/{new1,cyc4,cyc5,cyc6,...}`
(вне git); клиент обезличен (TESTCLIENT), orderno занулён.

## Фикстуры

| Файл | Что |
|---|---|
| `transact-neworder-live.hex` | payload RQ_TRANSACT neworder (24 Б, client обезличен): `B 01, B 00, W secid=26, W board=37, G client, B 'B', B 00, L price, L qty`, хвост нулей |
| `transact-cancelorder-live.hex` | payload cancelorder (5 Б): `B 02, B 00, L transactionid, B 00` |
| `transact-moveorder-live.hex` | payload moveorder (5 Б): `B 02, B 01, L transactionid, B 00` (снят для уже снятой транзакции; сервер ответил `[151]Объект операции не найден`) |
| `demo-trade-repro.md` | этот файл |

## O-факты демо-сессии

- `neworder` price=285.50, qty=1, buysell=B, unfilled=PutInQueue → `<result success="true"
  transactionid="90"/>`; payload 24 Б (см. выше); reply type=14: `B status=0, L transactionid, L
  server_time, B 00` (напр. `00 a1 05 94 73 c5 ae 06 00` для txid=90).
- `cancelorder` transactionid=90 → success, payload `02 00 a1 05 00`; колбэк
  `<order transactionid="90" ... status="cancelled"><result>{42} Заявка снята.</result></order>`.
  Отмена работает и в **новой** сессии (DLL шлёт по переданному id).
- Ценовой коридор сервера: заявка с ценой ниже минимума отклоняется:
  `<order ... status="removed"><result>(579)Для выбранного финансового инструмента цена должна
  быть не меньше 284.62</result></order>` (S); в другом прогоне минимум был 284.64 — значение
  плавающее (**S**).
- `moveorder`: для активной заявки, созданной в этой же сессии (txid=90), DLL отвечает
  `{32} Недопустимое значение параметра 'TransactionID' для данной транзакции` и payload **не
  шлёт**; для уже снятых транзакций (txid=20/40) шлёт `B 02, B 01, L txid, B 00`, сервер отвечает
  текстом `[151]Объект операции не найден`. Успешный payload moveorder не снят — **I**.
- `makeordown` требует **атрибут** `transactionid` (D: `FUN_18040cf20`): с
  `<command id="makeordown" transactionid="90"/>` DLL отвечает
  `Cannot make specified transaction_id an own conditional order` (активная заявка не подошла);
  payload RQ_MAKE_COND_OWN не снят — **I**.

## Воспроизведение

```bash
set -a; . ~/re/demo.env; set +a
# prep (read-only): клиентские коды и цена SBER
.re-run/t49/demo-trade.sh prep
# цикл (одна сессия; id транзакции предсказан по наблюдённой последовательности)
.re-run/t49/demo-run.sh cyc \
  '<command id="neworder"><secid>26</secid><client>TESTCLIENT</client><price>285.5</price><quantity>1</quantity><buysell>B</buysell><unfilled>PutInQueue</unfilled></command>' sleep:4s \
  '<command id="moveorder"><transactionid>90</transactionid><moveflag>0</moveflag><price>286</price></command>' sleep:3s \
  '<command id="makeordown" transactionid="90"/>' sleep:3s \
  '<command id="cancelorder"><transactionid>90</transactionid></command>'
```

Активных заявок не оставлено: все созданные заявки сняты (cancelled) или отклонены (removed).
