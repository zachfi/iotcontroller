# Ember formation vs zigbee-herdsman

Comparison of our Ember network formation flow with **zigbee-herdsman** (TypeScript, `old/zigbee-herdsman/src/adapter/ember/`). Herdsman is the reference used by Zigbee2MQTT.

## Herdsman formation flow (emberAdapter.ts)

1. **Start / init**  
   `initEzsp()`: version, config values, policies. Then trust-center init (policies, join policy).

2. **networkInit**  
   Called with `PARENT_INFO_IN_TOKEN | END_DEVICE_REJOIN_ON_REBOOT`.  
   - **Accepted responses**: `SLStatus.OK` or `SLStatus.NOT_JOINED` only.  
   - Any other status (including 0x58) → **throw**, formation never starts.

3. **If initStatus === OK**  
   Wait for `STACK_STATUS_NETWORK_UP` (oneWaitress, **10s** timeout). Get network params; if config doesn’t match → leave network, wait for `STACK_STATUS_NETWORK_DOWN`, then form from config or backup.

4. **If NOT_JOINED or just left**  
   Decide FORM_BACKUP vs FORM_CONFIG, then `formNetwork()`:

   - `ezspSetInitialSecurityState(state)` → **must return SLStatus.OK**, else throw.
   - `ezspSetExtendedSecurityBitmask(extended)` → **must return OK**, else throw.
   - If !fromBackup: `ezspClearKeyTable()` → log error if not OK but don’t throw.
   - `ezspFormNetwork(netParams)` → **must return SLStatus.OK**, else throw.
   - Wait for `STACK_STATUS_NETWORK_UP` with **DEFAULT_NETWORK_REQUEST_TIMEOUT = 10s**.

5. **Stack status**  
   Delivered via EZSP callback `STACK_STATUS_HANDLER`; oneWaitress resolves the “wait for NETWORK_UP” promise. No polling of NETWORK_STATE in the formation path (they rely on the callback).

## Our implementation vs herdsman

| Aspect | Herdsman | Us (after alignment) |
|--------|----------|----------------------|
| networkInit accepted | OK, NOT_JOINED only | We still accept 0x58 and continue (device may be in invalid state; we try formation anyway). |
| setInitialSecurityState / setExtendedSecurityBitmask | Must be OK; throw otherwise | We use the **same initial security bitmask** as herdsman (incl. TRUST_CENTER_USES_HASHED_LINK_KEY, REQUIRE_ENCRYPTED_KEY). We log 0x58 as warning and continue (device may already be configured). |
| **FORM_NETWORK response** | **Must be SLStatus.OK; throw otherwise** | **We now treat 0x58 as failure** and return an error immediately (no 45s wait). Aligned with herdsman. |
| Wait for NETWORK_UP | 10s (oneWaitress), callback-only | We wait up to 45s, using STACK_STATUS callback + NETWORK_STATE polling every 2s. |
| 0x58 on FORM_NETWORK | Would have thrown earlier (at init or setInitialSecurityState); if they ever got 0x58 from formNetwork they would throw | We fail fast with a clear message: "form network rejected: device returned 0x58 ..." |

## Takeaways

- **Herdsman does not accept 0x58** for any of: networkInit (only OK/NOT_JOINED), setInitialSecurityState, setExtendedSecurityBitmask, formNetwork. They fail fast.
- **We now fail fast when FORM_NETWORK returns 0x58** so we don’t wait 45s for NETWORK_UP when the device has effectively rejected the command. Error message directs the user to try power cycle or different firmware/dongle.
- We keep tolerating 0x58 for NETWORK_INIT and security/config commands so that we still attempt formation on devices that return 0x58 there; if they then return 0x58 for FORM_NETWORK we stop immediately.
- Herdsman uses a **10s** timeout for “network up” after form; we use **45s** and polling as fallback for environments where the callback might be delayed or missing.

## What 0x58 on this device says about its state

**Verified (raw EZSP response):** We log the full response payload when formation commands fail. On a dongle that returns 0x58, the response is exactly **len=1, 0x58** for NETWORK_INIT, SET_INITIAL_SECURITY_STATE, SET_EXTENDED_SECURITY_BITMASK, and FORM_NETWORK. So we are not misreading (e.g. not reading byte 0 of a 4-byte status); the NCP really returns a single-byte status 0x58.

When the **same Ember dongle** returns 0x58 for LEAVE_NETWORK (Step -1), NETWORK_INIT, SET_INITIAL_SECURITY_STATE, SET_EXTENDED_SECURITY_BITMASK, and FORM_NETWORK (even after replug):

- **Herdsman would fail earlier**: It only accepts OK or NOT_JOINED from networkInit, so it would throw at the first command and never attempt formation. So this is not “our sequence is wrong” — the device is in a state (or has firmware) that does not respond with OK to the standard formation path.
- **We already force leave then init**: We call LEAVE_NETWORK before NETWORK_INIT to try to get a clean state. If the device returns 0x58 for leave as well, it is not transitioning to a “no network” state that accepts the rest of the sequence.
- **Possible causes**: NCP/tokens in a bad or locked state; firmware that returns 0x58 in these contexts; or a model that needs a different init/reset (e.g. vendor-specific or factory reset) before it will accept formation. Replug (power cycle) does not clear it on this unit.

## Single configuration for multiple dongles (goal)

The **formation logic for “one network.yaml, multiple devices” is correct**:

- We load the same PAN ID, extended PAN ID, channel, and network key from disk and pass them into the same EZSP formation sequence (leave → networkInit → setInitialSecurityState → setExtendedSecurityBitmask → formNetwork). That is the same design as herdsman and supports using one config when swapping dongles.
- **ZNP**: Formation and save/reload work; you can form on ZNP, save `network.yaml`, and reload or swap.
- **Ember**: Formation only succeeds when the dongle returns SLStatus.OK for the formation commands. On a dongle that consistently returns 0x58, we cannot form with the standard sequence; the blocker is the device/firmware, not the design of our formation or config.

**Practical options**: (1) Use ZNP to form and maintain the single config; (2) Try a different Ember dongle or firmware that returns OK; (3) If a factory reset or vendor-specific clear command exists for this model, try that and then form again.

## References

- `old/zigbee-herdsman/src/adapter/ember/adapter/emberAdapter.ts`: init path (~855–930), formNetwork (~1048–1138), DEFAULT_NETWORK_REQUEST_TIMEOUT = 10000.
- `old/zigbee-herdsman/src/adapter/ember/ezsp/ezsp.ts`: ezspFormNetwork (2928), ezspNetworkInit (2750), STACK_STATUS_HANDLER (663).
- Our formation: `pkg/zigbee-dongle/ember/controller.go` FormNetwork().
