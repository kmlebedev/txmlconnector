# Чек-лист утренней S-сессии (RE-2/RE-4)

**Статус: ожидает S-сессию 2026-11-05 10:00+ МСК** (окно демо 09:50–23:50 МСК).

Порядок: тики → лимиты/портфели → торговый цикл. Общие правила: `set -a; . ~/re/demo.env;
set +a`; DLL под Wine через `tools/re/run_dll.sh` + `tcproxy`-захват; сырьё только в `.re-run`
(gitignored); секреты и коды клиента не печатать; `change_pass`/REPO/RPS не отправлять; после
серии — disconnect.

## 1. Тики (неторговая; согласие есть)

- `subscribe_ticks`: `<command id="subscribe_ticks"><filter>true</filter><security><board>TQBR</board><seccode>SBER</seccode><tradeno>0</tradeno></security></command>`; ждать 30–60 с.
- Снять: C>S type=13 (RQ_TICKS), S>C `<ticks>`/`<tick>`; если 13 нет — S-негатив, повторить filter=false/GAZP.
- Фикстуры: `ticks-live.hex.gz` + `ticks-dll.xml.gz` (client/union/login → TESTCLIENT/TESTUNION/TESTLOGIN, длины сохранить); обновить `docs/protocol/history.md`.

## 2. Лимиты (неторговая)

- `<command id="get_client_limits" client="<код клиента из сессии>"/>`; 2–3 повтора/значения, если доступно.
- Снять: `<clientlimits client="…">` (21 элемент, см. `limits.md`) + reply 43.
- Фикстуры: `limits-reply.hex` (client→4096) + `limits-dll.xml` (client→TESTCLIEN); обновить `limits.md`.

## 3. Портфели (неторговая)

- `get_portfolio`, `get_portfolio_mct`, `get_united_portfolio`, `get_mc_portfolio`.
- Снять: `<portfolios>`/`<portfolio_tplus>`/`<portfolio_forts>`/`<portfolio_currency>`/`<portfolio_mct>`/`<mc_portfolio>`/`<united_portfolio>` + reply-кадры 86/84/85/82.
- Фикстуры `portfolio-*-inject.hex` + `portfolio-*-dll.xml`; если MCT/FORTS пусты — S-негатив честно; обновить `portfolios.md`.

## 4. Торговый цикл (согласие пользователя есть; 1 лот SBER, цена далеко от рынка, отмена сразу)

- `neworder`: secid=26, client=<код>, buysell=B, price≈1.00 (далеко от рынка), quantity=1, unfilled=PutInQueue; снять RQ_TRANSACT payload (type14).
- `moveorder`: transactionid из `<result transactionid=…>`, `moveflag=0`, price/quantity; снять payload type14.
- `makeordown`: тот же transactionid; зафиксировать, отправляет ли DLL `RQ_MAKE_COND_OWN` (type112) или «Unknown command/не поддерживается».
- `cancelorder`: transactionid из moveorder/новой заявки, сразу после; проверить снятие в `<orders>`.
- Фикстуры: `transact-moveorder-payload.hex` (+ makeordown при наличии), client/orderno обезличить; обновить `orders.md`.

## 5. После серии

- Отчёт Lead: матрица «снято / пусто (S-негатив)» по тикам, лимитам, портфелям, moveorder/makeordown; коммиты; карточки не закрывать без приёмки.
- Тайминг: торговый цикл — последним. Если S-сессия не состоится — внешний блокер, T19 остаётся на D с I-пометками.