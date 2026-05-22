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

const (
	mhMagic    = 0xfeedface
	mhMagic64  = 0xfeedfacf
	mhCigam    = 0xcefaedfe
	mhCigam64  = 0xcffaedfe
	fatMagic   = 0xcafebabe
	fatCigam   = 0xbebafeca
	fatMagic64 = 0xcafebabf
	fatCigam64 = 0xbfbafeca

	lcEncryptionInfo   = 0x21
	lcEncryptionInfo64 = 0x2c
)

type VerifyMismatch struct {
	Name   string
	Reason string
}

type VerifyResult struct {
	Scanned        int              // Mach-Os parsed in output IPA
	Compared       int              // additionally byte-checked against source
	StillEncrypted []string         // cryptid != 0 (decrypt didn't write through)
	AllZeroCrypt   []string         // cryptid == 0 but crypt region all zeros (decrypt-on-fault never fired)
	Mismatches     []VerifyMismatch // bytes outside crypt region differ from source
	Missing        []string         // output entry has no source counterpart (only when source given)
	Skipped        []string         // output Mach-O failed to parse
}

func (r VerifyResult) OK() bool {
	return len(r.StillEncrypted) == 0 &&
		len(r.AllZeroCrypt) == 0 &&
		len(r.Mismatches) == 0
}

// Verify scans every Mach-O in the decrypted output IPA, asserting
// cryptid==0 and that the FairPlay crypt region isn't a zero-fill (a
// signal that helper decrypt-on-fault never actually fired). When
// sourceIPA is non-empty, each output is also byte-compared against
// its source slice outside the cryptid byte and the encrypted region,
// catching cryptid-zeroed-without-decrypting and any out-of-band byte
// corruption. When skipAppex is true, entries under Payload/<App>.app
// /PlugIns/ are ignored  the helper left them encrypted on purpose.
//
// Output is THIN  helper drops fat siblings  so each output entry
// maps to one specific slice in a (possibly fat) source.
func Verify(outputIPA, sourceIPA string, skipAppex bool) (VerifyResult, error) {
	var res VerifyResult

	out, err := zip.OpenReader(outputIPA)
	if err != nil {
		return res, fmt.Errorf("open output %s: %w", outputIPA, err)
	}
	defer out.Close()

	var srcByName map[string]*zip.File

	if sourceIPA != "" {
		src, err := zip.OpenReader(sourceIPA)
		if err != nil {
			return res, fmt.Errorf("open source %s: %w", sourceIPA, err)
		}
		defer src.Close()

		srcByName = make(map[string]*zip.File, len(src.File))
		for _, f := range src.File {
			srcByName[f.Name] = f
		}
	}

	for _, of := range out.File {
		if !strings.HasPrefix(of.Name, "Payload/") || of.FileInfo().IsDir() {
			continue
		}

		if skipAppex && isAppExtPath(of.Name) {
			continue
		}

		outData, isMacho, err := readMachO(of)
		if err != nil {
			return res, fmt.Errorf("read %s: %w", of.Name, err)
		}

		if !isMacho {
			continue
		}

		res.Scanned++

		encrypted, err := sliceHasCryptid(outData)
		if err != nil {
			res.Skipped = append(res.Skipped, of.Name)
			continue
		}

		if encrypted {
			res.StillEncrypted = append(res.StillEncrypted, of.Name)
			continue
		}

		// Source-bearing path: precise compare (handles its own all-zero
		// check, gated on srcCrypt.cryptid to avoid false positives on
		// legitimately-plaintext passthrough slices).
		if sf := srcByName[of.Name]; sf != nil {
			srcData, srcIsMacho, err := readMachO(sf)
			if err != nil {
				return res, fmt.Errorf("read source %s: %w", of.Name, err)
			}

			// Source isn't Mach-O (TBD stubs, .a archives, resolved
			// symlinks)  nothing FairPlay touches, skip.
			if !srcIsMacho {
				continue
			}

			if reason := compareMachOSlice(outData, srcData); reason != "" {
				res.Mismatches = append(res.Mismatches, VerifyMismatch{Name: of.Name, Reason: reason})
				continue
			}

			res.Compared++

			continue
		}

		if srcByName != nil {
			res.Missing = append(res.Missing, of.Name)
		}

		// Source-free fallback: flag thin outputs whose entire crypt
		// region is zeros. False positives are possible if the source
		// shipped LC_ENCRYPTION_INFO with cryptid==0 over a genuinely
		// zero region  rare, and source-aware verify catches it.
		if cryptZeroed(outData) {
			res.AllZeroCrypt = append(res.AllZeroCrypt, of.Name)
		}
	}

	return res, nil
}

func cryptZeroed(data []byte) bool {
	if len(data) < 4 {
		return false
	}

	m := binary.LittleEndian.Uint32(data[:4])
	if m != mhMagic && m != mhMagic64 {
		return false
	}

	info, err := readEncryptionInfo(data)
	if err != nil || info.cryptsize == 0 {
		return false
	}

	return isAllZero(data[info.cryptoff : info.cryptoff+info.cryptsize])
}

func isMachOMagic(b []byte) bool {
	if len(b) < 4 {
		return false
	}

	m := binary.LittleEndian.Uint32(b)

	switch m {
	case mhMagic, mhMagic64, mhCigam, mhCigam64, fatMagic, fatCigam, fatMagic64, fatCigam64:
		return true
	}

	return false
}

// readMachO returns the file's bytes if it begins with a Mach-O / fat magic.
// (nil, false, nil) signals "not a Mach-O, skip".
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

// isAppExtPath matches Payload/<App>.app/PlugIns/<*>.appex/...
func isAppExtPath(name string) bool {
	parts := strings.Split(name, "/")

	return len(parts) >= 4 &&
		parts[0] == "Payload" &&
		strings.HasSuffix(parts[1], ".app") &&
		parts[2] == "PlugIns"
}

func sliceHasCryptid(data []byte) (bool, error) {
	if len(data) < 4 {
		return false, errors.New("too short")
	}

	magic := binary.LittleEndian.Uint32(data[:4])
	switch magic {
	case fatMagic, fatCigam:
		return checkFat(data, false)
	case fatMagic64, fatCigam64:
		return checkFat(data, true)
	case mhMagic, mhMagic64:
		return checkThin(data, false)
	case mhCigam, mhCigam64:
		return checkThin(data, true)
	}

	return false, errors.New("not mach-o")
}

func checkFat(data []byte, is64 bool) (bool, error) {
	bo := binary.BigEndian // fat headers are always big-endian on disk

	if len(data) < 8 {
		return false, errors.New("fat header truncated")
	}

	nfat := bo.Uint32(data[4:8])
	if nfat == 0 || nfat > 32 {
		return false, fmt.Errorf("implausible nfat_arch=%d", nfat)
	}

	archSize := 20
	if is64 {
		archSize = 32
	}

	off := 8
	for range nfat {
		if off+archSize > len(data) {
			return false, errors.New("fat_arch truncated")
		}

		var sliceOff, sliceSize uint64
		if is64 {
			sliceOff = bo.Uint64(data[off+8 : off+16])
			sliceSize = bo.Uint64(data[off+16 : off+24])
		} else {
			sliceOff = uint64(bo.Uint32(data[off+8 : off+12]))
			sliceSize = uint64(bo.Uint32(data[off+12 : off+16]))
		}

		off += archSize

		if sliceOff+sliceSize > uint64(len(data)) {
			return false, errors.New("fat slice out of range")
		}

		if sliceSize < 4 {
			continue
		}

		slice := data[sliceOff : sliceOff+sliceSize]
		m := binary.LittleEndian.Uint32(slice[:4])

		enc, err := checkThin(slice, m == mhCigam || m == mhCigam64)
		if err != nil {
			return false, err
		}

		if enc {
			return true, nil
		}
	}

	return false, nil
}

func checkThin(data []byte, swap bool) (bool, error) {
	var bo binary.ByteOrder = binary.LittleEndian
	if swap {
		bo = binary.BigEndian
	}

	if len(data) < 28 {
		return false, errors.New("mach_header truncated")
	}

	magic := bo.Uint32(data[0:4])
	is64 := magic == mhMagic64 || magic == mhCigam64
	ncmds := bo.Uint32(data[16:20])
	sizeofcmds := bo.Uint32(data[20:24])

	headerSize := 28
	if is64 {
		headerSize = 32
	}

	if uint64(headerSize)+uint64(sizeofcmds) > uint64(len(data)) {
		return false, errors.New("load commands truncated")
	}

	if ncmds > 1<<16 {
		return false, fmt.Errorf("implausible ncmds=%d", ncmds)
	}

	p := headerSize
	for i := uint32(0); i < ncmds; i++ {
		if p+8 > len(data) {
			return false, errors.New("load cmd truncated")
		}

		cmd := bo.Uint32(data[p : p+4])

		cmdSize := bo.Uint32(data[p+4 : p+8])
		if cmdSize < 8 || int(cmdSize) > len(data)-p {
			return false, fmt.Errorf("bad cmdsize=%d", cmdSize)
		}

		if (is64 && cmd == lcEncryptionInfo64) || (!is64 && cmd == lcEncryptionInfo) {
			if int(cmdSize) < 20 {
				return false, errors.New("LC_ENCRYPTION_INFO truncated")
			}

			cryptid := bo.Uint32(data[p+16 : p+20])
			if cryptid != 0 {
				return true, nil
			}
		}

		p += int(cmdSize)
	}

	return false, nil
}

// compareMachOSlice returns "" when the output is consistent with the
// source. Two cases:
//   - Output fat → helper didn't touch (no FairPlay slice); must match
//     source verbatim.
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
		// No LC_ENCRYPTION_INFO in output  slice was never encrypted.
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

	// Helper only patches cryptid and decrypts bytes  never moves
	// cryptoff or cryptsize.
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

	outCryptBytes := outData[outCrypt.cryptoff:cryptEnd]
	srcCryptBytes := srcSlice[srcCrypt.cryptoff : srcCrypt.cryptoff+srcCrypt.cryptsize]

	// Source already plaintext: bytes must match verbatim, and the
	// "decrypt bailed" heuristics below would false-flag.
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
	cryptid       uint32
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
