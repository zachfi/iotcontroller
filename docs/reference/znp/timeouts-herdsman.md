# ZNP timeouts (zigbee-herdsman reference)

We align formation/startup timeouts with zigbee-herdsman so behavior is consistent with a widely used stack.

**Source:** `zigbee-herdsman` — [src/adapter/z-stack/znp/znp.ts](https://github.com/Koenkk/zigbee-herdsman/blob/master/src/adapter/z-stack/znp/znp.ts)

## Timeout constants (from znp.ts)

```ts
const timeouts = {
  SREQ: 6000,      // 6s - normal SREQ
  reset: 30000,    // 30s - reset
  default: 10000,  // 10s - default
};
```

## startupFromApp

For `startupFromApp` (and `bdbStartCommissioning`) herdsman uses **40 seconds**, not the 6s SREQ default:

```ts
const t = object.command.name === "bdbStartCommissioning" || object.command.name === "startupFromApp" ? 40000 : timeouts.SREQ;
const waiter = this.waitress.waitFor({...}, timeout || t);
```

So: **40s for startupFromApp**.

## Bootloader skip (magic byte)

After writing the magic byte (0xef), herdsman waits **1 second** then sends ping:

```ts
this.unpiWriter.writeBuffer(Buffer.from([0xef]));
await wait(1000);
await this.request(Subsystem.SYS, "ping", {capabilities: 1}, undefined, 250);
```

So: **1s delay after magic byte** before first command.

## Our usage

- `ZdoStartupFromAppRequest` and one-off handler for `ZdoStateChangeInd`: **40s**
- `SysVersionRequest` after reset: **30s** (herdsman default is 10s; we allow more for slow boot)
- Post–magic byte delay: **1s**
- Post–hardware reset delay: **2s** (herdsman does not do DTR reset; we do when not using skip-hardware-reset)
