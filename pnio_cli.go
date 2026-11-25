package main

// "plcmonitor pnio-test": the PROFINET IO test from the command line (Phase 0
// spike, before the Topology window gets it). Connects to one device as IO
// controller, prints its inputs and lets you force its outputs.

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
)

const pnioCLIHelp = `commands:
  arm                  outputs act (IOPS good). Before this they keep substitute values.
  disarm               outputs back to substitute values, data cleared
  out <slot> <hex>     set the outputs of a slot (subslot 1), e.g. "out 2 55"
  blink [slot]         toggle the outputs 55/AA every second (arm first); "blink off" stops
  status               state, counters, timing
  quit                 release the AR and exit (Ctrl+C does the same)`

func runPNIOTest(args []string) error {
	fs := flag.NewFlagSet("pnio-test", flag.ContinueOnError)
	adapter := fs.String("adapter", "", "network adapter (pcap ID or Windows name, e.g. \"Ethernet 2\"); default: the one in the device's subnet")
	ipS := fs.String("ip", "", "device IP address (required)")
	macS := fs.String("mac", "", "device MAC address (default: found by DCP)")
	localS := fs.String("local-ip", "", "this PC's IP on that adapter (default: the one in the device's subnet)")
	slotsS := fs.String("slots", "", "modules after the DAP, e.g. \"1:0x30/0x130:i1,2:0x31/0x131:o1\" (default: p-net sample DI 8 + DO 8)")
	ports := fs.Int("ports", 1, "number of Ethernet ports of the device (DAP port submodules 0x8001…)")
	dapS := fs.String("dap", "0x1/0x1", "DAP module/submodule ident numbers")
	cycleMs := fs.Int("cycle", 32, "cycle time in ms: 1, 2, 4, … 512")
	wdf := fs.Int("wdf", 10, "watchdog factor (cycles the device waits before it drops the AR)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	q := pnioStartReq{Adapter: *adapter, IP: *ipS, MAC: *macS, Slots: *slotsS, Ports: *ports, DAP: *dapS,
		CycleMs: *cycleMs, WDF: *wdf, LocalIP: *localS}
	cfg, conn, link, err := pnioPrepare(q, func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
	if err != nil {
		return err
	}
	fmt.Println("Expected configuration:")
	for _, sl := range cfg.Slots {
		for _, s := range sl.Subs {
			fmt.Printf("  slot %d subslot 0x%04X  module 0x%X submodule 0x%X  in %d out %d\n", sl.Slot, s.Subslot, sl.Module, s.Ident, s.In, s.Out)
		}
	}
	fmt.Println("While connected, no PLC can connect to this device. Outputs keep their substitute values until you type \"arm\".")
	cfg.OnEvent = func(e string) { fmt.Printf("%s  %s\n", time.Now().Format("15:04:05.000"), e) }
	sess, err := startPNIO(cfg, link, conn)
	if err != nil {
		return err
	}
	fmt.Println(pnioCLIHelp)

	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			lines <- strings.TrimSpace(sc.Text())
		}
		close(lines)
	}()
	intr := make(chan os.Signal, 1)
	signal.Notify(intr, os.Interrupt)
	defer signal.Stop(intr)

	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var lastIn string
	blinkSlot := uint16(0)
	var blinkOn bool
	lastBlink := time.Now()
	firstOut := func() uint16 {
		for _, o := range sess.Snapshot().Outputs {
			return o.Slot
		}
		return 0
	}
	defer sess.Close()
	for {
		select {
		case <-intr:
			return nil
		case <-tick.C:
			sn := sess.Snapshot()
			var parts []string
			for _, v := range sn.Inputs {
				parts = append(parts, fmt.Sprintf("slot %d: %s (IOPS %02X)", v.Slot, bitString(v.Data), v.IOPS))
			}
			if s := strings.Join(parts, "  "); s != lastIn {
				lastIn = s
				fmt.Printf("%s  inputs  %s\n", time.Now().Format("15:04:05.000"), s)
			}
			if blinkSlot != 0 && sn.Armed && time.Since(lastBlink) >= time.Second {
				lastBlink = time.Now()
				blinkOn = !blinkOn
				v := byte(0x55)
				if blinkOn {
					v = 0xAA
				}
				if err := sess.SetOutput(blinkSlot, 1, []byte{v}); err != nil {
					fmt.Println(err)
					blinkSlot = 0
				}
			}
		case line, ok := <-lines:
			if !ok {
				return nil
			}
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			switch strings.ToLower(f[0]) {
			case "quit", "q", "exit":
				return nil
			case "arm":
				sess.Arm(true)
			case "disarm":
				blinkSlot = 0
				sess.Arm(false)
			case "out":
				if len(f) != 3 {
					fmt.Println("usage: out <slot> <hex>")
					continue
				}
				slot, err := parseNum(f[1])
				data, err2 := hex.DecodeString(f[2])
				if err != nil || err2 != nil {
					fmt.Println("usage: out <slot> <hex>, e.g. out 2 0F")
					continue
				}
				if err := sess.SetOutput(uint16(slot), 1, data); err != nil {
					fmt.Println(err)
				}
			case "blink":
				switch {
				case len(f) > 1 && f[1] == "off":
					blinkSlot = 0
				case len(f) > 1:
					v, err := parseNum(f[1])
					if err != nil {
						fmt.Println("usage: blink [slot|off]")
						continue
					}
					blinkSlot = uint16(v)
				default:
					blinkSlot = firstOut()
				}
				if blinkSlot != 0 && !sess.Snapshot().Armed {
					fmt.Println("type \"arm\" first")
				}
			case "status":
				sn := sess.Snapshot()
				fmt.Printf("state %s, outputs %s\n", sn.State, map[bool]string{true: "ARMED", false: "not armed"}[sn.Armed])
				fmt.Printf("device data status %s, %d input frames, last %v ago\n", dataStatusText(sn.InStatus), sn.InCount, time.Since(sn.LastIn).Round(time.Millisecond))
				fmt.Printf("%d output frames sent, %d late (> half a cycle), worst %v late\n", sn.Sent, sn.Late, sn.MaxLate.Round(10*time.Microsecond))
				for _, v := range sn.Outputs {
					fmt.Printf("output slot %d: %s IOPS %02X, device IOCS %02X\n", v.Slot, bitString(v.Data), v.IOPS, v.IOCS)
				}
			default:
				fmt.Println(pnioCLIHelp)
			}
		}
	}
}

// bitString shows bytes as bits, bit 7 first: "0000 0101".
func bitString(b []byte) string {
	var parts []string
	for _, x := range b {
		parts = append(parts, fmt.Sprintf("%04b %04b", x>>4, x&0xF))
	}
	return strings.Join(parts, " | ")
}
