# changepwd: формат запроса RQ_CHANGE_PWD (3) и ответы — оракул (RE-3)

Снято DLL 6.47.2.26.4 под Wine 11.21 против `tools/replay-server` (только loopback): DLL
подключается к проигрывателю записанной сессии, команда `change_pass` отправляет настоящий кадр
RQ_CHANGE_PWD, а мы перехватываем его `tcproxy`. **Живая смена пароля на реальном сервере не
выполнялась и запрещена** (карточка task-57); используется синтетический пароль.

## Запрос (`changepwd-request.hex`, 56 байт)

```
SHA1(oldpass) 20 байт
00 × 8
SHA1(newpass) 20 байт
00 × 8
```

Пример снят для синтетических паролей `OldPass2!` / `TestPass1!` (значения не секретны):

```
sha1("OldPass2!") = 42c124f7e49351d16642652e905f58f2be1c7ef4
sha1("TestPass1!") = 79596833eb2f397613f47f43c1b758760b276861
payload = 42c124…7ef4 0000000000000000 795968…6861 0000000000000000
```

Пароли передаются **несолёным SHA-1** (D: `FUN_1801bc080` = SHA-1 + 12-байтовая добивка
`FUN_18043d370`, в провод пишутся первые 20 байт; O: payload побайтно совпал с SHA-1).
8-байтовые поля — нули (зарезервированы; назначение — I). Транспортный кадр шифруется
сессионным ключом, как и остальные запросы (O: в дампе payload после расшифровки).

## Ответы (`changepwd-reply-*.hex`)

Ответ RQ_CHANGE_PWD — прямой reply-кадр type=3: `B status`; при status=2 далее C-строка текста
(D: `FUN_1803b18d0`):

| Reply | status | Результат DLL |
|---|---|---|
| `changepwd-reply-ok.hex` = `00` | 0 | `<result success="true"/>` |
| `changepwd-reply-wrong-old.hex` = `01` | 1 | `<result success="false"><message>Wrong oldpass specified.</message></result>` |
| `changepwd-reply-error.hex` = `02` + "Server says: bad password" + NUL | 2 | `<result success="false"><message>Server says: bad password</message></result>` |

Пустой reply (`len=0`) — **фатально**: DLL пишет `RQ_CHANGE_PWD incorrect server buffer` и
закрывает соединение (`<server_status connected="false">`); в oracle-прогонах reply нужно
подставлять всегда (O).

## Воспроизведение

```bash
set -a; . ~/re/demo.env; set +a
.re-run/t49/oracle-margin.sh <name> -reply-to 3 -inject native/session/testdata/changepwd-reply-ok.hex -- \
  '<command id="change_pass" oldpass="OldPass2!" newpass="TestPass1!"/>'
```

Захват C>S: `type=3 len=56 <hex>`; S>C: `type=3 <reply hex>`. Полный вывод —
`.re-run/t49/{passwd1,pwdok,pwdwrong,pwderr2,pwdval,pwdweak,pwdlong,pwdsemi,pwdsame}/`
(вне git).

## Обезличивание

Пароли в фикстуре — синтетические (`OldPass2!`/`TestPass1!`), не совпадают с демо-паролем;
реальные пароли не снимались и не печатались. В payload видны только SHA-1 синтетических строк.
