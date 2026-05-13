# ZNP approach vs old/ and zigbee-herdsman

This compares our `pkg/zigbee-dongle/znp` and coordinator flow to `old/znp` and `old/zigbee-herdsman`.

## zigbee-herdsman skipBootloader (znp.ts)

Herdsman does **not** send the magic byte first. It:

1. Tries **SYS ping** (command ID 1) with **250ms** timeout.
2. If ping fails: send magic byte 0xef, wait **1s**, try SYS ping again (250ms).
3. If still failing, CC2652 path: DTR/RTS toggling (no magic byte).
4. After skipBootloader, adapter does version (SYS "version").

We follow this: **try ping first**; only if ping fails do we send the magic byte and retry ping. Then we always do SysVersionRequest.

## old/znp startup sequence (controller.go)

1. **No hardware reset** — old/znp does not toggle DTR or do any reset before the magic byte.
2. **Magic byte** — `WriteMagicByteForBootloader()` (0xef) unconditionally.
3. **SysVersionRequest** — timeout **10s**.
4. **UtilGetDeviceInfoRequest** — via `WriteCommand` (default **1s** in old/znp port).
5. If device state != Coordinator: **ZdoStartupFromAppRequest** via `WriteCommand` (1s), then wait for **ZdoStateChangeInd** (one-off handler **10s**).
6. Register AF endpoints.

So old/znp uses short timeouts (1s for most commands, 10s for version and state change) and **no DTR reset**.

## Our approach

- **Optional hardware reset** — we do DTR reset before magic byte by default. Use `-zigbeecoordinator.skip-hardware-reset` to match old/znp (no reset).
- **Magic byte** then **1s** delay (matches herdsman).
- **SysVersionRequest** **30s** (longer than old/znp 10s; herdsman default 10s).
- **UtilGetDeviceInfoRequest** **15s**.
- **ZdoStartupFromAppRequest** **40s** and **ZdoStateChangeInd** **40s** (matches zigbee-herdsman).
- **Config load/save** — we load `network.yaml` and optionally form or adopt; old/znp has no config file.
- **FormNetwork** — we implement NV write (PAN ID, extended PAN ID, channel, key) + reset + re-startup; old/znp does not implement formation.

## Validation

- **Start sequence**: Same order as old/znp (magic byte → version → device info → startup from app if needed → endpoints). We added reset and longer timeouts.
- **Config vs device**: We compare loaded state to device (PAN, extended PAN, channel) and keep config as source of truth when they match; old/znp has no such logic.
- **Formation**: Our FormNetwork follows Z-Stack NV items and herdsman-style startupFromApp timing; old/znp has no formation.

If the device was working with old/znp and is unresponsive with our reset, try **replug (power-cycle)** then run with **`-zigbeecoordinator.skip-hardware-reset`** to match old/znp (no DTR reset).
