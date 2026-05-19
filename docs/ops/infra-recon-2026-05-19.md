# Infrastructure recon (read-only SSH)

**Дата:** 2026-05-19  
**Режим:** только осмотр, без изменений на хостах  
**SSH:** алиасы из `~/.ssh/config` (совпадают с ожидаемыми)  
**Контекст от оператора:** `bx_msk_d` снят; после падений **основной канал — WireGuard**, не tun-rnd; на всех узлах живые клиенты — действовать осторожно.

---

## Сводная матрица

| Alias | hostname (на сервере) | IP (из ssh config) | Production WG | Shadow (VRN stack) | tun-rnd runtime | runtime-helper | Monitoring docker |
|-------|----------------------|---------------------|---------------|-------------------|-----------------|----------------|-------------------|
| **edg** | `bridge-msk` | 85.239.44.100 | **да** (control-api, portal, uplinks) | туннели read/write | нет active units | **нет** | нет |
| **vrn** | `bridge-vrn` | 91.221.109.60 | нет | **да** (api, portal, postgres) | **да** (server@ ams/fra/nyc) | **нет** | нет |
| **ams** | `bridge-ams` | 147.45.238.121 | wg0 | нет | **client@ spb, vrn** | **нет** | нет |
| **fra** | `bridge-fra` | 103.110.65.30 | 6× wg ifaces | нет | **client@ spb, vrn** | **нет** | нет |
| **nyc** | TS KVM host | 108.165.154.213 | wg-us, wg-mac, … | нет | **client@ spb,vrn + server@laptop** | **нет** | нет |
| **msk** | `server-msk` | 85.239.44.49 | wg0 | нет | watchdog **failed** (ams/fra/nyc) | **нет** | **да** (18070/18071) |
| **spb** | `spb` | 188.225.57.173 | **18110** api | нет | **server@ ams,fra,nyc** | **нет** | нет |
| **exe** | `bitrix01…` | 91.221.109.192 | нет | нет | нет | **нет** | gridai only |

**Нигде не найдено:** `runtime-helper`, `/run/tun/runtime-helper.sock`, порт `19090`, unit `tun-runtime-helper.service`.

---

## По ролям (что реально работает)

### EDG — production шлюз (WireGuard baseline)

- **Активно:** `wg-control-api`, `wg-portal-http`, `wg-fra-uplink`, `wg-us-canary`
- **Shadow-связь с VRN:** `jstun-shadow-read-tunnel`, `jstun-shadow-write-db-tunnel` (active)
- **WG:** `wg0`, `wg-edg-fra`, `wg-us`
- **Control API:** `127.0.0.1:18110` (localhost only — снаружи не слушает)
- **tun-rnd:** unit-файлы есть, **running mesh на edg нет**
- **Вывод:** EDG соответствует сценарию «WG production + shadow sync», не tun-rnd datapath.

### VRN — shadow control-plane + остатки tun-rnd mesh

- **Активно:** `jstun-shadow-control-api`, `jstun-shadow-portal-http`, `jstun-shadow-postgres`
- **tun-rnd (осторожно!):** `tun-runtime-server@ams|fra|nyc` — **всё ещё running**
- **Проблема:** `tun-runtime-server-watchdog@nyc` — **failed**
- **WG:** `wg0`, `wg-ams`, `wg-fra`, `wg-nyc` (инфраструктурные интерфейсы)
- **Вывод:** VRN — не «чистый shadow»; R&D mesh server ещё крутится параллельно с WG-переходом.

### AMS / FRA / NYC — uplinks

- **AMS/FRA:** `tun-runtime-client@spb` и `@vrn` — **active** (mesh clients к SPB/VRN)
- **NYC:** те же clients + `tun-runtime-server@laptop` (отдельный контур)
- **WG на AMS:** только `wg0`; на FRA — множество `wg-*-fra` (транзит)
- **Вывод:** uplink’и всё ещё держат **tun-rnd mesh**, не только WG.

### MSK (`server-msk`) — хостинг / monitoring

- **Не** старый `bx_msk_d` (другой IP: 85.239.44.49 vs 158.160.254.197 в config для bx_msk_d)
- **Docker:** `tun-monitoring-api`, `tun-monitoring-ingestor`, `tun-monitoring-postgres` — Up ~22h
- **Порты:** `18070`, `18071` (публично на 0.0.0.0)
- **/etc/tun:** `runtime-server-*.env`, static keys (legacy mesh server config)
- **Watchdog:** `tun-runtime-server-watchdog@{ams,fra,nyc}` — **failed** (серверы mesh на MSK не running в этом скане)
- **Вывод:** MSK сейчас — узел **monitoring**, с **хвостами** tun-rnd mesh; не трогать без плана.

### SPB — дополнительный узел (не в исходном списке, но в mesh)

- `wg-control-api` на `0.0.0.0:18110`
- `tun-runtime-server@ams|fra|nyc` — **running**
- **Вывод:** SPB — активный hub tun-rnd; критичен для clients на AMS/FRA.

### EXE — вне VPN-контура TUN

- Bitrix-хост, `wg` нет, только `gridai-monitor` + 443
- **Не кандидат** для pilot/helper.

---

## Соответствие заявлению «перешли на WG»

| Утверждение | Факт на диске/ systemd |
|-------------|------------------------|
| WG — основной для клиентов на EDG | **Подтверждается** (portal + control-api + uplinks) |
| tun-rnd выключен везде | **Не подтверждается** — mesh **active** на VRN, AMS, FRA, NYC, SPB |
| bx_msk_d нет | **Подтверждается** — alias в ssh config остался, IP 158.160.254.197 не сканировался; **msk** = `server-msk` @ 85.239.44.49 |
| runtime-helper для JsTun | **Нигде не развёрнут** |

---

## Риски при любых работах

1. **Не останавливать** на EDG: `wg-control-api`, `wg-portal-http`, uplink units — клиенты на линии.
2. **tun-runtime-* на VRN/AMS/FRA/NYC/SPB** — деcommission только по `scripts/decommission_tunrnd_contour.sh` + окно; иначе разорвёт mesh, который ещё running.
3. **Shadow tunnels на EDG** — нужны для read/write mirror; не рвать без cutover-плана.
4. **MSK monitoring** — единственный живой monitoring stack; рестарты docker — осознанно.
5. **Pilot runtime-helper** — **не на EDG/VRN** сначала; кандидаты для изолированного опыта:
   - отдельная VM / **EXE** не подходит;
   - логичнее **новая** staging-VM или **локальный** macOS с JsTun (не prod gateway);
   - **MSK** — только если helper слушает localhost и **не** трогает WG/mesh (согласовать).

---

## SSH заметки

- `edg` → hostname `bridge-msk` (имя может путать с alias `msk` — **разные IP**: edg .100, msk .49)
- `edg_msk` (10.200.0.4 via ProxyJump msk): **No route to host** с текущей сети — внутренний EDG недоступен напрямую
- `bx_msk_d` в config — **устарел**, не использовать

---

## Рекомендуемые следующие шаги (безопасные)

1. **Согласовать с оператором:** tun-rnd mesh — целевое состояние «off» или «оставить до decommission»?
2. Если mesh снимаем — волна: clients (ams/fra/nyc) → servers (vrn/spb) → очистка `/etc/tun` на msk
3. **runtime-helper pilot** — только на **новой** staging-ноде или dev laptop; inventory уже без helper
4. Зафиксировать в monitoring ingestor: discovery URLs → EDG `127.0.0.1:18110` через SSH tunnel или public API policy

---

## Команды (повтор recon)

```bash
# один хост
ssh edg 'systemctl is-active wg-control-api wg-portal-http; ss -tln | grep -E "18110|19090"'
```

*Секреты из `/etc/tun`, `wg show` peers, содержимое `.env` не собирались.*
