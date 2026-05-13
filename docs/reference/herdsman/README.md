# zigbee-herdsman as reference

[zigbee-herdsman](https://github.com/Koenkk/zigbee-herdsman) is the library that implements **all low-level Zigbee handling for Zigbee2MQTT** (and ioBroker). Zigbee2MQTT uses it plus [zigbee-herdsman-converters](https://github.com/Koenkk/zigbee-herdsman-converters) for device-specific logic; herdsman is the adapter/controller/spec layer.

We keep a clone in **`old/zigbee-herdsman/`** for local reading. This doc maps herdsman to our codebase and suggests what to read for future work.

## Herdsman layout (high level)

| Area | Path in `old/zigbee-herdsman/src/` | What it does |
|------|------------------------------------|--------------|
| **Adapters** | `adapter/` | Z-Stack (ZNP), EZSP/Ember, deCONZ, Zigate, ZBOSS, ZOH. Serial, discovery, backup. |
| **Z-Stack / ZNP** | `adapter/z-stack/` | ZNP protocol (znp.ts = startup/skipBootloader/timeouts), NV structs, manager, backup. |
| **Ember / EZSP** | `adapter/ezsp/`, `adapter/ember/` | EZSP driver, Ember adapter, ASH/UART. |
| **Controller** | `controller/` | Main controller, database, device/endpoint/group models, interview, OTA, touchlink, green power, install codes, request queue, ZCL frame conversion. |
| **Models** | `models/` | Backup storage (legacy + unified), network options. |
| **ZCL / ZDO** | `zspec/zcl/`, `zspec/zdo/` | Cluster definitions, datatypes, foundation, frame build/parse (buffaloZcl, zclFrame). |
| **Utils** | `utils/` | waitress (async request/response matching), async mutex, backup helpers. |

## What we already used from herdsman

- **ZNP startup**: `adapter/z-stack/znp/znp.ts` — skipBootloader (ping first, then magic byte), timeouts (SREQ 6s, reset 30s, startupFromApp 40s, post-magic 1s). See `docs/reference/znp/timeouts-herdsman.md` and `pkg/zigbee-dongle/znp/controller.go`.
- **Formation / backup**: Z-Stack adapter manager and backup/structs for NV/network formation ideas; our formation and `network.yaml` are inspired by that.

## What we can learn next (by topic)

1. **Device interview**  
   - `controller/controller.ts` (start, permit join, device join flow).  
   - `controller/model/device.ts`, `endpoint.ts` (model, endpoints, descriptors).  
   - Z-Stack: `adapter/z-stack/adapter/endpoints.ts`, manager.

2. **ZCL frames and clusters**  
   - `zspec/zcl/` — cluster IDs, attributes, datatypes, foundation.  
   - `controller/helpers/zclFrameConverter.ts`, `zclTransactionSequenceNumber.ts`.  
   - Useful if we add generic ZCL read/write or cluster-specific handling.

3. **Request/response pairing**  
   - `utils/waitress.ts` — match async responses to requests (like our one-off handlers).  
   - Adapter usage: how they send a command and wait for a specific response/callback.

4. **Backup / network state**  
   - `models/backup-storage-unified.ts`, `backup.ts`.  
   - Z-Stack: `adapter/z-stack/adapter/adapter-backup.ts`, `adapter-nv-memory.ts`, structs in `structs/`.  
   - For cross-dongle compatibility (we already have `network.yaml`; herdsman backup format is a reference).

5. **OTA (Over-The-Air update)**  
   - `controller/helpers/ota.ts`.  
   - When we need firmware OTA for devices.

6. **Touchlink / Green Power**  
   - `controller/touchlink.ts`, `controller/greenPower.ts`.  
   - If we add commissioning or green power device support.

7. **Install codes**  
   - `controller/helpers/installCodes.ts`.  
   - For secure join.

8. **Adapter abstraction**  
   - `adapter/adapter.ts` (base), how Z-Stack vs EZSP implement the same operations.  
   - Helps keep our ZNP and Ember implementations aligned (same formation/state semantics).

## Our code vs herdsman

- **We implement**: coordinator-only (formation, load/save network state, ZNP + Ember dongles). We do not run Zigbee2MQTT’s stack ourselves; our `pkg/iot/routers/zigbee2mqtt` talks to an **existing** Zigbee2MQTT instance via MQTT.
- **Herdsman**: full stack (adapters + controller + device/endpoint model + ZCL + backup + OTA + …). Reading it tells us how a production stack does formation, interview, ZCL, and backup so we can mirror patterns or reuse ideas in our coordinator and any future device-side logic.

## Quick links (under `old/zigbee-herdsman/`)

- ZNP startup/timeouts: `src/adapter/z-stack/znp/znp.ts`
- Z-Stack adapter (manager, backup): `src/adapter/z-stack/adapter/`
- Controller API / device flow: `src/controller/controller.ts`
- Device/endpoint model: `src/controller/model/device.ts`, `endpoint.ts`
- ZCL definitions: `src/zspec/zcl/`
- Project overview / layout: `AGENTS.md`, `README.md`
