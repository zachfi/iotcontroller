# Zigbee Network Processor (ZNP) Interface

## 1. Introduction

The Z-Stack Zigbee Network Processor (ZNP) is a cost-effective, low power solution that provides full Zigbee functionality with a minimal development effort.

In this solution, the Z-Stack runs on a SoC, i.e. the CC26x2 and the application runs on an external microcontroller, being any host processor. The Z-Stack ZNP handles all the Zigbee protocol tasks, and leaves the resources of the application microcontroller free to handle the application.

This makes it easy for users to add Zigbee to new or existing products at the same time as it provides great flexibility in choice of microcontroller.

Z-Stack ZNP interfaces to any microcontroller through a range of serial interfaces.

## 2. Physical Interface

### 2.1 Network Processor Signals

The CC26x2 ZNP uses RX/TX/RTS/CTS for UART communication.

#### Default Pin Configuration

| Transport | CC26x2 ZNP signal | CC26x2 PIN | Direction |
|-----------|-------------------|------------|-----------|
| UART      | TX                | DIO_3      | Out       |
| UART      | RX                | DIO_2      | In        |
| UART      | GND               | GND        | In/Out    |

#### Optional Pin Configuration (if NPI_FLOW_CTRL is enabled)

| Transport | CC26x2 ZNP signal | CC26x2 PIN | Direction |
|-----------|-------------------|------------|-----------|
| UART      | CTS               | DIO_19     | In        |
| UART      | RTS               | DIO_18     | Out       |

### 2.2 UART Transport

#### 2.2.1 Configuration

The following UART configuration is supported:

- Baud rate: 115200
- CTS/RTS flow control with NPI_FLOW_CTRL enabled (disabled by default)
- 8-N-1 byte format

#### 2.2.2 Frame Format

UART transport frame format:

```
SOF | General Frame Format | FCS
1   | 3-253                | 1
```

- **SOF (Start of Frame)**: Always set to `0xFE`
- **General Frame Format**: As described in section 2.3
- **FCS (Frame Check Sequence)**: XOR of all bytes in the general format frame fields

#### 2.2.3 FCS Calculation

```c
unsigned char calcFCS(unsigned char *pMsg, unsigned char len)
{
  unsigned char result = 0;
  while (len--)
  {
    result ^= *pMsg++;
  }
  return result;
}
```

#### 2.2.4 Signal Description

Standard UART signals:
- **TX**: Transmit data
- **RX**: Receive data
- **CTS**: Clear to Send (active-low, optional)
- **RTS**: Ready to Send (active-low, optional)

**Note**: CTS/RTS are disabled by default. Enable by defining `NPI_FLOW_CTRL` in predefined symbols.

### 2.3 General Frame Format

```
Length | Command | Data
1      | 2       | 0-250
```

- **Length**: Length of the data field (0-250 bytes)
- **Command**: Command ID (2 bytes, see 2.3.1)
- **Data**: Frame data (0-250 bytes, depends on command)

Multi-byte fields are transmitted in little-endian format (lowest order byte first).

#### 2.3.1 Command Field

The command field is 2 bytes:

```
Cmd0 | Cmd1
7-5  | 7-0
Type | Subsystem (bits 4-0) | Id
```

**Cmd0**:
- Bits 7-5: **Type** (command type)
  - `0x00`: POLL (not used in Z-Stack)
  - `0x20`: SREQ (Synchronous Request - requires immediate response)
  - `0x40`: AREQ (Asynchronous Request - callback/event, no return value)
  - `0x60`: SRSP (Synchronous Response - only sent in response to SREQ)
- Bits 4-0: **Subsystem** (command subsystem)

**Cmd1**: **ID** (8-bit command ID, maps to specific interface message)

**Subsystem Values**:
- `0x00`: RPC Error interface
- `0x01`: SYS interface
- `0x02`: MAC interface
- `0x03`: NWK interface
- `0x04`: AF interface
- `0x05`: ZDO interface
- `0x06`: Simple API interface
- `0x07`: UTIL interface
- `0x08`: DEBUG interface
- `0x09`: APP Interface
- `0x0F`: APP config
- `0x15`: GreenPower

### 2.4 Initialization Procedures

#### 2.4.1 CC26x2 ZNP Power-up Procedure

Recommended power-up procedure:

1. Application processor and CC26x2 power up
2. Application processor initializes its UART interface
3. Application processor receives the `SYS_RESET_IND` message

The CC26x2 ZNP can be reset when the application processor sends a `SYS_RESET_REQ` message.

## 3. ZNP Software Command Interface

The ZNP software command interface is sub-divided into:

- **SYS interface (MT_SYS)**: Low level interface to ZNP hardware and software
- **AF (MT_AF) and ZDO (MT_ZDO) interfaces**: Complete Zigbee interface for full Zigbee compliant applications
  - AF allows registration of application endpoints and send/receive data
  - ZDO provides Zigbee management functions (device/service discovery)
- **UTIL (MT_UTIL) interface**: Support functionalities (setting PAN-ID, getting device info, NV info, callbacks, etc.)
- **APP CONF (MT_APP_CNF) interface**: BDB functionality (Install Codes, Primary/Secondary Channel, commissioning methods, Trust Center configurations)

### 3.1 Configuration Interface

ZNP device has numerous parameters stored in non-volatile memory that persist across resets.

Configuration parameters are divided into:
- **Network-specific**: Should be set to same value for all devices in a Zigbee network
- **Device-specific**: Can be set to different values on each device

#### 3.1.1 Device Specific Configuration Parameters

##### ZCD_NV_STARTUP_OPTION
- **Item ID**: `0x0003`
- **Size**: 1 Byte
- **Default Value**: `0x00`

Bit mask controlling device startup options:
- Bit 7: `ZCD_STARTOPT_CLEAR_NWK_FRAME_COUNTER` - Clear network frame counter (debug only)
- Bit 1: `ZCD_STARTOPT_CLEAR_STATE` - Clear previous network state
- Bit 0: `ZCD_STARTOPT_CLEAR_CONFIG` - Overwrite all config parameters with defaults

**Note**: `ZCD_STARTOPT_CLEAR_CONFIG` is read immediately on power-up. When config is restored to defaults, this bit itself is not restored (except for clearing the bit).

##### ZCD_NV_LOGICAL_TYPE
- **Item ID**: `0x0087`
- **Size**: 1 Byte
- **Default Value**: `0x00`

Logical type of device in Zigbee network:
- `0x00`: `ZG_DEVICETYPE_COORDINATOR`
- `0x01`: `ZG_DEVICETYPE_ROUTER`
- `0x02`: `ZG_DEVICETYPE_ENDDEVICE`

**Note**: This parameter is read immediately on power-up after reset.

##### ZCD_NV_ZDO_DIRECT_CB
- **Item ID**: `0x008F`
- **Size**: 1 Byte
- **Default Value**: `TRUE`

Configures how ZDO responses (callbacks) are issued to host processor:
- `TRUE` (default): Host receives "verbose" response (e.g., `ZDO_IEEE_ADDR_RSP` in response to `ZDO_IEEE_ADDR_REQ`)
- `FALSE`: Host must use `ZDO_MSG_CB_REGISTER` to subscribe to specific ZDO callbacks

#### 3.1.2 Network Specific Configuration Parameters

##### ZCD_NV_PANID
- **Item ID**: `0x0083`
- **Size**: 2 Bytes
- **Default Value**: `0xFFFF`

Zigbee network identifier:
- Value between `0` and `0x3FFF`
- Networks in same vicinity must have different values
- `0xFFFF` = "don't care"

### 3.2 Z-Stack 3.0 ZNP Considerations

#### 3.2.1 Backward Compatibility

ZNP is backward compatible with non Z3.0 devices using the same API from previous releases, or by using Base Device Behavior commissioning MT interface (except new security schemas for Z3.0 such as Distributed networks or Install Codes).

#### 3.2.2 ZNP for Z3.0

ZNP provides compatible baseline for Zigbee 3.0 devices, but full implementation requires additional layers on host-side (outside network processor scope).

Main updates needed on host for Zigbee 3.0:

1. **Base Device Behavior Specification**:
   - Finding and Binding: Host must implement (as Initiator or Target)
   - Touchlink (optional): proximity-based commissioning

2. **Green Power Basic Proxy**:
   - Coordinator and router devices must implement GP Basic proxy
   - ZNP includes GP Stub interfaces for host implementation

#### 3.2.3 ZNP Startup Procedure for Z3.0 Implementation

Recommended startup sequence (mandatory APIs must be called before any Zigbee over-the-air messaging):

1. Host uses `ZB_WRITE_CONFIGURATION` to configure at minimum `ZCD_NV_LOGICAL_TYPE`
2. If logical device is ZC or ZR, GP basic proxy must be initialized in host processor
3. Optional configurations:
   - Set Primary/Secondary channel mask for Formation or Network Steering
   - Set PAN ID via `ZCD_NV_PAN_ID`
   - Set Install codes for networks requiring it
4. Send `AF_REGISTER` to register application endpoint
5. Use BDB commissioning API to create or join network
6. Wait for BDB notifications and ZDO state reports

### 3.3 Return Values

#### General Status Values

| Name | Value | Description |
|------|-------|-------------|
| ZSuccess | 0x00 | Success |
| ZFailure | 0x01 | General failure |
| ZInvalidParameter | 0x02 | Invalid parameter |
| ZDecodeError | 0x03 | Decode error |
| NV_ITEM_UNINIT | 0x09 | NV item uninitialized |
| NV_OPER_FAILED | 0x0a | NV operation failed |
| NV_BAD_ITEM_LEN | 0x0c | NV bad item length |
| ZMemError | 0x10 | Memory error |
| ZBufferFull | 0x11 | Buffer full |
| ZUnsupportedMode | 0x12 | Unsupported mode |
| ZMacMemError | 0x13 | MAC memory error |
| ZSapiInProgress | 0x20 | SAPI in progress |
| ZSapiTimeout | 0x21 | SAPI timeout |
| ZSapiInit | 0x22 | SAPI init |
| ZNotAuthorized | 0x7E | Not authorized |
| ZMalformedCmd | 0x80 | Malformed command |
| ZUnsupClusterCmd | 0x81 | Unsupported cluster command |
| ZOtaAbort | 0x95 | OTA abort |
| ZOtaImageInvalid | 0x96 | OTA image invalid |
| ZOtaWaitForData | 0x97 | OTA wait for data |
| ZOtaNoImageAvailable | 0x98 | OTA no image available |
| ZOtaRequireMoreImage | 0x99 | OTA require more image |
| ZApsFail | 0xb1 | APS fail |
| ZApsTableFull | 0xb2 | APS table full |
| ZApsIllegalRequest | 0xb3 | APS illegal request |
| ZApsInvalidBinding | 0xb4 | APS invalid binding |
| ZApsUnsupportedAttrib | 0xb5 | APS unsupported attribute |
| ZApsNotSupported | 0xb6 | APS not supported |
| ZApsNoAck | 0xb7 | APS no ACK |
| ZApsDuplicateEntry | 0xb8 | APS duplicate entry |
| ZApsNoBoundDevice | 0xb9 | APS no bound device |
| ZApsNotAllowed | 0xba | APS not allowed |
| ZApsNotAuthenticated | 0xbb | APS not authenticated |
| ZSecNoKey | 0xa1 | Security no key |
| ZSecOldFrmCount | 0xa2 | Security old frame count |
| ZSecMaxFrmCount | 0xa3 | Security max frame count |
| ZSecCcmFail | 0xa4 | Security CCM fail |
| ZNwkInvalidParam | 0xc1 | Network invalid parameter |
| ZNwkInvalidRequest | 0xc2 | Network invalid request |
| ZNwkNotPermitted | 0xc3 | Network not permitted |
| ZNwkStartupFailure | 0xc4 | Network startup failure |
| ZNwkTableFull | 0xc7 | Network table full |
| ZNwkUnknownDevice | 0xc8 | Network unknown device |
| ZNwkUnsupportedAttribute | 0xc9 | Network unsupported attribute |
| ZNwkNoNetworks | 0xca | Network no networks |
| ZNwkLeaveUnconfirmed | 0xcb | Network leave unconfirmed |
| ZNwkNoAck | 0xcc | Network no ACK |
| ZNwkNoRoute | 0xcd | Network no route |
| ZMacNoACK | 0xe9 | MAC no ACK |
| ZAfDuplicateEndpoint | 0xd0 | AF duplicate endpoint |
| ZAfEndpointMax | 0xd1 | AF endpoint max |
| ZIcallNoMsg | 0x30 | Icall no message |
| ZIcallTimeout | 0x31 | Icall timeout |

#### Common ZDO Status Values

| Name | Value | Description |
|------|-------|-------------|
| SUCCESS | 0x00 | Operation completed successfully |
| INVALID_REQTYPE | 0x80 | Supplied request type invalid |
| DEVICE_NOT_FOUND | 0x81 | Device not found |
| INVALID_EP | 0x82 | Invalid Endpoint value |
| NOT_ACTIVE | 0x83 | Endpoint not described by simple description |
| NOT_SUPPORTED | 0x84 | Optional feature not supported |
| TIMEOUT | 0x85 | Operation timed out |
| NO_MATCH | 0x86 | No match for End Device bind |
| NO_ENTRY | 0x88 | Unbind request failed, no entry |
| NO_DESCRIPTOR | 0x89 | Child descriptor not available |
| INSUFFICIENT_SPACE | 0x8a | Insufficient space to support operation |
| NOT_PERMITTED | 0x8b | Not in proper state to support operation |
| TABLE_FULL | 0x8c | No table space to support operation |
| NOT_AUTHORIZED | 0x8d | Permissions indicate request not authorized |
| BINDING_TABLE_FULL | 0x8e | No binding table space to support operation |

### 3.4 Additional Considerations for ZNP device in Z-Stack 3.0

- Current version of ZNP device does not support commissioning GP devices in the network if these devices require the basic proxy device to switch channel during commissioning process
- Other commissioning methods require that a Host Processor drives the commissioning process at the application level

### 3.5 Additional Information

For additional details of individual commands, refer to the Z-Stack Monitor and Test API.

## References

- Z-Stack Monitor and Test API

## Acronyms

- **AF**: Zigbee Application Framework
- **API**: Application Programming Interface
- **AREQ**: Asynchronous Request
- **BDB**: Base Device Behavior
- **CTS**: Clear To Send
- **FCS**: Frame Check Sequence
- **GP**: Green Power
- **GPIO**: General Purpose I/O
- **NPI**: Network Processor Interface
- **NV**: Non-Volatile
- **PA/LNA**: Power Amplifier / Low Noise Amplifier (CC259x)
- **RTS**: Ready To Send
- **SoC**: System on Chip
- **SREQ**: Synchronous request
- **SRSP**: Synchronous response
- **UART**: Universal Asynchronous Receiver Transmitter
- **ZDO**: Zigbee Device Object
- **ZNP**: Zigbee Network Processor
