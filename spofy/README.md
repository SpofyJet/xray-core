# Spofy Xray-core — тонкий форк официального Xray

Ветка `spofy/main` = официальный тег XTLS/Xray-core + небольшой набор патчей. Каждый патч — отдельный коммит
с объяснением и тестами; всё, что взято из Jolymmiles/Xray-core, помечено `Port of Jolymmiles/Xray-core <sha>`.

База сейчас: **v26.11.9**. Обновления upstream вливаются настоящим `git merge` (не переносом руками), как в
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
spofy/docker/build-image.sh          # -> spofy/remnanode:3.4.5-x<ver>-spofy-<sha>
spofy/test/run-e2e.sh <dir> reality SERVERS="xray-spofy"            # fingerprints edge/ios/qq/chrome/...
spofy/test/run-e2e.sh <dir> hysteria BINS="stock spofy" SCEN="none|delay 25ms loss 1%"
spofy/deploy/node.sh <ssh-host> plan           # только чтение
spofy/deploy/node.sh <ssh-host> preflight      # только чтение: живой конфиг ноды через новое ядро
spofy/deploy/node.sh <ssh-host> apply          # preflight, затем пересоздаёт контейнер: сессии рвутся на секунды
                                               # (тег по умолчанию — самый свежий локальный spofy/remnanode)
spofy/deploy/node.sh <ssh-host> rollback       # возврат к точному дайджесту прежнего образа
# <ssh-host> — имя из ~/.ssh/config или просто IP (тогда root@IP, ключ ~/.ssh/node_audit).
# Новая нода: на ней добавить ключ бот-сервера в /root/.ssh/authorized_keys, потом: spofy/deploy/node.sh <IP> apply
# Прямо на ноде, без бот-сервера (качает ядро из GitHub Release, проверяет sha256 и живой конфиг):
#   sudo bash -c "$(curl -fsSL https://raw.githubusercontent.com/SpofyJet/xray-core/refs/heads/spofy/main/spofy/install.sh)"
#   … _ plan | check | rollback
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

- **RemoveUser закрывает живые соединения** (38f4229e, 1593afe1; upstream PR #5844) — **не берём** (решено
  2026-11-04). rw-node 3.4.4 в `addUser` сначала вызывает `removeUser` на всех inbound и только потом добавляет
  (проверено в `/opt/app/dist/main.js`), т.е. любая правка пользователя в панели (продление, сброс трафика,
  смена сквада) рвала бы ему все соединения. К тому же патч не покрывает Hysteria и Shadowsocks. Вернуться,
  если upstream примет #5844 и rw-node перейдёт на точечное обновление.
- Атомарная перезагрузка TLS-сертификатов (62b88856) — гонка при обновлении сертификата/OCSP на TLS-входах
  (Trojan gRPC); низкий практический риск, +91 строка.
- Усиленный QUIC-сниффер (серия 1da3bfe1…), UDP-датаграммы > 8 КБ (PR #55, 4k строк), 2-байтный буфер ответа
  VLESS (aa9b9c9f) — тянут инфраструктуру форка, перенос дорогой.
- «Точный онлайн» — меняет то, что панель показывает как онлайн; не берём.
- **XHTTP flow governor (h2flow)** — не исправление, а новая функция сервера: своё управление окнами HTTP/2 для
  XHTTP поверх h2/TCP. По замерам автора (BASELINE.md) пинг рядом со скачиванием 1021 → 66 мс, загрузка на
  RTT 300 мс 3.2 → 9.5 МБ/с. Не берём пока: ~2900 строк кода, выключено по умолчанию даже у автора, 51 правка
  за последнюю неделю (5 раундов ревью, свежие баги). Вернуться, когда устоится; пробовать на одной XHTTP-ноде.
- XHTTP XMUX-резервирование (100adeae, 5938cad9) — код клиента (dialer), на нодах не работает.
- TCP half-close end to end (5016b0d4, 21 файл) и «perf hot paths» (5e7bb42e, переписаны dispatcher/router/buf,
  тысячи строк) — задевают все протоколы; польза не измерена нами, риск большой.
- Полезное для XHTTP из upstream (`h2Params` #7295, `OPTIONS` как uplinkHTTPMethod #7290) уже есть в v26.10.31.
- Hysteria-исправления форка, PR #70/#75, пулы/буферы — чинят регрессии самого форка, в upstream этих
  ошибок нет.
