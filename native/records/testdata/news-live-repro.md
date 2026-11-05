# news-live: S-фикстуры новостей из живой сессии на проде (2026-11-05, read-only)

Снято DLL 6.47.2.26.4 под Wine 11.21 через §tools/tcproxy§ с прод-сервера. Только read-only:
§connect§, §get_old_news§, §get_news_body§, §disconnect§. Сырьё — §.re-run/s/{news1,newsbody2}/§
(вне git).

## Файлы

| Файл | Что |
|---|---|
| §news-getnews-headers-request.hex§ | C>S §type=73§ payload get_old_news count=5 (8 Б) |
| §news-getnews-body-request.hex§ | C>S §type=73§ payload get_news_body news_id=15979131 (10 Б) |
| §news-header-live-inject.hex§ | запись RQ_MAIN §0x36 flags=1§ (70 Б, заголовок id=15979127) |
| §news-header-live-dll.xml§ | ожидаемый колбэк §<news_header>§ (S) |
| §news-body-live-inject.hex§ | запись RQ_MAIN §0x36 flags=2§ (1891 Б: id + L len + package int32+zlib) |
| §news-body-live-dll.xml§ | ожидаемый колбэк §<news_body>§ (S, полный текст новости) |

## S-факты

- **Reply §type=73§ всегда пустой** (len=0) — и для заголовков, и для тела (S, оба прогона).
  Данные приходят записями RQ_MAIN и колбэками; прежний I из task-56 закрыт.
- Заголовки: RQ_MAIN §0x36 flags=1§ = §L id, L timestamp, Z16 source, Z128 title, B flag§ (S:
  flag=0x80 у снятого заголовка; source/title — C-строки CP1251; §<news_header>§ O-форма 1:1).
  В этом захвате заголовки пришли именно §0x36 flags=1§ (записи §0x35§ не встречались — I, не
  исключено, что зависят от вида подписки/сервера).
- Тело: RQ_MAIN §0x36 flags=2§ = §L id, L package_len, package§, где package = §int32
  uncompressed_size + zlib§ (S: uncompressed=1658, zlib §78 da§; §package_len§ в записи = 1881;
  декодирование §package_len§ как packed-L требует уточнения — I).
- §get_old_news§ запрос (S): §L from=0, L to=2147483647, B count, B 1§ (payload §00 f4ffffff 07 05
  01§); после появления стора DLL подставляет §L from = минимальный id§ и §L to = from+1§ (S:
  второй вызов count=1 → from=15979126, to=15979127) — подтверждает D-находку §lastId = MIN§.
- §get_news_body§ запрос (S): §L news_id, L news_id, B 1, B 2§ (payload §b3273d0f b3273d0f 01 02§).
- Время в §<timestamp>§ — Unix-секунды, печатаются как серверное локальное время (S: 1793820519
  → 04.11.2026 19:28:39; UTC (значения совпадают с UTC-календарём)).

## Воспроизведение

```bash
set -a; . ~/re/prod.env; set +a
.re-run/t49/prod-capture.sh news1 '<command id="get_old_news" count="5"/>'
# затем в ТОЙ ЖЕ сессии (store живёт в рамках сессии):
.re-run/t49/prod-capture.sh newsbody2 '<command id="get_old_news" count="5"/>' sleep:4s \
  '<command id="get_news_body" news_id="15979127"/>'
```

Обезличивание: персональных данных нет; текст новости — публичный (FINAM), оставлен как есть.
