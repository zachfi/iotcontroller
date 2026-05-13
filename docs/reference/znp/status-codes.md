# Z-Stack ZNP Status Codes

Z-Stack ZNP commands return status values to indicate success or failure of operations. Status codes are one byte long.

**Source:** Z-Stack Monitor and Test API

## General Status Values

### Success
- `ZSuccess = 0x00` - Operation completed successfully

### General Errors
- `ZFailure = 0x01` - General failure
- `ZInvalidParameter = 0x02` - Invalid parameter supplied
- `ZDecodeError = 0x03` - Decode error

### Non-Volatile (NV) Memory Errors
- `NV_ITEM_UNINIT = 0x09` - NV item uninitialized
- `NV_OPER_FAILED = 0x0a` - NV operation failed
- `NV_BAD_ITEM_LEN = 0x0c` - NV bad item length

### Memory Errors
- `ZMemError = 0x10` - Memory error
- `ZBufferFull = 0x11` - Buffer full
- `ZMacMemError = 0x13` - MAC memory error

### Mode/State Errors
- `ZUnsupportedMode = 0x12` - Unsupported mode
- `ZSapiInProgress = 0x20` - SAPI operation in progress
- `ZSapiTimeout = 0x21` - SAPI operation timed out
- `ZSapiInit = 0x22` - SAPI initialization state

### Security Errors
- `ZNotAuthorized = 0x7E` - Operation not authorized
- `ZSecNoKey = 0xa1` - Security: no key available
- `ZSecOldFrmCount = 0xa2` - Security: old frame count
- `ZSecMaxFrmCount = 0xa3` - Security: max frame count exceeded
- `ZSecCcmFail = 0xa4` - Security: CCM (Counter with CBC-MAC) failure

### Command Errors
- `ZMalformedCmd = 0x80` - Malformed command
- `ZUnsupClusterCmd = 0x81` - Unsupported cluster command

### OTA (Over-The-Air) Errors
- `ZOtaAbort = 0x95` - OTA operation aborted
- `ZOtaImageInvalid = 0x96` - OTA image invalid
- `ZOtaWaitForData = 0x97` - OTA waiting for data
- `ZOtaNoImageAvailable = 0x98` - OTA no image available
- `ZOtaRequireMoreImage = 0x99` - OTA requires more image data

### APS (Application Support) Errors
- `ZApsFail = 0xb1` - APS operation failed
- `ZApsTableFull = 0xb2` - APS table full
- `ZApsIllegalRequest = 0xb3` - APS illegal request
- `ZApsInvalidBinding = 0xb4` - APS invalid binding
- `ZApsUnsupportedAttrib = 0xb5` - APS unsupported attribute
- `ZApsNotSupported = 0xb6` - APS not supported
- `ZApsNoAck = 0xb7` - APS no acknowledgment received
- `ZApsDuplicateEntry = 0xb8` - APS duplicate entry
- `ZApsNoBoundDevice = 0xb9` - APS no bound device
- `ZApsNotAllowed = 0xba` - APS operation not allowed
- `ZApsNotAuthenticated = 0xbb` - APS not authenticated

### Network (NWK) Errors
- `ZNwkInvalidParam = 0xc1` - Network invalid parameter
- `ZNwkInvalidRequest = 0xc2` - Network invalid request
- `ZNwkNotPermitted = 0xc3` - Network operation not permitted
- `ZNwkStartupFailure = 0xc4` - Network startup failure
- `ZNwkTableFull = 0xc7` - Network table full
- `ZNwkUnknownDevice = 0xc8` - Network unknown device
- `ZNwkUnsupportedAttribute = 0xc9` - Network unsupported attribute
- `ZNwkNoNetworks = 0xca` - Network: no networks available
- `ZNwkLeaveUnconfirmed = 0xcb` - Network leave unconfirmed
- `ZNwkNoAck = 0xcc` - Network no acknowledgment
- `ZNwkNoRoute = 0xcd` - Network no route available

### MAC Errors
- `ZMacNoACK = 0xe9` - MAC no acknowledgment

### AF (Application Framework) Errors
- `ZAfDuplicateEndpoint = 0xd0` - AF duplicate endpoint
- `ZAfEndpointMax = 0xd1` - AF endpoint maximum reached

### Icall Errors
- `ZIcallNoMsg = 0x30` - Icall no message
- `ZIcallTimeout = 0x31` - Icall timeout

## ZDO (Zigbee Device Object) Status Values

ZDO-specific status codes returned in ZDO command responses:

- `SUCCESS = 0x00` - Operation completed successfully
- `INVALID_REQTYPE = 0x80` - Supplied request type invalid
- `DEVICE_NOT_FOUND = 0x81` - Device not found
- `INVALID_EP = 0x82` - Invalid Endpoint value
- `NOT_ACTIVE = 0x83` - Endpoint not described by simple description
- `NOT_SUPPORTED = 0x84` - Optional feature not supported
- `TIMEOUT = 0x85` - Operation timed out
- `NO_MATCH = 0x86` - No match for End Device bind
- `NO_ENTRY = 0x88` - Unbind request failed, no entry
- `NO_DESCRIPTOR = 0x89` - Child descriptor not available
- `INSUFFICIENT_SPACE = 0x8a` - Insufficient space to support operation
- `NOT_PERMITTED = 0x8b` - Not in proper state to support operation
- `TABLE_FULL = 0x8c` - No table space to support operation
- `NOT_AUTHORIZED = 0x8d` - Permissions indicate request not authorized
- `BINDING_TABLE_FULL = 0x8e` - No binding table space to support operation

## Notes

- Status codes are returned in SRSP (Synchronous Response) frames
- For AREQ (Asynchronous Request) frames, status may be embedded in the response data
- ZDO responses include status as the first byte after the source address in AREQ frames
- When status is non-zero, response frames may be shorter (error responses contain minimal data)

## Usage in This Implementation

In our ZNP implementation:
- Status `0x00` indicates success
- Non-zero status indicates an error
- ZDO responses with `Status != 0` may only contain `[srcaddr:2] [status:1] [nwkaddr:2]` (5 bytes total)
- ZDO responses with `Status == 0` contain full descriptor data (20+ bytes)

This is why we manually parse `ZdoNodeDescriptor` responses in `handleFrame()` to handle both error and success cases.
