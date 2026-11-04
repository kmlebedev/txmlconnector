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

## `get_forts_positions` и `<forts_position>` — D + O (оракул reply 42)

FORTS-позиции приходят **не** через RQ_MAIN, а через ответ **RQ_CLIENT_HOLDINGS (42)** на команду
`<command id="get_forts_positions"/>` (обменник `FUN_1803b3e50`, `FUN_180355d50(...,0x2a,
"RQ_CLIENT_HOLDINGS")`). Формат ответа (D, сверено оракулом):

```
G ×8            ; заголовок (нули); при сервере новее 0x4b читаются ещё G ×2 (всего 10)
loop:
  L client_id   ; -1 — конец списка
  W secid
  G startnet; G todaybuy; G todaysell; G totalnet; G openbuys; G opensells; G varmargin
  B mask
    0x01 -> G ? ; если > -1: L ?
    0x02 -> G ?
    0x04 -> G ?
    0x08 -> G ×4 (optmargin/expirationpos/…)
```

XML-строитель `FUN_18011dcc0` (литеры `<forts_position>` `0x18082f530`/`0x180837900`,
`<forts_money>` `0x18082e4c0`). Оракул (O): в копию расшифрованного дампа добавлен S>C кадр
type=42 с сконструированным ответом (8 G-нулей; запись `client=4096, secid=20583`; G=100..700;
`mask=0`; терминатор `L=-1`), harness шлёт `get_forts_positions`; DLL отдаёт:

```xml
<positions><forts_position><client></client><secid>20583</secid><union></union><seccode>SiZ6</seccode>
<markets><market>4</market></markets><startnet>100</startnet><openbuys>500</openbuys>
<opensells>600</opensells><totalnet>400</totalnet><todaybuy>200</todaybuy><todaysell>300</todaysell>
<optmargin>0.0</optmargin><varmargin>7.0</varmargin><expirationpos>0</expirationpos></forts_position>
<forts_money><client>TESTCLIEN</client><union></union><markets><market>4</market></markets>
<shortname>Деньги FORTS</shortname><blocked>0.0</blocked><varmargin>7.0</varmargin>
<current>0.0</current><free>0.0</free></forts_money></positions>
```

Маппинг: `startnet`=G1, `todaybuy`=G2, `todaysell`=G3, `totalnet`=G4, `openbuys`=G5,
`opensells`=G6, `varmargin`=G7 (делится на 100); `optmargin`/`expirationpos` — из блока mask&8 (I).
Демо-счёт FORTS-позиций не имеет (S) — форма снята оракулом; на живом потоке не проверена.

## Команда `get_forts_positions` — S/O

`<command id="get_forts_positions"/>` → `<result success="true"/>`; DLL отдаёт колбэк `<positions>`
(на демо — только `<money_position>`; после инъекции бумажной позиции — `<sec_position>`).

## Фикстуры (native/records/testdata)

| Файл | Что | Статус |
|---|---|---|
| `position-sec-inject.hex` | `0x02*` клиент TESTCLIEN (id 4096) + `0x3b*` бумажная позиция SBER (60 Б) | O |
| `position-sec-dll.xml` | ожидаемые колбэки: `<client id="TESTCLIEN">` + `<positions><sec_position>…` | O |
| `snapshot-tail-live.hex.gz` (ранее) | живой конец загрузки демо, включает `<money_position>` | S |
| `position-forts-inject.hex` | тело ответа S>C type=42 (8 G-нулей; запись FORTS SiZ6; mask=0; `L=-1`), 29 Б | O |
| `position-forts-dll.xml` | ожидаемый `<positions><forts_position>…<forts_money>…` | O |

## Не проверено
- `<forts_position>` на живом потоке (демо без FORTS-позиций, S); блоки mask-полей
  (optmargin/expirationpos и др.) — I.
- Денежные поля bought/sold/ordbuy/ordsell/comission: точные источники в магазине — I.
- `<register>` при `micex_registers=true` — D/S по connect.md, на живом не перепроверялось в этой задаче.