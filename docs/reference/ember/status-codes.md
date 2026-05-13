# Ember Status Codes

Many EmberZNet API functions return an `EmberStatus` value to indicate the success or failure of the call. Return codes are one byte long.

**Source:** error-def.h (do not include directly - included by error.h inside an enumeration typedef, which is in turn included by ember.h)

## Common Status Codes

### Success
- `EMBER_SUCCESS = 0x00` - The generic "no error" message.

### Network States
- `EMBER_NETWORK_UP = 0x90` - The stack software has completed initialization and is ready to send and receive packets over the air.
- `EMBER_NETWORK_DOWN = 0x91` - The network is not operating.
- `EMBER_NOT_JOINED = 0x93` - The node has not joined a network.
- `EMBER_JOIN_FAILED = 0x94` - An attempt to join a network failed.
- `EMBER_NETWORK_OPENED = 0x9C` - The network has been opened for joining.
- `EMBER_NETWORK_CLOSED = 0x9D` - The network has been closed for joining.

### Transmission Errors
- `EMBER_MAC_TRANSMIT_QUEUE_FULL = 0x39` - The MAC transmit queue is full.
- `EMBER_MAC_NO_ACK_RECEIVED = 0x40` - An ACK was expected following the transmission but the MAC level ACK was never received.
- `EMBER_DELIVERY_FAILED = 0x66` - The APS layer attempted to send or deliver a message and failed.
- `EMBER_MESSAGE_TOO_LONG = 0x74` - The message to be transmitted is too big to fit into a single over-the-air packet.
- `EMBER_NETWORK_BUSY = 0xA1` - A message cannot be sent because the network is currently overloaded.

### Serial/Communication Errors
- `EMBER_SERIAL_INVALID_BAUD_RATE = 0x20` - Specifies an invalid baud rate.
- `EMBER_SERIAL_INVALID_PORT = 0x21` - Specifies an invalid serial port.
- `EMBER_SERIAL_TX_OVERFLOW = 0x22` - Tried to send too much data.
- `EMBER_SERIAL_RX_OVERFLOW = 0x23` - There wasn't enough space to store a received character and the character was dropped.
- `EMBER_SERIAL_RX_FRAME_ERROR = 0x24` - Detected a UART framing error.
- `EMBER_SERIAL_RX_PARITY_ERROR = 0x25` - Detected a UART parity error.
- `EMBER_SERIAL_RX_EMPTY = 0x26` - There is no received data to process.
- `EMBER_SERIAL_RX_OVERRUN_ERROR = 0x27` - The receive interrupt was not handled in time and a character was dropped.

### PHY/Radio Errors
- `EMBER_PHY_TX_SCHED_FAIL = 0x87` - The transmit attempt failed because the radio scheduler could not find a slot to transmit this packet in or a higher priority event interrupted it.
- `EMBER_PHY_TX_UNDERFLOW = 0x88` - The transmit hardware buffer underflowed.
- `EMBER_PHY_TX_INCOMPLETE = 0x89` - The transmit hardware did not finish transmitting a packet.
- `EMBER_PHY_INVALID_CHANNEL = 0x8A` - An unsupported channel setting was specified.
- `EMBER_PHY_INVALID_POWER = 0x8B` - An unsupported power setting was specified.
- `EMBER_PHY_TX_BUSY = 0x8C` - The requested operation cannot be completed because the radio is currently busy, either transmitting a packet or performing calibration.
- `EMBER_PHY_TX_CCA_FAIL = 0x8D` - The transmit attempt failed because all CCA attempts indicated that the channel was busy.
- `EMBER_PHY_TX_BLOCKED = 0x8E` - The transmit attempt was blocked from going over the air. Typically this is due to the Radio Hold Off (RHO) or Coexistence plugins as they can prevent transmits based on external signals.
- `EMBER_PHY_ACK_RECEIVED = 0x8F` - The expected ACK was received after the last transmission.

### Security Errors
- `EMBER_NO_SECURITY = 0x7C` - Security match.
- `EMBER_COUNTER_FAILURE = 0x7D` - Security match.
- `EMBER_AUTH_FAILURE = 0x7E` - Security match.
- `EMBER_APS_ENCRYPTION_ERROR = 0xA6` - An error occurred when trying to encrypt at the APS Level.
- `EMBER_SECURITY_STATE_NOT_SET = 0xA8` - There was an attempt to form or join a network with security without calling emberSetInitialSecurityState() first.
- `EMBER_KEY_INVALID = 0xB2` - The passed key data is not valid. A key of all zeros or all F's are reserved values and cannot be used.
- `EMBER_INVALID_SECURITY_LEVEL = 0x95` - The chosen security level (the value of EMBER_SECURITY_LEVEL) is not supported by the stack.

### General Errors
- `EMBER_ERR_FATAL = 0x01` - The generic "fatal error" message.
- `EMBER_BAD_ARGUMENT = 0x02` - An invalid value was passed as an argument to a function.
- `EMBER_NOT_FOUND = 0x03` - The requested information was not found.
- `EMBER_NO_BUFFERS = 0x18` - There are no more buffers.
- `EMBER_INVALID_CALL = 0x70` - The API call is not allowed given the current state of the stack.
- `EMBER_INDEX_OUT_OF_RANGE = 0xB1` - An index was passed into the function that was larger than the valid range.
- `EMBER_TABLE_FULL = 0xB4` - There are no empty entries left in the table.

## Complete Enumeration

```c
enum EmberStatus {
    EMBER_SUCCESS = 0x00,
    EMBER_ERR_FATAL = 0x01,
    EMBER_BAD_ARGUMENT = 0x02,
    EMBER_NOT_FOUND = 0x03,
    EMBER_EEPROM_MFG_STACK_VERSION_MISMATCH = 0x04,
    EMBER_EEPROM_MFG_VERSION_MISMATCH = 0x06,
    EMBER_EEPROM_STACK_VERSION_MISMATCH = 0x07,
    EMBER_NO_BUFFERS = 0x18,
    EMBER_PACKET_HANDOFF_DROP_PACKET = 0x19,
    EMBER_SERIAL_INVALID_BAUD_RATE = 0x20,
    EMBER_SERIAL_INVALID_PORT = 0x21,
    EMBER_SERIAL_TX_OVERFLOW = 0x22,
    EMBER_SERIAL_RX_OVERFLOW = 0x23,
    EMBER_SERIAL_RX_FRAME_ERROR = 0x24,
    EMBER_SERIAL_RX_PARITY_ERROR = 0x25,
    EMBER_SERIAL_RX_EMPTY = 0x26,
    EMBER_SERIAL_RX_OVERRUN_ERROR = 0x27,
    EMBER_MAC_TRANSMIT_QUEUE_FULL = 0x39,
    EMBER_MAC_UNKNOWN_HEADER_TYPE = 0x3A,
    EMBER_MAC_ACK_HEADER_TYPE = 0x3B,
    EMBER_MAC_SCANNING = 0x3D,
    EMBER_MAC_NO_DATA = 0x31,
    EMBER_MAC_JOINED_NETWORK = 0x32,
    EMBER_MAC_BAD_SCAN_DURATION = 0x33,
    EMBER_MAC_INCORRECT_SCAN_TYPE = 0x34,
    EMBER_MAC_INVALID_CHANNEL_MASK = 0x35,
    EMBER_MAC_COMMAND_TRANSMIT_FAILURE = 0x36,
    EMBER_MAC_NO_ACK_RECEIVED = 0x40,
    EMBER_MAC_RADIO_NETWORK_SWITCH_FAILED = 0x41,
    EMBER_MAC_INDIRECT_TIMEOUT = 0x42,
    EMBER_SIM_EEPROM_ERASE_PAGE_GREEN = 0x43,
    EMBER_SIM_EEPROM_ERASE_PAGE_RED = 0x44,
    EMBER_SIM_EEPROM_FULL = 0x45,
    EMBER_SIM_EEPROM_INIT_1_FAILED = 0x48,
    EMBER_SIM_EEPROM_INIT_2_FAILED = 0x49,
    EMBER_SIM_EEPROM_INIT_3_FAILED = 0x4A,
    EMBER_SIM_EEPROM_REPAIRING = 0x4D,
    EMBER_ERR_FLASH_WRITE_INHIBITED = 0x46,
    EMBER_ERR_FLASH_VERIFY_FAILED = 0x47,
    EMBER_ERR_FLASH_PROG_FAIL = 0x4B,
    EMBER_ERR_FLASH_ERASE_FAIL = 0x4C,
    EMBER_ERR_BOOTLOADER_TRAP_TABLE_BAD = 0x58,
    EMBER_ERR_BOOTLOADER_TRAP_UNKNOWN = 0x59,
    EMBER_ERR_BOOTLOADER_NO_IMAGE = 0x05A,
    EMBER_DELIVERY_FAILED = 0x66,
    EMBER_BINDING_INDEX_OUT_OF_RANGE = 0x69,
    EMBER_ADDRESS_TABLE_INDEX_OUT_OF_RANGE = 0x6A,
    EMBER_INVALID_BINDING_INDEX = 0x6C,
    EMBER_INVALID_CALL = 0x70,
    EMBER_COST_NOT_KNOWN = 0x71,
    EMBER_MAX_MESSAGE_LIMIT_REACHED = 0x72,
    EMBER_MESSAGE_TOO_LONG = 0x74,
    EMBER_BINDING_IS_ACTIVE = 0x75,
    EMBER_ADDRESS_TABLE_ENTRY_IS_ACTIVE = 0x76,
    EMBER_TRANSMISSION_SUSPENDED = 0x77,
    EMBER_MATCH = 0x78,
    EMBER_DROP_FRAME = 0x79,
    EMBER_PASS_UNPROCESSED = 0x7A,
    EMBER_TX_THEN_DROP = 0x7B,
    EMBER_NO_SECURITY = 0x7C,
    EMBER_COUNTER_FAILURE = 0x7D,
    EMBER_AUTH_FAILURE = 0x7E,
    EMBER_UNPROCESSED = 0x7F,
    EMBER_ADC_CONVERSION_DONE = 0x80,
    EMBER_ADC_CONVERSION_BUSY = 0x81,
    EMBER_ADC_CONVERSION_DEFERRED = 0x82,
    EMBER_ADC_NO_CONVERSION_PENDING = 0x84,
    EMBER_SLEEP_INTERRUPTED = 0x85,
    EMBER_PHY_TX_SCHED_FAIL = 0x87,
    EMBER_PHY_TX_UNDERFLOW = 0x88,
    EMBER_PHY_TX_INCOMPLETE = 0x89,
    EMBER_PHY_INVALID_CHANNEL = 0x8A,
    EMBER_PHY_INVALID_POWER = 0x8B,
    EMBER_PHY_TX_BUSY = 0x8C,
    EMBER_PHY_TX_CCA_FAIL = 0x8D,
    EMBER_PHY_TX_BLOCKED = 0x8E,
    EMBER_PHY_ACK_RECEIVED = 0x8F,
    EMBER_NETWORK_UP = 0x90,
    EMBER_NETWORK_DOWN = 0x91,
    EMBER_NOT_JOINED = 0x93,
    EMBER_JOIN_FAILED = 0x94,
    EMBER_INVALID_SECURITY_LEVEL = 0x95,
    EMBER_MOVE_FAILED = 0x96,
    EMBER_CANNOT_JOIN_AS_ROUTER = 0x98,
    EMBER_NODE_ID_CHANGED = 0x99,
    EMBER_PAN_ID_CHANGED = 0x9A,
    EMBER_CHANNEL_CHANGED = 0x9B,
    EMBER_NETWORK_OPENED = 0x9C,
    EMBER_NETWORK_CLOSED = 0x9D,
    EMBER_NO_BEACONS = 0xAB,
    EMBER_RECEIVED_KEY_IN_THE_CLEAR = 0xAC,
    EMBER_NO_NETWORK_KEY_RECEIVED = 0xAD,
    EMBER_NO_LINK_KEY_RECEIVED = 0xAE,
    EMBER_PRECONFIGURED_KEY_REQUIRED = 0xAF,
    EMBER_STACK_AND_HARDWARE_MISMATCH = 0xB0,
    EMBER_INDEX_OUT_OF_RANGE = 0xB1,
    EMBER_KEY_INVALID = 0xB2,
    EMBER_KEY_TABLE_INVALID_ADDRESS = 0xB3,
    EMBER_TABLE_FULL = 0xB4,
    EMBER_LIBRARY_NOT_PRESENT = 0xB5,
    EMBER_TABLE_ENTRY_ERASED = 0xB6,
    EMBER_SECURITY_CONFIGURATION_INVALID = 0xB7,
    EMBER_TOO_SOON_FOR_SWITCH_KEY = 0xB8,
    EMBER_SIGNATURE_VERIFY_FAILURE = 0xB9,
    EMBER_OPERATION_IN_PROGRESS = 0xBA,
    EMBER_KEY_NOT_AUTHORIZED = 0xBB,
    EMBER_SECURITY_DATA_INVALID = 0xBD,
    EMBER_IEEE_ADDRESS_DISCOVERY_IN_PROGRESS = 0xBE,
    EMBER_TRUST_CENTER_EUI_HAS_CHANGED = 0xBC,
    EMBER_TRUST_CENTER_SWAPPED_OUT_EUI_HAS_CHANGED = EMBER_TRUST_CENTER_EUI_HAS_CHANGED,
    EMBER_TRUST_CENTER_SWAPPED_OUT_EUI_HAS_NOT_CHANGED = 0xBF,
    EMBER_NVM3_TOKEN_NO_VALID_PAGES = 0xC0,
    EMBER_NVM3_ERR_OPENED_WITH_OTHER_PARAMETERS = 0xC1,
    EMBER_NVM3_ERR_ALIGNMENT_INVALID = 0xC2,
    EMBER_NVM3_ERR_SIZE_TOO_SMALL = 0xC3,
    EMBER_NVM3_ERR_PAGE_SIZE_NOT_SUPPORTED = 0xC4,
    EMBER_NVM3_ERR_TOKEN_INIT = 0xC5,
    EMBER_NVM3_ERR_UPGRADE = 0xC6,
    EMBER_NVM3_ERR_UNKNOWN = 0xC7,
    EMBER_APPLICATION_ERROR_0 = 0xF0,
    EMBER_APPLICATION_ERROR_1 = 0xF1,
    EMBER_APPLICATION_ERROR_2 = 0xF2,
    EMBER_APPLICATION_ERROR_3 = 0xF3,
    EMBER_APPLICATION_ERROR_4 = 0xF4,
    EMBER_APPLICATION_ERROR_5 = 0xF5,
    EMBER_APPLICATION_ERROR_6 = 0xF6,
    EMBER_APPLICATION_ERROR_7 = 0xF7,
    EMBER_APPLICATION_ERROR_8 = 0xF8,
    EMBER_APPLICATION_ERROR_9 = 0xF9,
    EMBER_APPLICATION_ERROR_10 = 0xFA,
    EMBER_APPLICATION_ERROR_11 = 0xFB,
    EMBER_APPLICATION_ERROR_12 = 0xFC,
    EMBER_APPLICATION_ERROR_13 = 0xFD,
    EMBER_APPLICATION_ERROR_14 = 0xFE,
    EMBER_APPLICATION_ERROR_15 = 0xFF,
};
```

## 0x58 during formation

- In the **EmberStatus** enum (error-def.h), `0x58` is **EMBER_ERR_BOOTLOADER_TRAP_TABLE_BAD** (bootloader).
- In **Silicon Labs SLStatus** (used by EZSP), `0x58` can also map to **TRANSMIT_BLOCKED**.
- In practice, when forming a network, **0x58** from SET_INITIAL_SECURITY_STATE, SET_EXTENDED_SECURITY_BITMASK, or FORM_NETWORK can indicate **invalid configuration**: e.g. an initial security bitmask that doesn’t match what the stack expects (missing TRUST_CENTER_USES_HASHED_LINK_KEY or REQUIRE_ENCRYPTED_KEY). Our formation now uses the same initial security bitmask as zigbee-herdsman to avoid this.

## Notes

- Status codes are one byte (uint8) values
- `EMBER_SUCCESS = 0x00` indicates success
- Network state codes: `EMBER_NETWORK_UP (0x90)`, `EMBER_NETWORK_DOWN (0x91)`, `EMBER_NOT_JOINED (0x93)`
- Common transmission errors: `EMBER_MAC_TRANSMIT_QUEUE_FULL (0x39)`, `EMBER_PHY_TX_BLOCKED (0x8E)`
- Serial communication errors: `EMBER_SERIAL_RX_OVERFLOW (0x23)`, `EMBER_SERIAL_RX_FRAME_ERROR (0x24)`
- Security: `EMBER_SECURITY_STATE_NOT_SET (0xA8)` — form/join with security without calling emberSetInitialSecurityState() first; `EMBER_SECURITY_CONFIGURATION_INVALID (0xB7)` — invalid security configuration.