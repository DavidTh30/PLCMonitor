// Command mkres builds the Windows resources for PLC Monitor: the program icon
// (shown by Explorer, the taskbar and shortcuts) and an application manifest.
// It writes them as a COFF object, rsrc_windows_amd64.syso, which `go build`
// links in automatically, plus a matching .ico file. It uses only the standard
// library.
//
//	go run ./tools/mkres            (from the project folder)
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"sort"
)

const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0" xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
  <assemblyIdentity type="win32" name="PLCMonitor" version="1.0.0.0" processorArchitecture="*"/>
  <description>PLC Monitor</description>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
  <asmv3:application>
    <asmv3:windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2, PerMonitor</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </asmv3:windowsSettings>
  </asmv3:application>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
</assembly>
`

// Icon sizes Windows asks for at the usual display scalings.
var sizes = []int{16, 20, 24, 32, 40, 48, 64, 256}

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtManifest  = 24
	langEnUS    = 0x0409
)

func main() {
	in := flag.String("icon", "winres/icon.png", "square PNG icon, 256x256")
	out := flag.String("o", "rsrc_windows_amd64.syso", "output COFF object")
	ico := flag.String("ico", "winres/plcmonitor.ico", "also write this .ico file (empty to skip)")
	flag.StringVar(&arch, "arch", "amd64", "amd64 or 386 (the .syso must match GOARCH: rsrc_windows_386.syso for 386)")
	flag.Parse()

	f, err := os.Open(*in)
	if err != nil {
		log.Fatal(err)
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		log.Fatalf("%s: %v", *in, err)
	}

	// One RT_ICON per size (PNG for 256, a 32-bit DIB for the rest, which
	// every Windows version reads), plus the RT_GROUP_ICON directory.
	var images [][]byte
	for _, s := range sizes {
		img := resize(src, s)
		if s == 256 {
			var b bytes.Buffer
			png.Encode(&b, img)
			images = append(images, b.Bytes())
		} else {
			images = append(images, dib(img))
		}
	}

	res := []resource{{rtGroupIcon, 1, groupIcon(images)}, {rtManifest, 1, []byte(manifest)}}
	for i, data := range images {
		res = append(res, resource{rtIcon, uint16(i + 1), data})
	}
	if err := os.WriteFile(*out, coff(res), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s (%d icon sizes + manifest)\n", *out, len(images))

	if *ico != "" {
		if err := os.WriteFile(*ico, icoFile(images), 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %s\n", *ico)
	}
}

// resize scales src to s×s by averaging the source pixels each target pixel
// covers (alpha-weighted, so edges don't darken).
func resize(src image.Image, s int) *image.NRGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, s, s))
	const n = 8 // samples per axis per target pixel
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			var r, g, bl, a float64
			for sy := 0; sy < n; sy++ {
				for sx := 0; sx < n; sx++ {
					px := b.Min.X + int((float64(x)+(float64(sx)+0.5)/n)*float64(sw)/float64(s))
					py := b.Min.Y + int((float64(y)+(float64(sy)+0.5)/n)*float64(sh)/float64(s))
					cr, cg, cb, ca := src.At(px, py).RGBA() // premultiplied, 16-bit
					r, g, bl, a = r+float64(cr), g+float64(cg), bl+float64(cb), a+float64(ca)
				}
			}
			if a == 0 {
				continue
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = uint8(r / a * 255)
			dst.Pix[i+1] = uint8(g / a * 255)
			dst.Pix[i+2] = uint8(bl / a * 255)
			dst.Pix[i+3] = uint8(a / (n * n) / 257)
		}
	}
	return dst
}

// dib encodes img as an icon image: BITMAPINFOHEADER, bottom-up BGRA pixels,
// then an all-zero AND mask (the alpha channel does the masking).
func dib(img *image.NRGBA) []byte {
	s := img.Bounds().Dx()
	var b bytes.Buffer
	le := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	le(uint32(40))
	le(int32(s))
	le(int32(2 * s)) // XOR + AND mask height
	le(uint16(1))    // planes
	le(uint16(32))   // bits per pixel
	le(uint32(0))    // BI_RGB
	le(uint32(s * s * 4))
	le(int32(0))
	le(int32(0))
	le(uint32(0))
	le(uint32(0))
	for y := s - 1; y >= 0; y-- {
		for x := 0; x < s; x++ {
			i := img.PixOffset(x, y)
			b.Write([]byte{img.Pix[i+2], img.Pix[i+1], img.Pix[i+0], img.Pix[i+3]})
		}
	}
	maskRow := ((s + 31) / 32) * 4
	b.Write(make([]byte, maskRow*s))
	return b.Bytes()
}

// iconEntry writes the size/format part shared by .ico and RT_GROUP_ICON entries.
func iconEntry(b *bytes.Buffer, s int, size int) {
	w := byte(s)
	if s >= 256 {
		w = 0 // 0 means 256
	}
	b.Write([]byte{w, w, 0, 0})
	binary.Write(b, binary.LittleEndian, uint16(1))  // planes
	binary.Write(b, binary.LittleEndian, uint16(32)) // bit count
	binary.Write(b, binary.LittleEndian, uint32(size))
}

// groupIcon builds the RT_GROUP_ICON data: the icon directory pointing at
// RT_ICON resources 1..n.
func groupIcon(images [][]byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, [3]uint16{0, 1, uint16(len(images))})
	for i, data := range images {
		iconEntry(&b, sizes[i], len(data))
		binary.Write(&b, binary.LittleEndian, uint16(i+1)) // RT_ICON id
	}
	return b.Bytes()
}

// icoFile builds a standalone .ico with the same images.
func icoFile(images [][]byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, [3]uint16{0, 1, uint16(len(images))})
	off := 6 + 16*len(images)
	for i, data := range images {
		iconEntry(&b, sizes[i], len(data))
		binary.Write(&b, binary.LittleEndian, uint32(off))
		off += len(data)
	}
	for _, data := range images {
		b.Write(data)
	}
	return b.Bytes()
}

type resource struct {
	typ, id uint16
	data    []byte
}

// coff lays the resources out as a .rsrc section (type → id → language
// directory tree, data entries, data) in an AMD64 COFF object. Each data
// entry's address is fixed up by the linker through an ADDR32NB relocation.
func coff(res []resource) []byte {
	sort.Slice(res, func(i, j int) bool {
		if res[i].typ != res[j].typ {
			return res[i].typ < res[j].typ
		}
		return res[i].id < res[j].id
	})
	var types []uint16
	byType := map[uint16][]resource{}
	for _, r := range res {
		if len(byType[r.typ]) == 0 {
			types = append(types, r.typ)
		}
		byType[r.typ] = append(byType[r.typ], r)
	}
	dirSize := func(n int) int { return 16 + 8*n }

	// Pass 1: offsets.
	off := dirSize(len(types))
	typeDir := map[uint16]int{}
	for _, t := range types {
		typeDir[t] = off
		off += dirSize(len(byType[t]))
	}
	langDir := make([]int, len(res))
	for i := range res {
		langDir[i] = off
		off += dirSize(1)
	}
	dataEntry := make([]int, len(res))
	for i := range res {
		dataEntry[i] = off
		off += 16
	}
	dataOff := make([]int, len(res))
	for i, r := range res {
		off = (off + 7) &^ 7
		dataOff[i] = off
		off += len(r.data)
	}
	sec := make([]byte, (off+7)&^7)

	// Pass 2: fill in.
	put32 := func(at int, v uint32) { binary.LittleEndian.PutUint32(sec[at:], v) }
	put16 := func(at int, v uint16) { binary.LittleEndian.PutUint16(sec[at:], v) }
	dir := func(at, n int) { put16(at+14, uint16(n)) } // all entries are numeric IDs
	const subdir = 0x80000000

	dir(0, len(types))
	for i, t := range types {
		put32(16+8*i, uint32(t))
		put32(16+8*i+4, subdir|uint32(typeDir[t]))
	}
	idx := 0
	for _, t := range types {
		at := typeDir[t]
		dir(at, len(byType[t]))
		for j, r := range byType[t] {
			put32(at+16+8*j, uint32(r.id))
			put32(at+16+8*j+4, subdir|uint32(langDir[idx]))
			idx++
		}
	}
	var relocs []uint32
	for i, r := range res {
		dir(langDir[i], 1)
		put32(langDir[i]+16, langEnUS)
		put32(langDir[i]+20, uint32(dataEntry[i]))
		put32(dataEntry[i], uint32(dataOff[i])) // relocated: section RVA + offset
		put32(dataEntry[i]+4, uint32(len(r.data)))
		relocs = append(relocs, uint32(dataEntry[i]))
		copy(sec[dataOff[i]:], r.data)
	}

	// COFF object: file header, one section header, section data,
	// relocations, symbol table (the section symbol), empty string table.
	var b bytes.Buffer
	le := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	const fileHdr, secHdr, relocSize = 20, 40, 10
	rawPtr := fileHdr + secHdr
	relocPtr := rawPtr + len(sec)
	symPtr := relocPtr + relocSize*len(relocs)

	machine, relType := uint16(0x8664), uint16(0x0003) // IMAGE_FILE_MACHINE_AMD64, IMAGE_REL_AMD64_ADDR32NB
	if arch == "386" {
		machine, relType = 0x014c, 0x0007 // IMAGE_FILE_MACHINE_I386, IMAGE_REL_I386_DIR32NB
	}
	le(machine)
	le(uint16(1)) // sections
	le(uint32(0)) // timestamp
	le(uint32(symPtr))
	le(uint32(1)) // symbols
	le(uint16(0)) // optional header size
	le(uint16(0)) // characteristics

	b.WriteString(".rsrc\x00\x00\x00")
	le(uint32(0)) // virtual size
	le(uint32(0)) // virtual address
	le(uint32(len(sec)))
	le(uint32(rawPtr))
	le(uint32(relocPtr))
	le(uint32(0)) // line numbers
	le(uint16(len(relocs)))
	le(uint16(0))
	le(uint32(0x40000040)) // initialized data, readable

	b.Write(sec)
	for _, r := range relocs {
		le(r)
		le(uint32(0)) // symbol 0: the section
		le(relType)
	}

	b.WriteString(".rsrc\x00\x00\x00")
	le(uint32(0))  // value
	le(int16(1))   // section number
	le(uint16(0))  // type
	b.WriteByte(3) // storage class: static
	b.WriteByte(0) // aux symbols
	le(uint32(4))  // string table size (empty)
	return b.Bytes()
}

// arch is the target of the COFF object (-arch).
var arch = "amd64"
