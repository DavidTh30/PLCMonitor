//go:build linux

package main

// Raw Ethernet for PROFINET DCP on Linux: an AF_PACKET socket, built into the
// kernel. It needs root or the CAP_NET_RAW capability, which
// "plcmonitor install-agent" gives the agent service.

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"
)

const ethPAll = 0x0003 // ETH_P_ALL: also VLAN-tagged answers, filtered in dcpPayload

func htons(v uint16) uint16 { return v<<8 | v>>8 }

const linuxNoPerm = "raw Ethernet access needs root or the CAP_NET_RAW capability: " +
	"install the agent with  sudo ./plcmonitor install-agent  (or start it with sudo)"

func init() {
	dcpUnavailable = linuxNoPerm
	dcpListAdapter = linuxAdapters
	dcpOpen = linuxOpen
}

// rawAllowed tells whether this process may open a raw packet socket.
func rawAllowed() error {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(ethPAll)))
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			return errors.New(linuxNoPerm)
		}
		return err
	}
	syscall.Close(fd)
	return nil
}

func linuxAdapters() ([]DCPAdapter, error) {
	if err := rawAllowed(); err != nil {
		return nil, err
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	list := []DCPAdapter{}
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 || len(i.HardwareAddr) != 6 {
			continue
		}
		a := DCPAdapter{ID: i.Name, Name: i.Name, MAC: i.HardwareAddr.String(), IPs: []string{}}
		if addrs, err := i.Addrs(); err == nil {
			for _, ad := range addrs {
				if n, ok := ad.(*net.IPNet); ok && n.IP.To4() != nil {
					a.IPs = append(a.IPs, n.IP.String())
				}
			}
		}
		list = append(list, a)
	}
	return list, nil
}

type rawLink struct {
	fd    int
	index int
	mac   net.HardwareAddr
}

func linuxOpen(name string) (dcpLink, error) {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("network adapter %q: %v", name, err)
	}
	if len(ifi.HardwareAddr) != 6 {
		return nil, fmt.Errorf("%s is not an Ethernet adapter", name)
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(htons(ethPAll)))
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			return nil, errors.New(linuxNoPerm)
		}
		return nil, err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(ethPAll), Ifindex: ifi.Index}); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind to %s: %v", name, err)
	}
	return &rawLink{fd: fd, index: ifi.Index, mac: ifi.HardwareAddr}, nil
}

func (l *rawLink) MAC() net.HardwareAddr { return l.mac }

func (l *rawLink) Send(frame []byte) error {
	sa := &syscall.SockaddrLinklayer{Protocol: htons(dcpEtherType), Ifindex: l.index, Halen: 6}
	copy(sa.Addr[:], frame[0:6])
	if err := syscall.Sendto(l.fd, frame, 0, sa); err != nil {
		return fmt.Errorf("send: %v", err)
	}
	return nil
}

func (l *rawLink) Recv(deadline time.Time) ([]byte, error) {
	buf := make([]byte, 1600)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return nil, nil
		}
		if left > 200*time.Millisecond {
			left = 200 * time.Millisecond
		}
		tv := syscall.NsecToTimeval(left.Nanoseconds())
		syscall.SetsockoptTimeval(l.fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
		n, from, err := syscall.Recvfrom(l.fd, buf, 0)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
				continue
			}
			return nil, fmt.Errorf("receive: %v", err)
		}
		// Skip our own outgoing frames (the socket sees both directions).
		if ll, ok := from.(*syscall.SockaddrLinklayer); ok && ll.Pkttype == 4 { // PACKET_OUTGOING
			continue
		}
		if _, _, ok := dcpPayload(buf[:n]); ok {
			return append([]byte(nil), buf[:n]...), nil
		}
	}
}

func (l *rawLink) Close() error { return syscall.Close(l.fd) }
