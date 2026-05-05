package pipeline

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
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

	maxLoadCommandsSize = 16 << 20
)

type VerifyResult struct {
	Scanned   int
	Encrypted []string
	Skipped   []string
}

func VerifyCryptid(ipaPath string) (VerifyResult, error) {
	var res VerifyResult

	r, err := zip.OpenReader(ipaPath)
	if err != nil {
		return res, fmt.Errorf("open %s: %w", ipaPath, err)
	}
	defer r.Close()

	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, "Payload/") || f.FileInfo().IsDir() {
			continue
		}

		encrypted, macho, err := zipEntryHasCryptid(f)
		if err != nil {
			res.Skipped = append(res.Skipped, f.Name)
			continue
		}
		if !macho {
			continue
		}

		res.Scanned++
		if encrypted {
			res.Encrypted = append(res.Encrypted, f.Name)
		}
	}

	return res, nil
}

func zipEntryHasCryptid(f *zip.File) (encrypted, macho bool, err error) {
	r, err := f.Open()
	if err != nil {
		return false, false, fmt.Errorf("open %s: %w", f.Name, err)
	}
	defer r.Close()

	encrypted, err = readerHasCryptid(r)
	if errors.Is(err, errNotMachO) {
		return false, false, nil
	}
	if err != nil {
		return false, true, err
	}

	return encrypted, true, nil
}

var errNotMachO = errors.New("not mach-o")

func isMachOMagic(b []byte) bool {
	if len(b) < 4 {
		return false
	}

	switch binary.LittleEndian.Uint32(b) {
	case mhMagic, mhMagic64, mhCigam, mhCigam64, fatMagic, fatCigam, fatMagic64, fatCigam64:
		return true
	}

	return false
}

func readerHasCryptid(r io.Reader) (bool, error) {
	magicBytes, err := readExact(r, 4)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return false, errNotMachO
		}
		return false, err
	}

	magic := binary.LittleEndian.Uint32(magicBytes)
	switch magic {
	case fatMagic, fatCigam:
		return checkFatReader(r, false)
	case fatMagic64, fatCigam64:
		return checkFatReader(r, true)
	case mhMagic, mhMagic64:
		encrypted, _, err := checkThinReader(r, magic, binary.LittleEndian)
		return encrypted, err
	case mhCigam, mhCigam64:
		encrypted, _, err := checkThinReader(r, magic, binary.BigEndian)
		return encrypted, err
	default:
		return false, errNotMachO
	}
}

type fatSlice struct {
	off  uint64
	size uint64
}

func checkFatReader(r io.Reader, is64 bool) (bool, error) {
	bo := binary.BigEndian

	nfatBytes, err := readExact(r, 4)
	if err != nil {
		return false, errors.New("fat header truncated")
	}

	nfat := bo.Uint32(nfatBytes)
	if nfat == 0 || nfat > 32 {
		return false, fmt.Errorf("implausible nfat_arch=%d", nfat)
	}

	archSize := 20
	if is64 {
		archSize = 32
	}

	archBytes, err := readExact(r, int(uint64(archSize)*uint64(nfat)))
	if err != nil {
		return false, errors.New("fat_arch truncated")
	}

	slices := make([]fatSlice, 0, nfat)
	for i := uint32(0); i < nfat; i++ {
		off := int(i) * archSize

		var sliceOff, sliceSize uint64
		if is64 {
			sliceOff = bo.Uint64(archBytes[off+8 : off+16])
			sliceSize = bo.Uint64(archBytes[off+16 : off+24])
		} else {
			sliceOff = uint64(bo.Uint32(archBytes[off+8 : off+12]))
			sliceSize = uint64(bo.Uint32(archBytes[off+12 : off+16]))
		}

		if sliceSize >= 4 {
			slices = append(slices, fatSlice{off: sliceOff, size: sliceSize})
		}
	}

	sort.Slice(slices, func(i, j int) bool { return slices[i].off < slices[j].off })

	pos := uint64(8 + archSize*int(nfat))
	for _, s := range slices {
		if s.off < pos {
			return false, errors.New("fat slice overlaps header or previous slice")
		}

		if _, err := io.CopyN(io.Discard, r, int64(s.off-pos)); err != nil {
			return false, errors.New("fat slice truncated")
		}
		pos = s.off

		magicBytes, err := readExact(r, 4)
		if err != nil {
			return false, errors.New("fat slice magic truncated")
		}
		pos += 4

		magic := binary.LittleEndian.Uint32(magicBytes)
		var sliceBO binary.ByteOrder
		switch magic {
		case mhMagic, mhMagic64:
			sliceBO = binary.LittleEndian
		case mhCigam, mhCigam64:
			sliceBO = binary.BigEndian
		default:
			continue
		}

		encrypted, consumed, err := checkThinReader(r, magic, sliceBO)
		if err != nil {
			return false, err
		}
		if 4+consumed > s.size {
			return false, errors.New("fat slice load commands exceed slice size")
		}
		pos += consumed
		if encrypted {
			return true, nil
		}
	}

	return false, nil
}

func checkThinReader(r io.Reader, magic uint32, bo binary.ByteOrder) (encrypted bool, consumed uint64, err error) {
	is64 := magic == mhMagic64 || magic == mhCigam64
	headerSize := 28
	if is64 {
		headerSize = 32
	}

	headerRest, err := readExact(r, headerSize-4)
	if err != nil {
		return false, 0, errors.New("mach_header truncated")
	}
	consumed = uint64(headerSize - 4)

	ncmds := bo.Uint32(headerRest[12:16])
	sizeofcmds := bo.Uint32(headerRest[16:20])
	if ncmds > 1<<16 {
		return false, consumed, fmt.Errorf("implausible ncmds=%d", ncmds)
	}
	if sizeofcmds > maxLoadCommandsSize {
		return false, consumed, fmt.Errorf("implausible sizeofcmds=%d", sizeofcmds)
	}

	cmds, err := readExact(r, int(sizeofcmds))
	if err != nil {
		return false, consumed, errors.New("load commands truncated")
	}
	consumed += uint64(sizeofcmds)

	p := 0
	for i := uint32(0); i < ncmds; i++ {
		if p+8 > len(cmds) {
			return false, consumed, errors.New("load cmd truncated")
		}

		cmd := bo.Uint32(cmds[p : p+4])
		cmdSize := bo.Uint32(cmds[p+4 : p+8])
		if cmdSize < 8 || int(cmdSize) > len(cmds)-p {
			return false, consumed, fmt.Errorf("bad cmdsize=%d", cmdSize)
		}

		if (is64 && cmd == lcEncryptionInfo64) || (!is64 && cmd == lcEncryptionInfo) {
			if int(cmdSize) < 20 {
				return false, consumed, errors.New("LC_ENCRYPTION_INFO truncated")
			}
			return bo.Uint32(cmds[p+16:p+20]) != 0, consumed, nil
		}

		p += int(cmdSize)
	}

	return false, consumed, nil
}

func readExact(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}
