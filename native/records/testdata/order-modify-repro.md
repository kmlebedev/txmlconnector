# order-modify fixture: происхождение и воспроизведение (RE-3)

Пара `order-modify-inject.hex` + `order-modify-dll.xml` снята оракулом (replay-server + DLL,
только loopback, без реальных торговых команд) и **согласована**:

1. Исходная инъекция (до обезличивания) — `.re-run/t32/inject-ordmod.hex`: два RQ_MAIN-рекорда
   `0x03*` (заявка, transactionid=1) + `0x10*` (`L 1, uint32BE mask=1, B=2`); первые 124 Б
   побайтно совпадают с `order-new-inject.hex` (после обезличивания).
2. Прогон: replay-server с `-dump .re-run/r14/decode-full.txt -inject <исходная> -after 148`,
   harness: connect -> gethistorydata (arm) -> ожидание. DLL отдаёт
   `<orders><order transactionid="1">…<time>00:00:11</time>…<balance>1</balance>…
   <result>{37} Заявка отменена</result></order></orders>` — ровно `order-modify-dll.xml`
   после замены client-строк на `TESTCLIEN` (сравнение 2026-11-04: MATCH true).
3. Обезличивание: client id `22 69 1a` -> `02 00 01` (тот же packed-размер), строки
   `virt/9993` -> `TESTCLIEN` (9 -> 9 байт); значения остальных полей не менялись.

**Важно:** первые 0x03-рекорды `order-new-inject.hex` и `order-modify-inject.hex` ИДЕНТИЧНЫ —
различие XML (`time=00:00:11`/`balance=1` против `time=00:00:01`/`balance=11` у order-new)
возникает из-за второго рекорда `0x10*` (изменение/отмена заявки), а не из-за полей 0x03.
Правка байтов 17/57 в 0x03 (для «побайтного совпадения» с order-new) ломает согласованность с
XML и оракулом не подтверждается — байты восстановлены.

Воспроизведение: replay-server на 127.0.0.1 + `tools/re/run_dll.sh` с командами
connect / gethistorydata / disconnect (см. `docs/protocol/orders.md`, раздел R_ORD_MODIFY).
