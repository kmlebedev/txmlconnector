# Фикстуры задачи 12: источники и происхождение

Дата: 2026-11-04. Живые захваты — вне git (.re-run/); в git только обезличенные срезы
рыночных/state-записей (логинов, счетов, юнионов и UID в них нет).

| Файл | Что внутри | Источник |
|---|---|---|
| misc-live.hex.gz | 8×R_FORTS_SESSION (0x6b), 3×R_WRAPPER (0x5f) с вложенным R_EDP_RATE (0x6e), живой AmBOOK (0x5c, пустая книга 20 86 ff), R_SYSPARAMS (0x5b) и пустая state-запись 0x05 | RQ_MAIN-кадры r8–r16 (0x6b/0x6e/0x5f), r10 (AmBOOK), sysparams-inject.hex (RE-3, task-33) |
| sysparams-inject.hex | один R_SYSPARAMS 0x5b: 5b + packed L size (133) + 133 байта blob со строками SP_PERCENT_MARKET_HARD/MAX, SP_PRICE_DEVIATION_HARD/MAX | RE-3 инъекция реальной записи r13 в DLL (task-33); формат — L size + blob (D/S/O) |
| e07-tail-live.hex.gz | полный RQ_MAIN-хвост E07 (41070 байт, 662 записи): R_SEC_NEW/R_SEC_PARAM, R_DATE, R_SEC_RATE, R_CLIENT_ASSET, R_WRAPPER с nested 0x74, R_SYSPARAMS, state 0x05, AmBOOK и хвостовые 0x0a | r13 (decode-full.txt), кадр S>C type=5, содержащий 0x5b |

AmBOOK: тело по RE-1 FUN_1803bea70 — W secid + цикл {B flags, 0xff — конец, условные B/L/L/Z4/L};
живая r10-запись подтверждает пустую книгу (20 86 ff). R_SYSPARAMS: L size + пропуск blob.

Проверки: TestMiscLiveRecords, TestMiscParsersNegative, TestKnownUnseenTagReturnsUnknownRecord,
TestE07TailHasNoUnknownRecords (Records: 662 записи, 0 unknown), TestE07TailHasNoUnknownRecordsAfterLoad
(Client/Stats: UnknownRecords==0).
