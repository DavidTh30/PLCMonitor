# PROFINET IO test (PRONETA-style) – feasibility and design

Status: design note, no code yet. Date: 2026-11-25.

Goal: in the Topology window, pick a PROFINET device found by DCP, load its
GSDML file, connect to it **as an IO controller**, show its inputs live and let
the user force its outputs. First target: an ESP32 board running the RT-Labs
p-net stack with 8 DI / 8 DO (the p-net sample app GSDML).

## Recommendation (short)

**Feasible.** Do it in phases and stop after Phase 0 if the go/no-go test fails.

- Limit it to what PRONETA's IO test needs: **RT_CLASS_1** (plain RT, no IRT),
  one IOCAR, no shared device, no system redundancy, no MRP, no fast startup,
  **no PROFIsafe** (F-modules are refused), slow cycle (32–128 ms).
- Everything fits the existing raw-frame layer. It needs one new Npcap call
  (immediate mode / `pcap_setmintocopy`) and a UDP socket on port 34964.
  No cgo and no new modules: `encoding/xml`, `encoding/binary` and `net` are
  enough. The repo already has `github.com/google/uuid` as an indirect dependency.
- Estimate: **about 25 working days (5 weeks), range 20–32**, including tests
  against the ESP32. Phase 0 (go/no-go spike) takes **4–5 days**.

## 1. What a minimal IO controller has to do

Sequence for one device (C = PLC Monitor as controller, D = device):

```
C → D  DCP Identify / name check (already exists)
C → D  RPC Connect.req       (UDP → D:34964)  AR + 2×IOCR + AlarmCR + ExpectedSubmodule
D → C  RPC Connect.res       (ModuleDiffBlock if the config differs, IOCR FrameIDs)
C ⇄ D  cyclic RT frames start (EtherType 0x8892), both directions
C → D  RPC Write.req         (parameter records from GSDML defaults, IODWriteMultiple)
C → D  RPC Control.req       PrmEnd
D → C  RPC Control.req       ApplicationReady   (UDP → C:34964, C must serve this)
C → D  RPC Control.res       ApplicationReady done
       … data exchange: read inputs, write outputs, acknowledge alarms …
C → D  RPC Release.req       (or stop sending → device watchdog → substitute values)
```

### 1.1 DCE/RPC over UDP (connectionless RPC, version 4)

- 80-byte CL RPC header: packet type (request 0, response 2, fault 3, reject 6,
  ping/working/ack), flags, data representation (we send little-endian, ASCII,
  IEEE), serial number, **object UUID**, **interface UUID**, **activity UUID**,
  server boot time, interface version, sequence number, opnum, interface hint,
  activity hint, body length, fragment number, auth protocol 0.
- Interface UUIDs: IO Device `DEA00001-6C97-11D1-8271-00A02442DF7D`,
  IO Controller `DEA00002-…` (needed to serve ApplicationReady).
- Object UUID: `DEA00000-6C97-11D1-8271-` + instance (2 bytes) + DeviceID +
  VendorID. DeviceID and VendorID are known from DCP and from the GSDML, so
  **we can skip the endpoint mapper** and send straight to port 34964 (0x8894).
- Opnums: Connect 0, Release 1, Read 2, Write 3, Control 4, ReadImplicit 5.
- Body = NDR header (ArgsMaximum, ArgsLength, MaximumCount, Offset,
  ActualCount; little-endian in our data rep), then **PNIO blocks in big-endian**
  (BlockType, BlockLength, BlockVersion 1.0, then fields).
- Response body: PNIOStatus (4 bytes: ErrorCode, ErrorDecode, ErrorCode1,
  ErrorCode2), then NDR header, then blocks. Map common PNIOStatus codes to text
  ("AR already exists", "wrong module ident", …). That matters for the UI.
- Fragmentation: a Connect for DAP + a few modules is well below one UDP
  datagram (~1460 bytes). Phase 1 does not fragment and refuses a configuration
  whose Connect would be larger. Fragmentation can come later.
- Retransmission: resend a request after ~1 s, at most 3 times. Answer `ping`
  with `working` while we serve ApplicationReady.

### 1.2 Connect request blocks

| Block | Type | What we fill in |
|---|---|---|
| ARBlockReq | 0x0101 | ARType 0x0001 (IOCARSingle), new ARUUID, SessionKey, our MAC, CMInitiatorObjectUUID (controller object UUID), ARProperties = state Active, parametrization server = CM initiator, **supervisor takeover NOT allowed**, startup mode from GSDML (legacy or advanced; p-net does both), activity timeout factor (e.g. 600 = 60 s), CMInitiatorUDPRTPort 0x8892, our station name (e.g. `plcmonitor-iotest`). |
| IOCRBlockReq (input) | 0x0102 | IOCRType 1, IOCRReference 1, LT 0x8892, properties RT_CLASS_1, DataLength (≥ 40), FrameID (we propose from 0xC000–0xF7FF), SendClockFactor, ReductionRatio, Phase 1, FrameSendOffset 0xFFFFFFFF, WatchdogFactor, DataHoldFactor, tag header (prio 6, VLAN 0), multicast MAC 0. API list with IODataObjects (slot, subslot, frame offset) and IOCS entries (slot, subslot, frame offset). |
| IOCRBlockReq (output) | 0x0102 | IOCRType 2, IOCRReference 2, the same with the output layout. |
| AlarmCRBlockReq | 0x0103 | AlarmCRType 1, LT 0x8892, properties (priority from user, transport RTA over L2), RTATimeoutFactor (e.g. 1 = 100 ms), RTARetries 3, LocalAlarmReference, MaxAlarmDataLength 200, tag headers high/low. |
| ExpectedSubmoduleBlockReq | 0x0104 | One API (0) per slot: SlotNumber, ModuleIdentNumber, ModuleProperties 0, then per submodule: SubslotNumber, SubmoduleIdentNumber, SubmoduleProperties (type NO_IO / INPUT / OUTPUT / INPUT_OUTPUT), DataDescription (direction, SubmoduleDataLength, LengthIOCS 1, LengthIOPS 1). |

Frame layout rules (the usual pitfalls):

- Every submodule has an IOPS in the frame that carries its data and an IOCS
  in the frame going the other way. **NO_IO submodules (the DAP, the interface
  0x8000 and the ports 0x8001…) still get a 1-byte IOPS in the input CR and a
  1-byte IOCS in the output CR.** Input-only submodules: data+IOPS in the input
  frame, IOCS in the output frame. Output-only: the reverse.
- The C_SDU is padded to at least 40 bytes; frame offsets come from our layout.
- The device answers with IOCRBlockRes (FrameIDs confirmed) and, if the real
  modules differ, a **ModuleDiffBlock** (0x8104). Show that block to the user:
  it is the most useful error message ("slot 2 has module 0x31, expected 0x32").

### 1.3 Records, PrmEnd, ApplicationReady

- After Connect: IODWriteMultipleReq (index 0xE040) with one IODWriteReq per
  ParameterRecordDataItem from the GSDML, filled with default values. p-net's
  sample modules have few or no parameters; Phase 1 can send only records the
  GSDML marks as needed and leave out the rest.
- IODControlReq (0x0110) with ControlCommand = PrmEnd → IODControlRes (0x8110).
- The device then sends IOXControlReq (0x0112) ApplicationReady **as an RPC
  client to our port 34964**. We answer with IOXControlRes (0x8112) Done. Only
  then is the AR in data exchange (device IOPS GOOD, data valid).
- Release: IODReleaseReq (0x0114) ControlCommand Release on Stop.

### 1.4 Cyclic RT frames

```
dst MAC | src MAC | [VLAN 0x8100 prio 6, optional] | 0x8892 | FrameID (2) |
C_SDU (data + IOPS + IOCS, ≥ 40 bytes) | CycleCounter (2) | DataStatus (1) | TransferStatus (1)
```

- One output frame per cycle, cycle = 31.25 µs × SendClockFactor × ReductionRatio.
  With SCF 32 (1 ms) and RR 32/64/128 → 32/64/128 ms.
- CycleCounter counts in 31.25 µs units: add SCF × RR each frame.
- DataStatus we send: State Primary (bit 0), Redundancy 0, DataValid (bit 2),
  ProviderState Run (bit 4), StationProblemIndicator normal (bit 5), Ignore 0
  → 0x35.
- IOPS: 0x80 = GOOD, 0x00 = BAD. IOCS for each input submodule: 0x80 once we use
  the data.
- **Safe default**: until the user arms output forcing, we send every output
  submodule's **IOPS = BAD** and data 0. The device then applies its substitute
  values (usually 0). Arming switches to IOPS GOOD.
- Receive: check FrameID, source MAC, DataStatus (Primary, DataValid,
  ProviderState Run) and the device's IOPS per submodule. Also watch for the
  input frames stopping; that is our own watchdog (shown as "device stopped
  sending", AR aborted).
- The device supervises us with WatchdogFactor × cycle. If our output frames
  stop (crash, PC asleep, cable pulled), it aborts the AR and goes to substitute
  values. That is the main safety net and the reason to choose a generous
  watchdog (see §3).

### 1.5 Alarms (minimum for a simple device)

Alarms come on the Alarm CR as RTA frames over EtherType 0x8892, FrameID
0xFC01 (high) / 0xFE01 (low): RTA header with AlarmDstEndpoint,
AlarmSrcEndpoint, PDUType (DATA 1, NACK 2, ACK 3, ERR 4), AddFlags, SendSeqNum,
AckSeqNum, VarPartLen.

Minimum to keep the AR alive:

1. Answer every RTA DATA with an RTA ACK (sequence numbers), and drop duplicates.
2. For AlarmNotification (diagnosis appears/disappears, process alarm, pull,
   plug, plug wrong submodule, return of submodule) send **AlarmAck** as an RTA
   DATA (block 0x8001/0x8002) and show the alarm in the UI.
3. Send RTA ERR and abort cleanly on Stop.

If the device's alarm is not acknowledged within RTATimeoutFactor × 100 ms ×
retries, the device aborts the AR. So this part is not optional, even if the
board never raises a diagnosis: p-net sends a "return of submodule" or plug
alarm after some state changes.

Pull/plug at run time: show it, and offer "reconnect" instead of
re-parametrizing a single submodule (that comes later, if ever).

## 2. How it fits the existing code

### 2.1 Raw frames

- PROFINET RT, RTA and DCP all use **EtherType 0x8892**, so
  `rawOpen(adapter, 0x8892, false)` already opens the right kind of link on
  Windows (`dcp_windows.go` `npcapOpenRaw`) and Linux (`dcp_linux.go`
  `linuxOpenRaw`). The `dcpLink` interface (Send / Recv(deadline) / Close /
  MAC) is enough for the cyclic engine.
- An IO test session opens **its own link**. Frames are told apart by FrameID
  (DCP 0xFEFC–0xFEFF, RT 0xC000–0xF7FF, alarms 0xFC01/0xFE01). The BPF filter
  should also limit it to the device: `ether proto 0x8892 and ether src <dev
  MAC>`. That needs a new optional parameter (a filter string or source MAC) on
  `rawOpen`, or a separate `rtOpen`. Without it every DCP and RT frame on the
  wire is copied to us.
- **Npcap latency**: `npcapOpenRaw` uses `pcap_open_live(…, to_ms=100)`. In
  Npcap the driver holds packets until the timeout expires or the buffer
  reaches `mintocopy`, so inputs would come in batches of up to 100 ms. For
  the IO test link use **`pcap_setmintocopy(h, 0)`** (or
  `pcap_create` + `pcap_set_immediate_mode` + `pcap_activate`) and a short
  read timeout (1–10 ms). Add the symbol to `loadPcap` as optional, so older
  Npcap still works for DCP.
- Npcap also captures our own sent frames; the source-MAC filter above removes
  them (Linux already skips outgoing frames).
- Sending goes through `pcap_sendpacket`, ~10–50 µs. That is not a problem.

### 2.2 UDP / RPC

- Plain Go `net.ListenUDP` on the adapter's IPv4 address. No raw access needed.
- **PLC Monitor must listen on UDP 34964** for the device's ApplicationReady.
  Problems: the Windows Firewall prompt on first use (document it; the installer
  could add a rule), and a port clash with Siemens services (PRONETA, TIA
  Portal's PNIO service, S7DOS) that may already hold 34964. If the port is
  taken, show that clearly ("close PRONETA/TIA or use the Linux agent").
- The PC needs an IPv4 address **in the device's subnet** on that adapter.
  The Topology window knows both (DCP device IP, adapter IPs) and can warn
  before connecting. It can offer the existing "Set IP" if the device has none.

### 2.3 Where the controller runs (local vs. Linux agent)

The cyclic loop has to run on the machine that owns the raw link.

- **local** (`localDCP`): a goroutine in PLC Monitor runs the session.
- **agent** (`agentDCP`, `plcmonitor -dcp-agent`): the agent runs the session.
  Cyclic data cannot go over HTTP request by request. The dashboard calls a
  session API, and the agent keeps the AR alive by itself:
  - `POST /api/pnio/connect` {adapter, mac, ip, gsdml id, slot config, cycle} → session id
  - `GET  /api/pnio/session/{id}` → state, inputs, IOPS/IOCS, alarms, counters (poll 100–250 ms, or SSE)
  - `POST /api/pnio/session/{id}/outputs` {arm, data}
  - `POST /api/pnio/session/{id}/heartbeat`
  - `DELETE /api/pnio/session/{id}` → Release
  - The GSDML (or the parsed, minimal JSON config) is sent with connect, so the
    agent needs no file access.
- The same handler pattern as `dcpAPI` / `dcpAccess` works: `query` for
  reading, `change` (loopback and not read-only) for connect, outputs and
  release. Log every connect, arm and force with `log.Printf`, like DCP SET.

Suggested files: `pnio_gsdml.go` (parser), `pnio_rpc.go` (CL RPC + NDR +
blocks), `pnio_ar.go` (Connect/Write/Control/Release state machine),
`pnio_cyclic.go` (RT engine), `pnio_alarm.go` (RTA), `pnio_api.go` (HTTP +
agent), plus tests with a fake link and a fake device (as `dcp_test.go` does).

### 2.4 GSDML: what has to be parsed

GSDML is XML (`ISO15745Profile`, schema V2.x). It is often given as a zip
with bitmaps; accept `.xml` and `.zip`. Parse with `encoding/xml` into a small
model; ignore what we do not use.

Needed:

- `ProfileBody/DeviceIdentity`: VendorID, DeviceID (check against DCP;
  warn if they differ).
- `DeviceAccessPointList/DeviceAccessPointItem` (one or more DAPs, the user
  picks one; default = the one matching the device): ID, ModuleIdentNumber,
  PhysicalSlots, FixedInSlots, MinDeviceInterval, name (TextId →
  `ExternalTextList`).
  - `VirtualSubmoduleList/VirtualSubmoduleItem` (subslot 1 of slot 0,
    SubmoduleIdentNumber, IOData if any).
  - `SystemDefinedSubmoduleList/InterfaceSubmoduleItem` (SubslotNumber,
    usually 0x8000, SubmoduleIdentNumber, `ApplicationRelations/TimingProperties`
    SendClock and ReductionRatio lists, plus `StartupMode` / advanced-startup
    support flags) and `PortSubmoduleItem` (0x8001, 0x8002…).
  - `UseableModules/ModuleItemRef`: ModuleItemTarget, AllowedInSlots,
    FixedInSlots, UsedInSlots.
- `ModuleList/ModuleItem`: ID, ModuleIdentNumber, name,
  `VirtualSubmoduleList/VirtualSubmoduleItem` with SubmoduleIdentNumber,
  `IOData/Input|Output/DataItem` (DataType and, for OctetString/
  VisibleString, Length → byte length; `BitDataItem` names for the bit view),
  `RecordDataList/ParameterRecordDataItem` (Index, Length, `Ref` with ByteOffset,
  BitOffset, DataType, DefaultValue; `Const` with Data) to build default
  parameter records.
- `ExternalTextList/PrimaryLanguage` for display names and channel names.
- **Refuse**: modules with `PROFIsafeSupported="true"` or F-parameters
  (record index 0x…F-params), IRT-only DAPs, and SubmoduleList / pluggable
  submodules (V2.3+ `UseableSubmodules`) in Phase 1. Show "not supported in
  the IO test" instead of guessing.

Module configuration (which module is in which slot):

- Fixed slots come from the GSDML. For free slots, the user picks the module
  from a list, as in PRONETA.
- Better: read the real configuration first with an **implicit Read**
  (ReadImplicit opnum 5, no AR) of **RealIdentificationData (index 0xF000)** and
  pre-fill the slot table. Many devices support it; whether the p-net build on
  the ESP32 does has to be tested. Fall back to manual choice.
- The p-net sample app accepts whatever the controller expects (it "plugs"
  modules on request), so for the ESP32 the user just picks DI 8 in slot 1 and
  DO 8 in slot 2 (or the DIO 8 module).

## 3. Timing on Windows with Npcap

- SendClock 1 ms (SCF 32) is the usual minimum in GSDMLs. Allowed
  ReductionRatios come from `TimingProperties` (RT_CLASS_1 typically 1–512).
- **Default cycle 32 ms, WatchdogFactor 10** (watchdog ≈ 320 ms), selectable
  16/32/64/128 ms. The total watchdog time must stay ≤ 1.92 s (spec limit).
- Go's ticker on Windows uses high-resolution waitable timers: typical jitter
  under 1–2 ms, with occasional spikes of 10–20 ms under load. GC pauses are
  sub-millisecond. At 32 ms × 10 that leaves a large margin. A 1–4 ms cycle
  would need a dedicated thread, timeBeginPeriod and luck. **Do not offer it.**
- Run the send loop in its own goroutine with `runtime.LockOSThread`. Precompute
  the frame and only patch data and counters each cycle. Send at absolute
  deadlines (next = start + n·T), not "sleep T after the send", so the cycle
  does not drift.
- Laptop sleep, Wi-Fi adapter, USB-Ethernet adapters with power saving: the AR
  will drop. That is expected; show "AR aborted (watchdog)" and offer
  Reconnect.
- Linux (raw socket or agent) has better timing; same defaults.
- An IO test is a diagnostic tool, not a control system. Say that in the UI.

## 4. Risks

### 4.1 Running alongside a real controller

- A device with an active IOCAR from a PLC normally **rejects** our Connect
  (no free AR, or "AR already exists"). That protects the plant. We **never set
  "supervisor takeover allowed"** and never use the IOSAR/supervisor AR types,
  so we cannot steal a running PLC's AR.
- The real risk is the opposite: the PLC is **stopped or temporarily offline**.
  Our AR then holds the device, and when the PLC comes back its Connect fails.
  The station shows as failed in the PLC, and the process may react. Countermeasures:
  - Before connecting, warn: "This device may belong to a PLC. While the IO test
    runs, that PLC cannot connect to it." Require the station name to be typed
    or confirmed.
  - Optional pre-check: an implicit read of the ARData record (0xF820) or of I&M,
    or watch for 1–2 s whether cyclic RT frames from/to that MAC are already on
    the wire (we receive 0x8892 anyway). If yes, refuse.
  - Release the AR at once on Stop, on window close, on lost UI heartbeat, and
    on program exit.
- DCP name or IP changes while an AR is active would break it. Disable Set IP /
  name / reset for that device while a session runs.
- Duplicate FrameIDs: with RT_CLASS_1 unicast, FrameIDs only have to be unique
  per receiver, so they do not clash with other controllers.

### 4.2 Forcing outputs safely

Forcing outputs moves real actuators. Rules:

1. Forcing needs the `change` permission (loopback, not a read-only dashboard),
   like DCP Set. Connecting just to watch inputs is the default mode.
2. **Two steps**: connect (outputs IOPS BAD → substitute values), then an
   explicit "Enable output forcing" with a confirmation that names the station
   and warns about moving machinery.
3. All outputs start at 0 when forcing is armed. Nothing is restored from a
   previous session.
4. **Dead-man heartbeat**: the browser sends a heartbeat every 1 s; after 3 s
   without one (tab closed, network lost to the agent) the session disarms
   (IOPS BAD) and releases the AR. The device watchdog covers a crash of PLC
   Monitor itself.
5. "Disarm" and "Stop" are always visible. Esc disarms.
6. No PROFIsafe, ever. F-modules are refused at GSDML load.
7. Log connect, arm, every output change (station, slot/subslot, value, user
   address) and release.

### 4.3 Technical risks

| Risk | Impact | Mitigation |
|---|---|---|
| Encoding errors in Connect (block lengths, NDR, padding) | Device rejects or ignores us | Byte-exact tests against Wireshark captures of a real controller ↔ p-net; Wireshark's PNIO dissector to check our frames |
| Device-specific behaviour (advanced startup, records it requires, timing limits) | Some devices will not run | Phase 1 targets p-net; test with an ET 200SP or similar before calling it done; show PNIOStatus text |
| Port 34964 taken / firewall | ApplicationReady never arrives → AR times out | Detect bind failure and explain; installer firewall rule; agent alternative |
| Npcap batching | Laggy inputs | `pcap_setmintocopy(0)` / immediate mode |
| GSDML variety (V2.1–V2.45, multi-DAP, submodule lists) | Some files will not load | Clear "not supported" messages; grow the parser from real files |
| Scope creep toward a full controller | Time | Keep to the limits at the top; no IRT/MRP/shared device |

## 5. UI in the Topology window

- Device row → "IO test…" (enabled when DCP shows name and IP).
- Step 1: load GSDML (file picker; keep loaded GSDMLs in the config folder
  keyed by VendorID/DeviceID so the next time they are found by themselves).
- Step 2: DAP + slot table (pre-filled by implicit read or fixed slots), cycle
  time selection.
- Step 3: Connect → status line (Connecting / Parametrizing / Waiting for
  ApplicationReady / Data exchange / Aborted: reason, PNIOStatus text,
  ModuleDiff table).
- Step 4: per submodule, an input bit grid (8 LEDs for DI 8) with IOPS and
  value; an output grid of toggles, disabled until armed; an alarm list;
  counters (frames sent/received, late cycles, max jitter).

## 6. Plan and estimate

| Phase | Content | Days |
|---|---|---|
| **0 – spike (go/no-go)** | Hard-coded p-net sample config (DAP + DI8 + DO8), CL RPC Connect/Control/Release, ApplicationReady server, cyclic loop on Windows Npcap with immediate mode, minimal RTA ACK. Proof: LEDs on the ESP32 follow a test toggle, inputs read; AR stays up for 1 h at 32 ms. | 4–5 |
| 1 – RPC/blocks properly | NDR + block encoder/decoder with tests, PNIOStatus texts, ModuleDiffBlock, Write records, retransmit, ping/working | 4–5 |
| 2 – GSDML | Parser, DAP/modules/submodules/IO lengths/records/texts, zip support, frame-layout calculation, tests with p-net and 2–3 vendor files | 4–5 |
| 3 – cyclic + alarms | Production RT engine (deadlines, stats, own watchdog), full RTA (ACK/AlarmAck/duplicates/ERR), Linux raw path | 3–4 |
| 4 – API + agent | Session API, agent support, heartbeat/arming/logging, read-only gating | 2–3 |
| 5 – UI | Topology "IO test" panel as in §5 | 3–4 |
| 6 – interop + hardening | ESP32 p-net, one Siemens device (e.g. ET 200SP), Linux agent, Wireshark checks, docs/help/CHANGELOG | 3–5 |
| **Total** | | **≈ 23–31 days (≈ 5–6 weeks)** |

Phase 0 is cheap and answers the main open questions: does p-net on the ESP32
accept our Connect, and is the Windows/Npcap path stable at 32 ms? If Phase 0
fails on Windows only, the Linux agent path is the fallback, and the rest of
the plan still holds.

## 6a. Phase 0 spike – status and how to run it

Code: `pnio_rpc.go` (CL RPC, NDR, blocks, PNIOStatus), `pnio_ar.go` (config,
frame layout, Connect/PrmEnd/ApplicationReady/Release, session), `pnio_cyclic.go`
(cyclic frames, input watchdog, RTA ACK + AlarmAck), `pnio_cli.go` (console
command), `pnio_test.go` (layout, block lengths, a fake p-net device over a fake
cable and loopback UDP). `dcp_windows.go` got `npcapOpenRT`: BPF filter on the
device's MAC, 1 ms read timeout, `pcap_setmintocopy(0)` when Npcap has it.
Linux uses the existing raw socket.

Verified so far: unit tests and the fake device only, which share our own
reading of the spec. **Nothing has run against a real device yet**: that is
the go/no-go test.

Run it (console build `plcmonitor.exe`, Npcap installed, PC IP in the board's subnet):

```powershell
plcmonitor.exe pnio-test -ip 192.168.0.50
```

Options: `-adapter "Ethernet 2"`, `-cycle 32`, `-wdf 10`, `-ports 1`,
`-dap 0x1/0x1`, `-slots "1:0x30/0x130:i1,2:0x31/0x131:o1"` (default = p-net
sample DI 8 in slot 1, DO 8 in slot 2). Commands once connected: `arm`, `out 2 55`,
`blink`, `status`, `disarm`, `quit`.

Windows Firewall must let the device's ApplicationReady in (UDP 34964), e.g.
as administrator:

```powershell
New-NetFirewallRule -DisplayName "PLC Monitor PROFINET IO" -Direction Inbound -Protocol UDP -LocalPort 34964 -Action Allow
```

Go/no-go checklist:

1. `connected` appears (if not: the PNIOStatus text names the faulty block;
   a Wireshark capture with the `pn_io` dissector shows the field).
2. `device: ApplicationReady` and `state: data exchange` appear.
3. Inputs follow the DI switches; `arm` + `blink` makes the DO LEDs blink.
4. `status` after 1 h: AR still up, "late" count small, worst lateness well
   below the watchdog time.

Known spike limits: no GSDML, no parameter records (Write), no RPC
fragmentation (a fragmented response looks like "no answer"), legacy startup
only, IOCR frame IDs proposed as 0xC010/0xC011.

## 7. Open questions for the user

1. Which ESP32 p-net build and GSDML exactly (sample app version)? Wireshark
   captures of it with any controller (e.g. a Siemens PLC or Codesys) would
   make the encoding work much quicker.
2. Is "watch inputs only" (no forcing) acceptable for a first release, with
   forcing following once the safety part has been reviewed?
3. Must it work without Npcap, i.e. via the Linux agent only, or both from the
   start?
4. Any Siemens device available for an interop test besides the ESP32?
