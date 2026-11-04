# Spofy Xray-core — тонкий форк официального Xray

Ветка `spofy/main` = официальный тег XTLS/Xray-core + небольшой набор патчей. Каждый патч — отдельный коммит
с объяснением и тестами; всё, что взято из Jolymmiles/Xray-core, помечено `Port of Jolymmiles/Xray-core <sha>`.

База сейчас: **v26.10.31**. Обновления upstream вливаются настоящим `git merge` (не переносом руками), как в
форках бота и кабинета.

## Что изменено относительно официального Xray

| Коммит | Что | Зачем |
|---|---|---|
| Hysteria: bump apernet/quic-go to v0.63.1 | quic-go как в Hysteria 2.13 | приоритет повторных отправок, проверки CRYPTO, меньше памяти |
| REALITY: in-tree copy + legacy ClientHello | `third_party/reality`, опция `requireMlkem` (по умолчанию `false`) | с 26.9.9 официальный REALITY не пускает fingerprint `edge`, `ios`, `qq` |
| Shadowsocks UDP buffer (Jolymmiles c766e4a2) | буфер под размер пакета | UDP-ответ больше ~8 КБ ронял **весь процесс** Xray |
| buf: timer instead of sleeper goroutine (0edd0d60) | `time.Timer` | минус горутина на каждое ожидание при сниффинге |
| Vision: release raw input buffer (25a4a24e) | освобождение ~17 КБ на соединение | память на нодах с VLESS+REALITY+Vision |
| Mux.Cool: TCP carriers only (8e56c2e0) | UDP на v1.mux.cool не запускает worker | возможная утечка при UDP через Hysteria/VLESS |
| xray run: bounded shutdown (729bce29+9117a0c1) | после SIGTERM ждём Close не дольше 10 с | зависший Close не держит полумёртвый Xray при перезапуске rw-node |
| TLS sniffer: fragmented ClientHello (d3fcd37b) | сборка ClientHello из нескольких записей | без этого такой hello обходит доменные блокировки |

### REALITY `requireMlkem`

- не задан / `false` — принимаются и старые ClientHello (только X25519, без гибридной группы вообще), и новые.
  ClientHello, где X25519MLKEM768 есть, проверяются по правилам upstream.
- `true` — поведение официального Xray (только chrome/firefox/safari и т.п.).
- Имя и номер protobuf-поля (100) совпадают с Jolymmiles — конфиги взаимозаменяемы.
- Официальный Xray (проверено на 26.10.31 и 26.7.28) молча игнорирует неизвестное поле: конфиг с
  `requireMlkem` проходит `xray run -test`. Значит, опцию можно держать в общем профиле панели, но на нодах
  с официальным ядром 26.9.9+ она ничего не меняет — там всегда строгий режим, и `edge` не работает.

## Сборка, тесты, образ, выкатка

```bash
spofy/build.sh                       # -> spofy/dist/xray-linux-amd64 (флаги как в релизном workflow upstream)
spofy/docker/build-image.sh          # -> spofy/remnanode:3.4.4-x<ver>-spofy-<sha>
spofy/test/run-e2e.sh <dir> reality SERVERS="xray-spofy"            # fingerprints edge/ios/qq/chrome/...
spofy/test/run-e2e.sh <dir> hysteria BINS="stock spofy" SCEN="none|delay 25ms loss 1%"
spofy/deploy/node.sh <ssh-host> plan  <tag>    # только чтение
spofy/deploy/node.sh <ssh-host> apply <tag>    # пересоздаёт контейнер: сессии на ноде рвутся на несколько секунд
spofy/deploy/node.sh <ssh-host> rollback       # возврат к точному дайджесту прежнего образа
```

`<dir>` для e2e — каталог с бинарниками `xray-<label>`; для `reality` нужен ещё `xray-client` (старое ядро
с тем же uTLS, что у приложений пользователей). Юнит-тестам нужны `resources/geoip.dat` и `geosite.dat`
(берутся из образа remnanode, в git не попадают).

## Обновление на новый upstream

```bash
git fetch upstream --tags
git merge v<new>                     # конфликты обычно только в go.mod/go.sum и transport/internet/reality
# REALITY: если upstream поднял xtls/reality — обновить third_party/reality по third_party/reality/SPOFY.md
go test ./... ; spofy/test/run-e2e.sh ...   # затем образ, одна нода, сутки наблюдения, остальные
```

Проверить после merge: исправил ли upstream что-то из перенесённого (тогда наш коммит можно откатить),
и не вернулся ли неявный `minClientVer` (ловит `TestSpofyREALITYServerDefaults`).

## Отложено (разбор Jolymmiles, 2026-11-04)

- **RemoveUser закрывает живые соединения** (38f4229e, 1593afe1; upstream PR #5844 открыт). Полезно: после
  истечения/отключения пользователь сейчас продолжает качать по уже открытым соединениям. Но меняет поведение
  (если rw-node обновляет пользователя как remove+add — его соединения будут рваться) и не покрывает Hysteria и
  Shadowsocks. Решение за владельцем.
- Атомарная перезагрузка TLS-сертификатов (62b88856) — гонка при обновлении сертификата/OCSP на TLS-входах
  (Trojan gRPC); низкий практический риск, +91 строка.
- Усиленный QUIC-сниффер (серия 1da3bfe1…), UDP-датаграммы > 8 КБ (PR #55, 4k строк), 2-байтный буфер ответа
  VLESS (aa9b9c9f) — тянут инфраструктуру форка, перенос дорогой.
- «Точный онлайн» — меняет то, что панель показывает как онлайн; не берём.
- Hysteria-исправления форка, PR #70/#75, h2flow, пулы/буферы — чинят регрессии самого форка, в upstream этих
  ошибок нет.
