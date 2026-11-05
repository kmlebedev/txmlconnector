# ticks-live: S-фикстура тиков из живой сессии на проде (2026-11-05, read-only)

Снято DLL 6.47.2.26.4 под Wine 11.21 через §tools/tcproxy§ с сервера в основной торговой
сессии. Торговых команд не было: только §connect§, §subscribe_ticks§, §disconnect§ (read-only).
Сырьё — §.re-run/s/ticks2/§ (вне git).

## Что в фикстуре

| Файл | Что |
|---|---|
| §ticks-live-inject.hex§ | payload RQ_MAIN-ответа (27 Б): одна запись §0x5a§ — один тик SBER |
| §ticks-live-dll.xml§ | ожидаемый колбэк DLL §<ticks><tick>…</tick></ticks>§ (S, из того же прогона) |

Тик: secid 2412 (SBER, TQBR prod), tradeno 18027673956, time 1 793 865 395 (Unix; 05.11.2026
07:56:35 MSK), price 29 045 (290.45), quantity 45, period §N§, buysell §B§.

## Команда (S)

```xml
<command id="subscribe_ticks"><filter>true</filter>
  <security><board>TQBR</board><seccode>SBER</seccode><tradeno>0</tradeno></security>
</command>
```

- O/S: с элементом §<secid>§ DLL отвечает §Wrong security format.§; нужны **§<board>§ по коду и
  §<seccode>§** + дочерний §<tradeno>§.
- §unsubscribe_ticks§ DLL не знает: §Unknown command.§ (S).
- C>S §type=120§ payload (S): §01 096c 004b 00 0602 004b 00§ = §B filter=1; {W secid=2412, W
  board=75, G tradeno=0}, {W secid=1538, W board=75, G tradeno=0}§. На проде board TQBR = **75**
  (на демо — 37), secid SBER=2412, GAZP=1538 (S). S>C reply §type=120§ пустой.
- **RQ_TICKS (13) на проде не отправлялся** (S: в захвате за 25 с нет ни одного C>S/ S>C type=13);
  тики приходят в RQ_MAIN (type=5) — гипотеза §history.md§ про поллинг type=13 опровергнута.

## Запись RQ_MAIN 0x5a = TICK (D + S)

Диспетчер: case §0x59§ → §FUN_1803bf6c0§ → §FUN_1803858b0§ (строки §TICK: incorrect buffer 1/2§).
Тело — **цикл до §W secid == 0xffff§**:

```
W secid; W board; G tradeno; L time; L price; L quantity; B period; B buysell; L X; L Y
```

- §G tradeno§ и §L time§ — **накопительные дельты** (D: суммы §local_a0 += local_90§, §local_d4 +=
  local_88§; при нуле time подставляется начало дня §FUN_18017c360§); в снятой записи время уже
  абсолютное Unix-время (S: 1 793 865 395 → 07:56:35 MSK).
- §price§/§quantity§ — packed-L; §period§/§buysell§ — по байту (§N§/§B§); §X§/§Y§ — L (S: 0 и 803;
  семантика — I).
- S-декод записи: §5a 096c 004b 73e02f…§ (полный hex — в фикстуре).

## Воспроизведение

```bash
set -a; . ~/re/prod.env; set +a
.re-run/t49/prod-capture.sh ticks2 \
  '<command id="subscribe_ticks"><filter>true</filter><security><board>TQBR</board><seccode>SBER</seccode><tradeno>0</tradeno></security></command>' sleep:25s
```

Обезличивание: персональных данных нет (тик — биржевая сделка; tradeno — публичный номер сделки).
