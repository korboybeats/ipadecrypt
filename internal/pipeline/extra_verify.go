package pipeline

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ExtraVerifyMismatch describes one Mach-O whose plaintext bytes outside
// the encrypted region don't match the source IPA.
type ExtraVerifyMismatch struct {
	Name   string
	Reason string
}

type ExtraVerifyResult struct {
	Compared   int
	Mismatches []ExtraVerifyMismatch
	Missing    []string // output entries with no source counterpart
}

// ExtraVerify compares each Mach-O in the decrypted output IPA against
// the matching slice in the source (encrypted) IPA byte-for-byte, except
// for two regions: the cryptid byte (always 0 in output, 1 in source)
// and [cryptoff, cryptoff+cryptsize) which is encrypted in source. Any
// other diff means the helper either corrupted bytes or sliced the
// wrong region. Output is THIN — helper drops fat siblings — so each
// output entry maps to one specific slice in a (possibly fat) source.
func ExtraVerify(outputIPA, sourceIPA string) (ExtraVerifyResult, error) {
	var res ExtraVerifyResult

	out, err := zip.OpenReader(outputIPA)
	if err != nil {
		return res, fmt.Errorf("open output %s: %w", outputIPA, err)
	}
	defer out.Close()

	src, err := zip.OpenReader(sourceIPA)
	if err != nil {
		return res, fmt.Errorf("open source %s: %w", sourceIPA, err)
	}
	defer src.Close()

	srcByName := make(map[string]*zip.File, len(src.File))
	for _, f := range src.File {
		srcByName[f.Name] = f
	}

	for _, of := range out.File {
		if !strings.HasPrefix(of.Name, "Payload/") || of.FileInfo().IsDir() {
			continue
		}

		outData, ok, err := readMachO(of)
		if err != nil {
			return res, fmt.Errorf("read output %s: %w", of.Name, err)
		}

		if !ok {
			continue
		}

		sf := srcByName[of.Name]
		if sf == nil {
			res.Missing = append(res.Missing, of.Name)
			continue
		}

		srcData, srcIsMacho, err := readMachO(sf)
		if err != nil {
			return res, fmt.Errorf("read source %s: %w", of.Name, err)
		}

		// Source isn't a Mach-O (TBD stubs, .a archives the OS misclassifies,
		// resolved symlinks, etc.) — nothing FairPlay touches, skip.
		if !srcIsMacho {
			continue
		}

		if reason := compareMachOSlice(outData, srcData); reason != "" {
			res.Mismatches = append(res.Mismatches, ExtraVerifyMismatch{Name: of.Name, Reason: reason})
			continue
		}

		res.Compared++
	}

	return res, nil
}

// readMachO returns the file's bytes if it begins with a Mach-O / fat magic.
// (false, nil) signals "not a Mach-O, skip".
func readMachO(f *zip.File) ([]byte, bool, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, false, err
	}
	defer rc.Close()

	head := make([]byte, 4)
	if n, _ := io.ReadFull(rc, head); n < 4 || !isMachOMagic(head) {
		return nil, false, nil
	}

	rest, err := io.ReadAll(rc)
	if err != nil {
		return nil, false, err
	}

	return append(head, rest...), true, nil
}

// compareMachOSlice returns "" when the output is consistent with the
// source. Two cases:
//   - Output fat → helper didn't touch this file (no FairPlay slice
//     present); bytes must match source verbatim.
//   - Output thin → helper decrypted one slice and dropped fat siblings;
//     pick the matching cputype slice in source and byte-compare outside
//     the encrypted region + cryptid byte.
func compareMachOSlice(outData, srcData []byte) string {
	if len(outData) < 4 {
		return "output too short"
	}

	outMagic := binary.LittleEndian.Uint32(outData[:4])
	outIsFat := outMagic == fatMagic || outMagic == fatCigam ||
		outMagic == fatMagic64 || outMagic == fatCigam64

	if outIsFat {
		if !bytes.Equal(outData, srcData) {
			return "fat passthrough differs from source"
		}

		return ""
	}

	outCpu, outSub, err := readThinCpu(outData)
	if err != nil {
		return "parse output cpu: " + err.Error()
	}

	srcSlice, err := pickMatchingSlice(srcData, outCpu, outSub)
	if err != nil {
		return "match source slice: " + err.Error()
	}

	outCrypt, err := readEncryptionInfo(outData)
	if err != nil {
		// No LC_ENCRYPTION_INFO in output — the slice was never encrypted.
		// Direct compare without the cryptid/cryptoff skip.
		if bytes.Equal(outData, srcSlice) {
			return ""
		}

		return "thin passthrough differs from source slice"
	}

	srcCrypt, err := readEncryptionInfo(srcSlice)
	if err != nil {
		return "parse source LC_ENCRYPTION_INFO: " + err.Error()
	}

	// Both must agree on encrypted region geometry; helper only patches
	// cryptid and decrypts the bytes, never moves cryptoff or cryptsize.
	if outCrypt.cryptoff != srcCrypt.cryptoff ||
		outCrypt.cryptsize != srcCrypt.cryptsize {
		return fmt.Sprintf(
			"cryptoff/cryptsize moved: output=(%d,%d) source=(%d,%d)",
			outCrypt.cryptoff, outCrypt.cryptsize,
			srcCrypt.cryptoff, srcCrypt.cryptsize,
		)
	}

	if len(outData) != len(srcSlice) {
		return fmt.Sprintf("size differs: output=%d source=%d", len(outData), len(srcSlice))
	}

	// Compare:
	//   [0, cryptidOffset)           must match
	//   [cryptidOffset+4, cryptoff)  must match
	//   [cryptoff+cryptsize, end)    must match
	cryptidEnd := outCrypt.cryptidOffset + 4
	cryptEnd := outCrypt.cryptoff + outCrypt.cryptsize

	if !bytes.Equal(outData[:outCrypt.cryptidOffset], srcSlice[:outCrypt.cryptidOffset]) {
		return "header diff before cryptid"
	}

	if !bytes.Equal(outData[cryptidEnd:outCrypt.cryptoff], srcSlice[cryptidEnd:outCrypt.cryptoff]) {
		return "header/load-commands diff between cryptid and cryptoff"
	}

	if cryptEnd < uint64(len(outData)) &&
		!bytes.Equal(outData[cryptEnd:], srcSlice[cryptEnd:]) {
		return "diff after encrypted region (LINKEDIT/etc)"
	}

	// Now scrutinise the decrypted region itself. Helper writes cryptid=0
	// even when the decrypt step bailed mid-flight, so cryptid==0 alone
	// doesn't prove the bytes are plaintext.
	outCryptBytes := outData[outCrypt.cryptoff:cryptEnd]
	srcCryptBytes := srcSlice[srcCrypt.cryptoff : srcCrypt.cryptoff+srcCrypt.cryptsize]

	// If the source slice already shipped with cryptid=0, FairPlay never
	// touched these bytes — they were always plaintext (typical of
	// developer-bundled dylibs and 3rd-party SDKs Apple distributes
	// pre-thinned). Helper correctly took the no-op path; output should
	// equal source byte-for-byte. Skip the "byte-equal == decrypt bailed"
	// rule here, otherwise we false-flag every legitimate passthrough.
	if srcCrypt.cryptid == 0 {
		return ""
	}

	if isAllZero(outCryptBytes) {
		return "crypt region is all zeros (FairPlay decrypt-on-fault never fired in target)"
	}

	if bytes.Equal(outCryptBytes, srcCryptBytes) {
		return "crypt region byte-equal to source ciphertext (cryptid zeroed without decrypting)"
	}

	return ""
}

func isAllZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}

	return true
}

func readThinCpu(data []byte) (uint32, uint32, error) {
	if len(data) < 16 {
		return 0, 0, errors.New("mach_header truncated")
	}

	bo := binary.LittleEndian
	magic := bo.Uint32(data[0:4])

	if magic != mhMagic && magic != mhMagic64 {
		return 0, 0, fmt.Errorf("not a thin Mach-O magic=0x%x", magic)
	}

	return bo.Uint32(data[4:8]), bo.Uint32(data[8:12]), nil
}

// pickMatchingSlice returns the slice (or whole file when thin) of `data`
// whose (cputype, cpusubtype masking off feature bits) matches.
func pickMatchingSlice(data []byte, cpu, sub uint32) ([]byte, error) {
	if len(data) < 4 {
		return nil, errors.New("source too short")
	}

	magic := binary.LittleEndian.Uint32(data[:4])
	switch magic {
	case mhMagic, mhMagic64:
		c, s, err := readThinCpu(data)
		if err != nil {
			return nil, err
		}

		if c == cpu && (s&0x00ffffff) == (sub&0x00ffffff) {
			return data, nil
		}

		return nil, fmt.Errorf("source cpu mismatch: have (0x%x,0x%x) want (0x%x,0x%x)", c, s, cpu, sub)
	case fatMagic, fatMagic64:
		return pickFatSlice(data, magic == fatMagic64, cpu, sub)
	}

	return nil, fmt.Errorf("source not Mach-O magic=0x%x", magic)
}

func pickFatSlice(data []byte, is64 bool, cpu, sub uint32) ([]byte, error) {
	bo := binary.BigEndian

	if len(data) < 8 {
		return nil, errors.New("fat truncated")
	}

	nfat := bo.Uint32(data[4:8])
	if nfat == 0 || nfat > 32 {
		return nil, fmt.Errorf("implausible nfat_arch=%d", nfat)
	}

	archSize := 20
	if is64 {
		archSize = 32
	}

	off := 8
	for range nfat {
		if off+archSize > len(data) {
			return nil, errors.New("fat_arch truncated")
		}

		cputype := bo.Uint32(data[off : off+4])
		cpusubtype := bo.Uint32(data[off+4 : off+8])

		var sliceOff, sliceSize uint64
		if is64 {
			sliceOff = bo.Uint64(data[off+8 : off+16])
			sliceSize = bo.Uint64(data[off+16 : off+24])
		} else {
			sliceOff = uint64(bo.Uint32(data[off+8 : off+12]))
			sliceSize = uint64(bo.Uint32(data[off+12 : off+16]))
		}

		off += archSize

		if cputype == cpu && (cpusubtype&0x00ffffff) == (sub&0x00ffffff) {
			if sliceOff+sliceSize > uint64(len(data)) {
				return nil, errors.New("fat slice out of range")
			}

			return data[sliceOff : sliceOff+sliceSize], nil
		}
	}

	return nil, fmt.Errorf("no fat slice matched cputype=0x%x cpusubtype=0x%x", cpu, sub)
}

type encryptionInfo struct {
	cryptidOffset uint64 // file offset of the cryptid u32 within the slice
	cryptoff      uint64
	cryptsize     uint64
	cryptid       uint32 // current cryptid value at that offset
}

func readEncryptionInfo(data []byte) (encryptionInfo, error) {
	var info encryptionInfo

	if len(data) < 28 {
		return info, errors.New("mach_header truncated")
	}

	bo := binary.LittleEndian
	magic := bo.Uint32(data[0:4])
	is64 := magic == mhMagic64

	if magic != mhMagic && magic != mhMagic64 {
		return info, fmt.Errorf("not a thin LE Mach-O magic=0x%x", magic)
	}

	ncmds := bo.Uint32(data[16:20])
	sizeofcmds := bo.Uint32(data[20:24])

	headerSize := uint64(28)
	if is64 {
		headerSize = 32
	}

	if headerSize+uint64(sizeofcmds) > uint64(len(data)) {
		return info, errors.New("load commands truncated")
	}

	p := headerSize
	for range ncmds {
		if p+8 > uint64(len(data)) {
			return info, errors.New("load cmd truncated")
		}

		cmd := bo.Uint32(data[p : p+4])
		cmdSize := bo.Uint32(data[p+4 : p+8])

		if cmdSize < 8 || uint64(cmdSize) > uint64(len(data))-p {
			return info, fmt.Errorf("bad cmdsize=%d", cmdSize)
		}

		want := uint32(lcEncryptionInfo)
		if is64 {
			want = lcEncryptionInfo64
		}

		if cmd == want {
			if cmdSize < 20 {
				return info, errors.New("LC_ENCRYPTION_INFO truncated")
			}

			info.cryptoff = uint64(bo.Uint32(data[p+8 : p+12]))
			info.cryptsize = uint64(bo.Uint32(data[p+12 : p+16]))
			info.cryptidOffset = p + 16
			info.cryptid = bo.Uint32(data[p+16 : p+20])

			return info, nil
		}

		p += uint64(cmdSize)
	}

	return info, errors.New("no LC_ENCRYPTION_INFO load command")
}
