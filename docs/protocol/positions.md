# Позиции: `<positions>` — бумажные, денежные, FORTS (Task 20)

Дата: 2026-11-04/05. Источники: RE-1 (декомпилятор `FUN_1803cde40` и XML-строители), RE-2/живой
демо-захват (`r14`), RE-3 оракул (`replay-server` + DLL, только loopback). Статусы: **S** —
демо-сервер, **O** — оракул DLL, **D** — декомпилятор, **I** — вывод.

## Запись `0x3b*` R_CLIENT_ASSET (`FUN_1803cde40`) — D + O

Размерная запись RQ_MAIN. Формат (подтверждён оракулом):

```
L  client_id            ; числовой id клиента (стор клиентов)
W  sym_id               ; 0xffff — денежный актив, иначе secid бумаги
loop:
  B reg_code            ; 0xff — конец списка регистров
  B mask
    0x01 -> G open_balance   (REG_POS_OPENBAL; <saldoin>)
    0x02 -> G balance        (игнорируется)
    0x04 -> L bought
    0x08 -> L sold
    0x10 -> L blocked
    0x20 -> L estimated
  ... (повтор для каждого reg_code)
B mask2
  0x01 -> G commission
  0x02 -> Z15 waposprice   ; FORTS
  0x04 -> Z4 currency      ; имя валюты -> id
```

Демо-запись (S): `client=108178, sym=0xffff, reg=0x01, mask=0x03` → open_balance=30 000 000,
`mask2=0x04, currency=RUR`. Сохраняется в магазин позиций (`FUN_18024d4e0`, `connector+0x120`).

## `<money_position>` — S (демо)

Строитель `FUN_18012a000`. Демо-XML (End of load, r14):

```xml
<positions><money_position><currency>RUR</currency><client>TESTCLIEN</client><union></union>
<markets><market>1</market></markets><asset>FOND_MICEX</asset>
<shortname>Деньги КЦБ ММВБ (RUR)</shortname><saldoin>300000.0</saldoin><bought>0.0</bought>
<sold>0.0</sold><saldo>300000.0</saldo><ordbuy>0.0</ordbuy><ordbuycond>0.0</ordbuycond>
<comission>0.0</comission></money_position></positions>
```

`<saldoin>` = open_balance (делится на 10^scale); `<saldo>` = open_balance + bought − sold;
`<register>` добавляется только при `micex_registers=true` (см. `connect.md`).

## `<sec_position>` (бумажная позиция) — O (новое)

Оракул: инъекция `0x02*` клиента `TESTCLIEN` (id 4096) и затем `0x3b*` с `sym_id=26` (SBER),
`reg=1, mask=3, open_balance=30 000 000, mask2=0x04, currency=RUR`. DLL отдаёт колбэк:

```xml
<positions><sec_position><client>TESTCLIEN</client><union></union><secid>26</secid>
<market>1</market><shortname>Сбербанк</shortname><seccode>SBER</seccode>
<saldoin>30000000</saldoin><saldomin>0</saldomin><bought>0</bought><sold>0</sold>
<saldo>30000000</saldo><ordbuy>0</ordbuy><ordsell>0</ordsell></sec_position></positions>
```

Соответствие: `<client>/<union>` — из стора клиентов, `<secid>/<market>/<seccode>/<shortname>` —
из справочника символов, `<saldoin>` = open_balance, `<bought>/<sold>` = поля магазина,
`<saldo>` = open_balance + bought − sold (D/S), `<ordbuy>/<ordsell>` = ordbuy/ordbuycond/…
магазина (I для точных полей). Обновление позиции в `<orders>`-подобном виде не эмитится; каллбэк
приходит на конец загрузки и по команде `get_forts_positions` (S/O).

## `<forts_position>` — I, блокер

Литерал `<forts_position>` в DLL есть (`0x18082f530`, `0x180837900`), но инъекция `0x3b*` с FORTS
символом (SiZ6 secid=20583, mask=0x3f) **не рендерит** `<forts_position>` (O-негатив): FORTS-позиции
DLL берёт не из `0x3b`-магазина, а из ответа **RQ_CLIENT_HOLDINGS (42)** на команду
`get_forts_positions` (в логе видно `RQ_CLIENT_HOLDINGS return incorrect buffer (1)`); демо-счёт
FORTS-позиций не имеет (S). Нужен либо пользовательский захват с FORTS-позицией, либо RE-3 с
подменой reply type=42 в дампе (формат ответа ещё не разобран) — **блокер** для полной поддержки
FORTS в T20.

## Команда `get_forts_positions` — S/O

`<command id="get_forts_positions"/>` → `<result success="true"/>`; DLL отдаёт колбэк `<positions>`
(на демо — только `<money_position>`; после инъекции бумажной позиции — `<sec_position>`).

## Фикстуры (native/records/testdata)

| Файл | Что | Статус |
|---|---|---|
| `position-sec-inject.hex` | `0x02*` клиент TESTCLIEN (id 4096) + `0x3b*` бумажная позиция SBER (60 Б) | O |
| `position-sec-dll.xml` | ожидаемые колбэки: `<client id="TESTCLIEN">` + `<positions><sec_position>…` | O |
| `snapshot-tail-live.hex.gz` (ранее) | живой конец загрузки демо, включает `<money_position>` | S |

## Не проверено
- `<forts_position>`: layout/XML — блокер (см. выше), нужен FORTS-захват или разбор reply 42.
- Денежные поля bought/sold/ordbuy/ordsell/comission: точные источники в магазине — I.
- `<register>` при `micex_registers=true` — D/S по connect.md, на живом не перепроверялось в этой задаче.