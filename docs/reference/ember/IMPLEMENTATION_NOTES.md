# EZSP Implementation Notes from Documentation Review

## Key Documents
- **UG100**: EZSP Reference Guide (EmberZNet PRO Release 7.4.2)
- **UG101**: UART Gateway Protocol Reference Guide
- **AN706**: EZSP-UART Host Interfacing Guide

## Software flow control (XON/XOFF)
When RTS/CTS is off, **zigbee-herdsman** enables **software flow control** (XON/XOFF) on the serial port so the NCP can throttle the host. Our current serial stack does not set XON/XOFF; we only disable RTS/CTS. If formation or stability issues persist, consider enabling XON/XOFF when flow control is disabled (may require a serial driver or ASH-layer support that strips/handles XON/XOFF). See herdsman `uart/ash.ts`: `serialOpts.xon = true; serialOpts.xoff = true` when `!serialOpts.rtscts`.

## Recovery from bad state
If the dongle returns 0x58 for formation commands (and Zigbee2MQTT can form on the same hardware), the device can leave a bad state. **Power cycle** the dongle and try again. If 0x58 persists, try: different firmware, factory reset if documented for the model, or form with Zigbee2MQTT once and then use our stack with the same `network.yaml` (restore-from-tokens path).

## Sonoff Dongle Max (ESP32 bridge)
The Dongle Max exposes **ESP32** on USB; the **EFR32MG24** (Ember NCP) is behind it. The ESP32 can send boot logs (text) on the same serial line before the EZSP bridge is active. Our `Start()` in `pkg/zigbee-dongle/ember/controller.go` reads initial data, detects text (ESP32 boot), waits and drains, then looks for ASH frames. Some documentation suggests a **button press** is required to activate the Zigbee/EZSP interface after ESP32 boot. If the device returns version timeout or 0x58, try: replug, wait 10–15s, press the button, then start with `-zigbeecoordinator.disable-flow-control`. See also `docs/ZIGBEE_NETWORK_VALIDATION.md` (Troubleshooting).

## Comparison with zigbee-herdsman
Our Ember formation flow is compared to **zigbee-herdsman** (Zigbee2MQTT’s stack) in **formation-vs-herdsman.md**. Summary: herdsman requires `SLStatus.OK` for networkInit (or NOT_JOINED), setInitialSecurityState, setExtendedSecurityBitmask, and FORM_NETWORK; we fail fast when FORM_NETWORK returns 0x58 to align with that.

## Critical Commands for Network Formation

### 1. `networkInit` (ID: 0x0017)
**Description**: Resume network operation after a reboot. The node retains its original type. This should be called on startup whether or not the node was previously part of a network.

**Response**: 
- `EMBER_NOT_JOINED` if the node is not part of a network
- `EmberStatus` indicating successful initialization or reason for failure

**Options** (EmberNetworkInitBitmask):
- `EMBER_NETWORK_INIT_NO_OPTIONS` (0x0000): No options
- `EMBER_NETWORK_INIT_PARENT_INFO_IN_TOKEN` (0x0001): Save parent info in token
- `EMBER_NETWORK_INIT_END_DEVICE_REJOIN_ON_REBOOT` (0x0002): Send rejoin request on reboot

### 2. `networkState` (ID: 0x0018)
**Description**: Returns a value indicating whether the node is joining, joined to, or leaving a network.

**Response**: `EmberNetworkStatus` value indicating current join status.

### 3. `setInitialSecurityState` (ID: 0x0068)
**Description**: Sets the security state that will be used by the device when it forms or joins the network.

**Critical Note**: "This call should not be used when restoring saved network state via networkInit as this will result in a loss of security data and will cause communication problems when the device re-enters the network."

**When to call**: Before forming or joining a network (when NOT using `networkInit` to restore state).

**Prerequisites**: Documentation doesn't explicitly state network must be in `NO_NETWORK` state, but it must be called before `formNetwork` or `joinNetwork`.

### 4. `formNetwork` (ID: 0x001E)
**Description**: Forms a new network by becoming the coordinator.

**Response**: `EmberStatus` value indicating success or reason for failure.

**Note**: Network formation is asynchronous. Wait for `stackStatusHandler` callback with `EMBER_NETWORK_UP` status before considering the network formed.

### 5. `stackStatusHandler` (ID: 0x0019) - Callback
**Description**: A callback invoked when the status of the stack changes. If the status parameter equals `EMBER_NETWORK_UP`, then the `getNetworkParameters` command can be called to obtain the new network parameters.

**When received**: After `formNetwork` or `joinNetwork` completes (asynchronously).

## Status Code 0x58 Issue

### What the Documentation Says
In UG100, status code `0x58` is listed as:
- `EMBER_ERR_BOOTLOADER_TRAP_TABLE_BAD` (0x58): "The bootloader received an invalid message (failed attempt to go into bootloader)."

However, this is a bootloader error, not an EZSP status code.

### What We're Observing
The device consistently returns `0x58` for:
- All `SET_CONFIGURATION_VALUE` commands
- All `SET_POLICY` commands  
- `NETWORK_INIT` command
- `NETWORK_STATE` queries
- `SET_INITIAL_SECURITY_STATE` command
- `SET_EXTENDED_SECURITY_BITMASK` command
- `FORM_NETWORK` command

**Response format:** Raw EZSP response for these commands is exactly one byte: `0x58`. So the NCP is sending a single-byte status (not a 4-byte status as in EZSP v14+); we are reading it correctly.

### Possible Interpretations
1. **SLStatus.TRANSMIT_BLOCKED** (0x0058): From zigbee-herdsman's enum, this means "The transmit attempt was blocked from going over the air. Typically this is due to the Radio Hold Off (RHO) or Coexistence plugins." However, configuration commands don't involve radio transmission.

2. **Device in Invalid State**: The device may be in an uninitialized or invalid state where it cannot process commands properly.

3. **Firmware Issue**: The device firmware may be in a state where it cannot respond correctly to commands.

## Recommended Sequence (from Documentation)

### For New Network Formation:
1. Call `networkInit` with `EMBER_NETWORK_INIT_NO_OPTIONS` (0x0000)
   - If returns `EMBER_NOT_JOINED`, proceed with formation
   - If returns success, check `networkState` to verify
2. Call `setInitialSecurityState` (if not using `networkInit` to restore state)
3. Call `formNetwork` with network parameters
4. Wait for `stackStatusHandler` callback with `EMBER_NETWORK_UP`
5. Call `getNetworkParameters` to verify network parameters

### For Restoring Network State:
1. Call `networkInit` with appropriate options (e.g., `EMBER_NETWORK_INIT_PARENT_INFO_IN_TOKEN`)
2. **DO NOT** call `setInitialSecurityState` (per documentation warning)
3. Wait for `stackStatusHandler` callback
4. Verify network state with `networkState`

## Current Implementation vs Documentation

### What We're Doing:
1. ✅ Calling `networkInit` with bitmask `0x0003` (matching zigbee-herdsman)
2. ✅ Checking network state after `networkInit`
3. ⚠️ Attempting `setInitialSecurityState` only when state is `NO_NETWORK` (0x00)
4. ⚠️ Calling `formNetwork` even when device returns `0x58` for all commands

### What the Documentation Doesn't Clarify:
- What to do when `networkInit` returns `0x58` instead of `EMBER_NOT_JOINED`
- Whether `setInitialSecurityState` can be called when network state query returns `0x58`
- What `0x58` means in the context of EZSP commands (not bootloader)

### When FORM_NETWORK Returns 0x58 (aligned with zigbee-herdsman)
**zigbee-herdsman** requires `SLStatus.OK` (0x00) for FORM_NETWORK and throws on any other status (including 0x58). We now do the same: if FORM_NETWORK returns 0x58 we **fail immediately** with a clear error ("form network rejected: device returned 0x58 ... try power cycle or different firmware/dongle") and do not wait for NETWORK_UP. This avoids a 45s timeout when the device has effectively rejected the command. See **docs/reference/ember/formation-vs-herdsman.md** for a full comparison of our formation flow with herdsman.

## Next Steps

1. **Review AN706** for initialization sequence details
2. **Check if device needs different initialization** (hardware reset, different command sequence)
3. **Verify firmware version** - may need firmware update
4. **Consider alternative approach**: Try calling `leaveNetwork` first to force `NO_NETWORK` state
5. **Review zigbee-herdsman's error handling** for `0x58` responses
